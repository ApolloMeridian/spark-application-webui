package org.sparkconsole;

import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.HashSet;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.TreeSet;
import java.util.concurrent.Executors;
import org.apache.spark.sql.catalyst.analysis.UnresolvedFunction;
import org.apache.spark.sql.catalyst.analysis.UnresolvedRelation;
import org.apache.spark.sql.catalyst.analysis.UnresolvedIdentifier;
import org.apache.spark.sql.catalyst.analysis.UnresolvedNamespace;
import org.apache.spark.sql.catalyst.analysis.UnresolvedTable;
import org.apache.spark.sql.catalyst.analysis.UnresolvedTableOrView;
import org.apache.spark.sql.catalyst.analysis.UnresolvedTableValuedFunction;
import org.apache.spark.sql.catalyst.analysis.UnresolvedView;
import org.apache.spark.sql.catalyst.analysis.RelationTimeTravel;
import org.apache.spark.sql.catalyst.analysis.UnresolvedInlineTable;
import org.apache.spark.sql.catalyst.analysis.UnresolvedSubqueryColumnAliases;
import org.apache.spark.sql.catalyst.analysis.FunctionRegistry;
import org.apache.spark.sql.catalyst.FunctionIdentifier;
import org.apache.spark.sql.catalyst.expressions.Expression;
import org.apache.spark.sql.catalyst.expressions.Literal;
import org.apache.spark.sql.catalyst.expressions.SubqueryExpression;
import org.apache.spark.sql.catalyst.parser.ParseException;
import org.apache.spark.sql.catalyst.parser.extensions.IcebergSparkSqlExtensionsParser;
import org.apache.spark.sql.catalyst.plans.logical.*;
import org.apache.spark.sql.execution.SparkSqlParser;
import org.apache.spark.sql.execution.command.ExplainCommand;
import org.apache.spark.sql.execution.command.CreateViewCommand;
import org.apache.spark.sql.execution.command.CreateTableLikeCommand;
import org.apache.spark.sql.execution.command.DescribeQueryCommand;
import org.apache.spark.sql.types.StructType;
import org.apache.spark.sql.types.DataTypes;
import org.apache.spark.unsafe.types.UTF8String;
import org.apache.spark.sql.util.CaseInsensitiveStringMap;
import scala.collection.Seq;
import scala.collection.Iterator;

public final class SqlGuard {
    private final String namespace;
    private final Set<String> tempViews;
    private final Set<String> historyTables = new TreeSet<>();
    private final Set<String> auditReferences = new TreeSet<>();
    private String operation = "";

    private SqlGuard(String namespace, Set<String> tempViews) {
        this.namespace = namespace;
        this.tempViews = tempViews;
    }
    private static final Set<Class<? extends LogicalPlan>> PLANS = Set.of(
            Project.class, Filter.class, Aggregate.class, Sort.class, GlobalLimit.class, LocalLimit.class,
            Distinct.class, Join.class, Union.class, Intersect.class, Except.class, SubqueryAlias.class,
            UnresolvedRelation.class, UnresolvedWith.class, OneRowRelation.class, Window.class,
            RepartitionByExpression.class, Repartition.class, Offset.class, UnresolvedInlineTable.class,
            Generate.class, UnresolvedTableValuedFunction.class, Sample.class, UnresolvedHint.class, Pivot.class,
            Unpivot.class, WithWindowDefinition.class, UnresolvedSubqueryColumnAliases.class, Tail.class);
    private static final Set<String> UNSAFE_FUNCTIONS = Set.of("reflect", "reflect2", "java_method",
            "raise_error", "assert_true", "input_file_name", "input_file_block_start", "input_file_block_length");
    private static final Set<String> TABLE_FUNCTIONS = Set.of("range", "explode", "explode_outer",
            "inline", "inline_outer", "posexplode", "posexplode_outer", "stack", "json_tuple");
    private static final Set<String> TABLE_PROPERTIES = Set.of("format-version", "comment",
            "write.format.default", "write.parquet.compression-codec", "write.orc.compression-codec",
            "write.avro.compression-codec", "write.distribution-mode", "write.target-file-size-bytes",
            "write.delete.mode", "write.update.mode", "write.merge.mode", "write.delete.isolation-level",
            "write.update.isolation-level", "write.merge.isolation-level");
    private static final Set<String> PERFORMANCE_PROPERTIES = Set.of(
            "write.parquet.row-group-size-bytes", "write.parquet.page-size-bytes",
            "write.parquet.dict-size-bytes", "write.metadata.metrics.default",
            "commit.retry.num-retries", "commit.retry.min-wait-ms", "commit.retry.max-wait-ms",
            "commit.retry.total-timeout-ms");
    private static final Set<String> RETENTION_PROPERTIES = Set.of(
            "history.expire.max-snapshot-age-ms", "history.expire.min-snapshots-to-keep",
            "history.expire.max-ref-age-ms");
    private static final Set<String> ICEBERG_METADATA_TABLES = Set.of(
            "entries", "files", "data_files", "delete_files", "history", "snapshots",
            "manifests", "partitions", "refs", "all_data_files", "all_delete_files",
            "all_files", "all_manifests", "all_entries", "position_deletes", "metadata_log_entries");

    private static String identifier(String value) {
        if (!value.matches("[a-z_][a-z0-9_-]{0,127}")) {
            throw new UnsupportedOperationException("Unsupported identifier");
        }
        return value;
    }

    private void expression(Expression expression, Set<String> ctes, Set<String> tables) {
        if (expression instanceof UnresolvedFunction) {
            UnresolvedFunction function = (UnresolvedFunction) expression;
            String name = function.nameParts().head().toLowerCase(Locale.ROOT);
            if (function.nameParts().size() != 1 || UNSAFE_FUNCTIONS.contains(name) ||
                    !FunctionRegistry.builtin().functionExists(new FunctionIdentifier(name))) {
                throw new UnsupportedOperationException("Unsupported function");
            }
        }
        if (expression instanceof SubqueryExpression) {
            plan((LogicalPlan) ((SubqueryExpression) expression).plan(), ctes, tables);
        }
        Iterator<Expression> children = expression.children().iterator();
        while (children.hasNext()) expression(children.next(), ctes, tables);
    }

    private void plan(LogicalPlan plan, Set<String> ctes, Set<String> tables) {
        if (plan instanceof RelationTimeTravel) {
            var travel = (RelationTimeTravel)plan;
            if (!(travel.relation() instanceof UnresolvedRelation)) {
                throw new UnsupportedOperationException("Time travel requires a managed Iceberg table");
            }
            var relation = (UnresolvedRelation)travel.relation();
            var parts = relation.multipartIdentifier();
            if (!relation.options().isEmpty() ||
                    (parts.size() == 1 && (ctes.contains(alias(parts.head())) || tempViews.contains(alias(parts.head()))))) {
                throw new UnsupportedOperationException("Time travel requires a managed Iceberg table");
            }
            String table = resource(parts, false);
            if (travel.version().isDefined()) {
                var version = travel.version().get();
                if (!validReference(version)) {
                    throw new UnsupportedOperationException("Unsupported snapshot selector");
                }
                auditReference("version", version);
            } else if (travel.timestamp().isDefined() && travel.timestamp().get() instanceof Literal) {
                var cutoff = ((Literal)travel.timestamp().get()).value();
                if (cutoff == null) throw new UnsupportedOperationException("Use a literal time travel timestamp");
                auditReference("time", cutoff);
            } else {
                throw new UnsupportedOperationException("Use a literal time travel timestamp");
            }
            historyTables.add(table);
            tables.add(table);
            return;
        }
        if (!PLANS.contains(plan.getClass())) {
            throw new UnsupportedOperationException("Unsupported SQL operation");
        }
        if (plan instanceof UnresolvedWith) {
            UnresolvedWith with = (UnresolvedWith) plan;
            Set<String> aliases = new HashSet<>(ctes);
            var items = with.cteRelations().iterator();
            while (items.hasNext()) {
                var item = items.next();
                plan(item._2(), aliases, tables);
                aliases.add(alias(item._1()));
            }
            plan(with.child(), aliases, tables);
            return;
        }
        if (plan instanceof UnresolvedRelation) {
            UnresolvedRelation table = (UnresolvedRelation) plan;
            var parts = table.multipartIdentifier();
            if (parts.size() == 1 && ctes.contains(alias(parts.head()))) return;
            if (!table.options().isEmpty()) {
                throw new UnsupportedOperationException("Unsupported relation options");
            }
            tables.add(parts.size() == 1 && tempViews.contains(alias(parts.head()))
                    ? alias(parts.head()) : resource(parts, false, true, true));
        }
        if (plan instanceof UnresolvedTableValuedFunction) {
            var function = (UnresolvedTableValuedFunction) plan;
            if (function.name().size() != 1 || !TABLE_FUNCTIONS.contains(function.name().head().toLowerCase(Locale.ROOT))) {
                throw new UnsupportedOperationException("Unsupported table function");
            }
        }
        Iterator<Expression> expressions = plan.expressions().iterator();
        while (expressions.hasNext()) expression(expressions.next(), ctes, tables);
        Iterator<LogicalPlan> children = plan.children().iterator();
        while (children.hasNext()) plan(children.next(), ctes, tables);
    }

    private static String alias(String name) {
        return identifier(name.toLowerCase(Locale.ROOT));
    }

    private String resource(Seq<String> parts, boolean isNamespace) {
        return resource(parts, isNamespace, false, false);
    }

    private String resource(Seq<String> parts, boolean isNamespace, boolean allowMetadata, boolean allowBranch) {
        if (parts.size() < (isNamespace ? 0 : 1) || parts.size() > (isNamespace ? 7 : allowMetadata || allowBranch ? 9 : 8)) {
            throw new UnsupportedOperationException("Qualify the Polaris resource");
        }
        if (parts.nonEmpty() && (parts.head().equalsIgnoreCase("spark_catalog") || parts.head().equalsIgnoreCase("global_temp"))) {
            throw new UnsupportedOperationException("Only the managed Polaris catalog is available");
        }
        StringBuilder name = new StringBuilder(!isNamespace && parts.size() == 1 && !namespace.isEmpty() ? namespace : "polaris");
        var values = parts.iterator();
        if (values.hasNext() && parts.head().equals("polaris")) values.next();
        while (values.hasNext()) name.append('.').append(identifier(values.next()));
        if (!isNamespace) {
            String[] segments = name.toString().split("\\.");
            String last = segments[segments.length - 1];
            boolean branch = branchTable(segments);
            if (segments.length > 3 && ((!allowBranch || !branch) && last.matches("(branch|tag|snapshot_id|at_timestamp)_.+"))) {
                throw new UnsupportedOperationException("Use VERSION AS OF for explicit historical reads");
            }
            boolean metadata = metadataTable(segments);
            if (segments.length < 3 || (metadata && !allowMetadata) ||
                    segments.length > (metadata || branch ? 9 : 8)) {
                throw new UnsupportedOperationException("Unsupported Iceberg table identifier");
            }
        }
        return name.toString();
    }

    private static boolean metadataTable(String[] segments) {
        return segments.length > 3 && ICEBERG_METADATA_TABLES.contains(segments[segments.length - 1]);
    }

    private static boolean branchTable(String[] segments) {
        return segments.length > 3 && segments[segments.length - 1].matches("branch_[a-z_][a-z0-9_-]{0,120}");
    }

    private String tableWrite(String action, LogicalPlan plan) {
        String name = object(plan, true);
        if (branchTable(name.split("\\."))) {
            auditReference("branch", name.substring(name.lastIndexOf('.') + 8));
            return access("branchTable", action, name);
        }
        return access("table", action, name);
    }

    private String object(LogicalPlan plan) {
        return object(plan, false);
    }

    private String object(LogicalPlan plan, boolean allowBranch) {
        if (plan instanceof SubqueryAlias) return object(((SubqueryAlias)plan).child(), allowBranch);
        if (plan instanceof UnresolvedRelation) {
            var relation = (UnresolvedRelation)plan;
            if (!writeOptionsOnly(relation.options())) throw new UnsupportedOperationException("Unsupported relation options");
            return resource(relation.multipartIdentifier(), false, false, allowBranch);
        }
        if (plan instanceof UnresolvedIdentifier) return resource(((UnresolvedIdentifier)plan).nameParts(), false, false, allowBranch);
        if (plan instanceof UnresolvedTableOrView) return resource(((UnresolvedTableOrView)plan).multipartIdentifier(), false, false, allowBranch);
        if (plan instanceof UnresolvedTable) return resource(((UnresolvedTable)plan).multipartIdentifier(), false, false, allowBranch);
        if (plan instanceof UnresolvedView) return resource(((UnresolvedView)plan).multipartIdentifier(), false);
        throw new UnsupportedOperationException("Unsupported catalog object");
    }

    static boolean writeOptionsOnly(CaseInsensitiveStringMap options) {
        String required = options.get("__required_write_privileges__");
        return options.isEmpty() || (options.size() == 1 && required != null &&
                required.matches("(INSERT|UPDATE|DELETE)(,(INSERT|UPDATE|DELETE))*"));
    }

    private String namespace(LogicalPlan plan) {
        if (plan instanceof UnresolvedNamespace) return resource(((UnresolvedNamespace)plan).multipartIdentifier(), true);
        if (plan.getClass().getSimpleName().equals("CurrentNamespace")) return namespace.isEmpty() ? "polaris" : namespace;
        throw new UnsupportedOperationException("Unsupported catalog namespace");
    }

    private static void properties(scala.collection.Iterator<String> keys, Set<String> allowed) {
        while (keys.hasNext()) {
            if (!allowed.contains(keys.next())) throw new UnsupportedOperationException("Property is managed by the platform");
        }
    }

    private static void tableSpec(TableSpecBase specification) {
        if (!(specification instanceof UnresolvedTableSpec) ||
                specification.external() || specification.location().nonEmpty() ||
                specification.serde().nonEmpty() || specification.provider().isEmpty() ||
                !specification.provider().get().equalsIgnoreCase("iceberg") ||
                ((UnresolvedTableSpec)specification).optionExpression().options().nonEmpty()) {
            throw new UnsupportedOperationException("Use a managed Iceberg table");
        }
        var keys = specification.properties().keysIterator();
        while (keys.hasNext()) {
            var key = keys.next();
            var value = specification.properties().apply(key);
            if (key.equals("format-version") ? !Set.of("1", "2").contains(value) : !TABLE_PROPERTIES.contains(key)) {
                throw new UnsupportedOperationException("Property is managed by the platform");
            }
        }
    }

    private static void validateSchema(StructType schema) {
        for (var field : schema.fields()) {
            if (field.metadata().contains("CURRENT_DEFAULT") || field.metadata().contains("EXISTS_DEFAULT")) {
                throw new UnsupportedOperationException("Column defaults are managed by the platform");
            }
        }
    }

    private static boolean tablePropertiesAreRetentionManaged(Iterator<String> keys,
            scala.collection.immutable.Map<String, String> settings) {
        boolean retention = false;
        while (keys.hasNext()) {
            String key = keys.next();
            if ((!TABLE_PROPERTIES.contains(key) || key.equals("format-version")) &&
                    !PERFORMANCE_PROPERTIES.contains(key) && !RETENTION_PROPERTIES.contains(key)) {
                throw new UnsupportedOperationException("Property is managed by the platform");
            }
            if (RETENTION_PROPERTIES.contains(key)) {
                retention = true;
                if (settings != null) {
                    try {
                        long value = Long.parseLong(settings.apply(key));
                        long minimum = key.endsWith("-ms") ? 86_400_000L : 1;
                        if (value < minimum) throw new NumberFormatException();
                    } catch (NumberFormatException failure) {
                        throw new UnsupportedOperationException("Retention requires at least one day or one snapshot");
                    }
                }
            }
        }
        return retention;
    }

    private static boolean hasColumnDefault(QualifiedColType column) throws ReflectiveOperationException {
        // Scala's named accessor is a Java keyword. Inspect the expression without evaluating it.
        return ((scala.Option<?>)QualifiedColType.class.getMethod("default").invoke(column)).nonEmpty();
    }

    private static Seq<String> extensionTable(LogicalPlan plan) {
        if (plan instanceof AddPartitionField) return ((AddPartitionField)plan).table();
        if (plan instanceof DropPartitionField) return ((DropPartitionField)plan).table();
        if (plan instanceof ReplacePartitionField) return ((ReplacePartitionField)plan).table();
        if (plan instanceof SetWriteDistributionAndOrdering) return ((SetWriteDistributionAndOrdering)plan).table();
        if (plan instanceof SetIdentifierFields) return ((SetIdentifierFields)plan).table();
        if (plan instanceof DropIdentifierFields) return ((DropIdentifierFields)plan).table();
        throw new UnsupportedOperationException("Unsupported Iceberg table operation");
    }

    private String maintenance(CallStatement call) throws Exception {
        var name = call.name();
        if (name.size() != 3 || !name.apply(0).equalsIgnoreCase("polaris") || !name.apply(1).equalsIgnoreCase("system")) {
            throw new UnsupportedOperationException("Use a managed Polaris system procedure");
        }
        operation = name.apply(2).toLowerCase(Locale.ROOT);
        var positional = maintenanceParameters(operation);
        var values = new HashMap<String, Literal>();
        var args = call.args().iterator();
        boolean named = call.args().nonEmpty() && call.args().head() instanceof NamedArgument;
        boolean rewriteOptionsSeen = false;
        int index = 0;
        while (args.hasNext()) {
            var arg = args.next();
            if ((named && !(arg instanceof NamedArgument)) ||
                    (!named && (!(arg instanceof PositionalArgument) || index >= positional.size()))) {
                throw new UnsupportedOperationException("Unsupported maintenance argument list");
            }
            var key = named ? ((NamedArgument)arg).name().toLowerCase(Locale.ROOT) : positional.get(index++);
            var expression = named ? ((NamedArgument)arg).expr() : ((PositionalArgument)arg).expr();
            if (key.equals("options") && (operation.equals("rewrite_data_files") || operation.equals("rewrite_position_delete_files"))) {
                if (rewriteOptionsSeen) throw invalidMaintenanceArguments();
                inspectRewriteOptions(expression);
                rewriteOptionsSeen = true;
                continue;
            }
            if (!(expression instanceof Literal) || ((Literal)expression).value() == null ||
                    values.putIfAbsent(key, (Literal)expression) != null) {
                throw new UnsupportedOperationException("Maintenance arguments must be unique and literal");
            }
        }
        var table = values.remove("table");
        if (table == null || !(table.value() instanceof UTF8String)) throw invalidMaintenanceArguments();
        var action = maintenanceAction(operation, values);
        for (String key : List.of("branch", "to", "ref", "snapshot_id", "timestamp", "older_than", "wap_id")) {
            if (values.containsKey(key)) auditReference(key, values.get(key).value());
        }
        var parts = new SparkSqlParser().parseMultipartIdentifier(table.value().toString());
        if (parts.size() < 2) throw new UnsupportedOperationException("Qualify the maintenance table with its namespace");
        return access("table", action, resource(parts, false));
    }

    private void inspectRewriteOptions(Expression expression) {
        if (!(expression instanceof UnresolvedFunction)) throw invalidMaintenanceArguments();
        var map = (UnresolvedFunction)expression;
        if (map.nameParts().size() != 1 || !map.nameParts().head().equalsIgnoreCase("map") ||
                map.arguments().size() % 2 != 0 || map.arguments().size() > 4) throw invalidMaintenanceArguments();
        var fields = map.arguments().iterator();
        var seen = new HashSet<String>();
        while (fields.hasNext()) {
            var key = stringLiteral(fields.next());
            var value = stringLiteral(fields.next());
            if (!seen.add(key) ||
                    !(key.equals("rewrite-all") && Set.of("true", "false").contains(value) ||
                            key.equals("min-input-files") && value.matches("[1-9][0-9]{0,8}"))) {
                throw invalidMaintenanceArguments();
            }
            auditReference(key.replace('-', '_'), value);
        }
    }

    private static String stringLiteral(Expression expression) {
        if (!(expression instanceof Literal) || !(((Literal)expression).value() instanceof UTF8String)) {
            throw invalidMaintenanceArguments();
        }
        return ((Literal)expression).value().toString();
    }

    private static List<String> maintenanceParameters(String name) {
        switch (name) {
            case "rewrite_data_files":
            case "rewrite_position_delete_files":
            case "rewrite_manifests":
            case "expire_snapshots":
            case "remove_orphan_files": return List.of("table");
            case "fast_forward": return List.of("table", "branch", "to");
            case "rollback_to_snapshot":
            case "cherrypick_snapshot":
            case "set_current_snapshot":
            case "ancestors_of": return List.of("table", "snapshot_id");
            case "rollback_to_timestamp": return List.of("table", "timestamp");
            case "publish_changes": return List.of("table", "wap_id");
            default: throw new UnsupportedOperationException("Unsupported Iceberg maintenance procedure");
        }
    }

    private static String maintenanceAction(String name, Map<String, Literal> values) {
        switch (name) {
            case "rewrite_data_files":
            case "rewrite_position_delete_files":
                if (values.isEmpty()) return "rewriteDataFiles";
                break;
            case "rewrite_manifests":
                if (values.isEmpty() || (values.size() == 1 && boundedInteger(values.get("spec_id"), 0))) return "rewriteManifests";
                break;
            case "expire_snapshots":
                if (Set.of("retain_last", "older_than", "stream_results").containsAll(values.keySet()) &&
                        literalBoolean(values.get("stream_results"), true) &&
                        (!values.containsKey("retain_last") || boundedInteger(values.get("retain_last"), 1)) &&
                        (!values.containsKey("older_than") || timestamp(values.get("older_than")))) return "expireSnapshots";
                break;
            case "remove_orphan_files":
                if (values.size() == 2 && literalBoolean(values.get("prefix_listing"), true)) {
                    if (literalBoolean(values.get("dry_run"), true)) return "previewOrphanFiles";
                    if (literalBoolean(values.get("dry_run"), false)) return "deleteOrphanFiles";
                }
                break;
            case "fast_forward":
                if (values.size() == 2 && reference(values.get("branch")) && reference(values.get("to"))) return "snapshotManage";
                break;
            case "rollback_to_snapshot":
            case "cherrypick_snapshot":
                if (values.size() == 1 && snapshotId(values.get("snapshot_id"))) return "snapshotManage";
                break;
            case "set_current_snapshot":
                if (values.size() == 1 && (snapshotId(values.get("snapshot_id")) || reference(values.get("ref")))) return "snapshotManage";
                break;
            case "ancestors_of":
                if (values.isEmpty() || (values.size() == 1 && snapshotId(values.get("snapshot_id")))) return "snapshotRead";
                break;
            case "rollback_to_timestamp":
                if (values.size() == 1 && timestamp(values.get("timestamp"))) return "snapshotManage";
                break;
            case "publish_changes":
                if (values.size() == 1 && reference(values.get("wap_id"))) return "snapshotManage";
                break;
            default:
                break;
        }
        throw invalidMaintenanceArguments();
    }

    private static UnsupportedOperationException invalidMaintenanceArguments() {
        return new UnsupportedOperationException("Unsupported maintenance arguments");
    }

    private static boolean reference(Literal literal) {
        return literal != null && literal.value() instanceof UTF8String && validReference(literal.value().toString());
    }

    private static boolean snapshotId(Literal literal) {
        return literal != null && (literal.value() instanceof Integer || literal.value() instanceof Long) &&
                ((Number)literal.value()).longValue() > 0;
    }

    private static boolean timestamp(Literal literal) {
        return literal != null && literal.dataType().equals(DataTypes.TimestampType);
    }

    private static boolean literalBoolean(Literal literal, boolean value) {
        return literal != null && Boolean.valueOf(value).equals(literal.value());
    }

    private static boolean boundedInteger(Literal literal, int min) {
        return literal != null && literal.value() instanceof Integer && (Integer)literal.value() >= min;
    }

    private static String access(String scope, String action, String resource) {
        return scope + "\t" + action + "\t" + resource;
    }

    private static boolean validReference(String value) {
        return value.matches("[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}");
    }

    private void auditReference(String kind, Object value) {
        // Only typed reference names, IDs and dates. User expressions do not enter audit fields.
        if (value != null && auditReferences.size() < 32) {
            var selector = value.toString();
            if (selector.matches("[A-Za-z0-9_.:+ -]{1,128}")) {
                auditReferences.add("reference\t" + kind + "\t" + selector);
            }
        }
    }

    private void referenceChange(List<String> permissions, Seq<String> parts, String ref,
            String operation, scala.Option<Object> snapshot) {
        if (!validReference(ref)) throw new UnsupportedOperationException("Unsupported branch or tag name");
        this.operation = operation;
        var table = resource(parts, false);
        auditReference("ref", ref);
        if (snapshot.isDefined()) auditReference("snapshotId", snapshot.get());
        permissions.add(access("table", "referenceManage", table));
        if (ref.equalsIgnoreCase("main")) permissions.add(access("table", "snapshotManage", table));
    }

    private static String qualify(String resource) {
        return resource.equals("polaris") ? "catalog" : "namespace";
    }

    private static String parent(String object) {
        return object.substring(0, object.lastIndexOf('.'));
    }

    static String inspect(String sql) throws Exception {
        return new SqlGuard("", Set.of()).inspectStatement(sql);
    }

    private static LogicalPlan parse(String sql) throws Exception {
        var spark = new SparkSqlParser();
        try {
            return spark.parsePlan(sql);
        } catch (Exception nativeError) {
            if (!(nativeError instanceof ParseException)) throw nativeError;
            try {
                return new IcebergSparkSqlExtensionsParser(spark).parsePlan(sql);
            } catch (Exception extensionError) {
                if (!(extensionError instanceof ParseException || extensionError instanceof IllegalStateException)) throw extensionError;
                throw nativeError;
            }
        }
    }

    private String inspectStatement(String sql) throws Exception {
        Set<String> tables = new TreeSet<>();
        List<String> permissions = new ArrayList<>();
        List<String> targets = new ArrayList<>();
        var parsed = parse(sql);
        String result = "command";
        if (parsed instanceof InsertIntoStatement) {
            var insert = (InsertIntoStatement) parsed;
            if (insert.ifPartitionNotExists() || !insert.partitionSpec().isEmpty()) {
                throw new UnsupportedOperationException("Use Iceberg hidden partitioning with INSERT without PARTITION");
            }
            permissions.add(tableWrite(insert.overwrite() ? "overwrite" : "write", insert.table()));
            plan(insert.query(), Set.of(), tables);
        } else if (parsed instanceof UpdateTable) {
            var update = (UpdateTable)parsed;
            permissions.add(tableWrite("update", update.table()));
        } else if (parsed instanceof DeleteFromTable) {
            var delete = (DeleteFromTable)parsed;
            permissions.add(tableWrite("deleteRows", delete.table()));
        } else if (parsed instanceof TruncateTable) {
            permissions.add(access("table", "deleteRows", object(((TruncateTable)parsed).table())));
        } else if (parsed instanceof MergeIntoTable) {
            var merge = (MergeIntoTable)parsed;
            permissions.add(tableWrite("merge", merge.targetTable()));
            plan(merge.sourceTable(), Set.of(), tables);
        } else if (parsed instanceof CreateTable) {
            var create = (CreateTable)parsed;
            tableSpec(create.tableSpec());
            validateSchema(create.tableSchema());
            String name = object(create.name());
            targets.add(access("target", "table", name));
            permissions.add(access("namespace", "create", parent(name)));
        } else if (parsed instanceof CreateTableAsSelect) {
            var create = (CreateTableAsSelect)parsed;
            tableSpec(create.tableSpec());
            if (create.writeOptions().nonEmpty()) throw new UnsupportedOperationException("Unsupported write options");
            String name = object(create.name());
            targets.add(access("target", "table", name));
            permissions.add(access("namespace", "create", parent(name)));
            plan(create.query(), Set.of(), tables);
        } else if (parsed instanceof ReplaceTable || parsed instanceof ReplaceTableAsSelect) {
            throw new UnsupportedOperationException("Table replacement is managed by the platform; use CREATE TABLE or INSERT OVERWRITE");
        } else if (parsed instanceof CreateTableLikeCommand) {
            throw new UnsupportedOperationException("CREATE TABLE LIKE is unavailable for managed Iceberg; use reviewed SHOW CREATE TABLE output");
        } else if (parsed instanceof DropTable) {
            var drop = (DropTable)parsed;
            permissions.add(access("table", drop.purge() ? "purge" : "delete", object(drop.child())));
        } else if (parsed instanceof CreateView) {
            var create = (CreateView)parsed;
            properties(create.properties().keysIterator(), Set.of("comment"));
            String name = object(create.child());
            targets.add(access("target", "view", name));
            if (create.replace()) {
                permissions.add(access("view", "metadataWrite", name));
            }
            permissions.add(access("namespace", "create", parent(name)));
            plan(create.query(), Set.of(), tables);
        } else if (parsed instanceof CreateViewCommand) {
            var create = (CreateViewCommand)parsed;
            if (!create.viewType().toString().equals("LocalTempView") || create.name().nameParts().size() != 1 ||
                    create.properties().nonEmpty()) throw new UnsupportedOperationException("Use session temporary views");
            result = "temp\t" + alias(create.name().table());
            plan(create.plan(), Set.of(), tables);
        } else if (parsed instanceof AlterViewAs) {
            var alter = (AlterViewAs)parsed;
            permissions.add(access("view", "metadataWrite", object(alter.child())));
            plan(alter.query(), Set.of(), tables);
        } else if (parsed instanceof DropView) {
            permissions.add(access("view", "delete", object(((DropView)parsed).child())));
        } else if (parsed instanceof SetViewProperties || parsed instanceof UnsetViewProperties) {
            if (parsed instanceof SetViewProperties) {
                properties(((SetViewProperties)parsed).properties().keysIterator(), Set.of("comment"));
            } else {
                properties(((UnsetViewProperties)parsed).propertyKeys().iterator(), Set.of("comment"));
            }
            permissions.add(access("view", "metadataWrite", object(parsed.children().head())));
        } else if (parsed instanceof RenameTable) {
            var rename = (RenameTable)parsed;
            String source = object(rename.child());
            String target = rename.newName().size() == 1 ? parent(source) + "." + identifier(rename.newName().head()) : resource(rename.newName(), false);
            targets.add(access("target", "renameFrom", source));
            targets.add(access("target", "renameTo", target));
            permissions.add(access(rename.isView() ? "view" : "table", "metadataWrite", source));
            permissions.add(access("namespace", "create", parent(target)));
        } else if (parsed instanceof AddColumns || parsed instanceof DropColumns || parsed instanceof RenameColumn ||
                parsed instanceof AlterColumn || parsed instanceof SetTableProperties || parsed instanceof UnsetTableProperties) {
            var name = object(parsed.children().head());
            var settings = parsed instanceof SetTableProperties ? ((SetTableProperties)parsed).properties() : null;
            if (settings != null && (settings.contains("current-snapshot-id") || settings.contains("cherry-pick-snapshot-id"))) {
                if (settings.size() != 1) throw invalidMaintenanceArguments();
                var key = settings.keysIterator().next();
                var value = settings.apply(key);
                if (!value.matches("[1-9][0-9]{0,18}")) throw invalidMaintenanceArguments();
                auditReference("snapshot_id", value);
                operation = key.equals("current-snapshot-id") ? "set_current_snapshot_property" : "cherrypick_snapshot_property";
                permissions.add(access("table", "snapshotManage", name));
            } else {
                var keys = settings != null ? settings.keysIterator() : parsed instanceof UnsetTableProperties
                        ? ((UnsetTableProperties)parsed).propertyKeys().iterator() : null;
                if (keys != null && tablePropertiesAreRetentionManaged(keys, settings)) {
                    permissions.add(access("table", "expireSnapshots", name));
                }
                if (parsed instanceof AlterColumn && ((AlterColumn)parsed).setDefaultExpression().nonEmpty()) {
                    throw new UnsupportedOperationException("Column defaults are managed by the platform");
                }
                if (parsed instanceof AddColumns) {
                    var columns = ((AddColumns)parsed).columnsToAdd().iterator();
                    while (columns.hasNext()) {
                        if (hasColumnDefault(columns.next())) {
                            throw new UnsupportedOperationException("Column defaults are managed by the platform");
                        }
                    }
                }
                permissions.add(access("table", "metadataWrite", name));
            }
        } else if (parsed instanceof AddPartitionField || parsed instanceof DropPartitionField ||
                parsed instanceof ReplacePartitionField || parsed instanceof SetWriteDistributionAndOrdering ||
                parsed instanceof SetIdentifierFields || parsed instanceof DropIdentifierFields) {
            permissions.add(access("table", "metadataWrite", resource(extensionTable(parsed), false)));
        } else if (parsed instanceof CreateOrReplaceBranch) {
            var change = (CreateOrReplaceBranch)parsed;
            referenceChange(permissions, change.table(), change.branch(),
                    referenceOperation(change.create(), change.replace(), "branch"), change.branchOptions().snapshotId());
            auditOption("retentionMs", change.branchOptions().snapshotRefRetain());
            auditOption("minSnapshots", change.branchOptions().numSnapshots());
            auditOption("snapshotRetentionMs", change.branchOptions().snapshotRetain());
        } else if (parsed instanceof CreateOrReplaceTag) {
            var change = (CreateOrReplaceTag)parsed;
            referenceChange(permissions, change.table(), change.tag(),
                    referenceOperation(change.create(), change.replace(), "tag"), change.tagOptions().snapshotId());
            auditOption("retentionMs", change.tagOptions().snapshotRefRetain());
        } else if (parsed instanceof DropBranch) {
            var change = (DropBranch)parsed;
            referenceChange(permissions, change.table(), change.branch(), "drop_branch", scala.Option.empty());
        } else if (parsed instanceof DropTag) {
            var change = (DropTag)parsed;
            referenceChange(permissions, change.table(), change.tag(), "drop_tag", scala.Option.empty());
        } else if (parsed instanceof CreateNamespace) {
            var create = (CreateNamespace)parsed;
            properties(create.properties().keysIterator(), Set.of("comment"));
            String name = namespace(create.name());
            targets.add(access("target", "namespace", name));
            String resource = parent(name);
            permissions.add(access(qualify(resource), "create", resource));
        } else if (parsed instanceof DropNamespace) {
            var drop = (DropNamespace)parsed;
            if (drop.cascade()) throw new UnsupportedOperationException("Drop child objects first");
            permissions.add(access("namespace", "delete", namespace(drop.namespace())));
        } else if (parsed instanceof SetNamespaceProperties) {
            var set = (SetNamespaceProperties)parsed;
            properties(set.properties().keysIterator(), Set.of("comment"));
            permissions.add(access("namespace", "metadataWrite", namespace(set.namespace())));
        } else if (parsed instanceof SetCatalogAndNamespace) {
            String selected = namespace(parsed.children().head());
            permissions.add(access(qualify(selected), "metadataRead", selected));
            result = "use\t" + selected;
        } else if (parsed instanceof ShowCreateTable || parsed instanceof DescribeRelation ||
                parsed instanceof ShowColumns || parsed instanceof ShowTableProperties) {
            if (parsed instanceof ShowCreateTable && ((ShowCreateTable)parsed).asSerde()) {
                throw new UnsupportedOperationException("Hive SerDe DDL is unavailable for Iceberg");
            }
            permissions.add(access("table", "metadataRead", object(parsed.children().head())));
            result = "metadata";
        } else if (parsed instanceof ShowTables || parsed instanceof ShowNamespaces ||
                parsed instanceof ShowViews || parsed instanceof DescribeNamespace) {
            String resource = namespace(parsed.children().head());
            permissions.add(access(qualify(resource), "metadataRead", resource));
            result = "metadata";
        } else if (parsed instanceof ExplainCommand) {
            plan(((ExplainCommand)parsed).logicalPlan(), Set.of(), tables);
            result = "metadata";
        } else if (parsed instanceof DescribeQueryCommand) {
            plan(((DescribeQueryCommand)parsed).plan(), Set.of(), tables);
            result = "metadata";
        } else if (parsed instanceof ShowFunctions) {
            var functions = (ShowFunctions)parsed;
            if (functions.userScope() || !functions.systemScope() ||
                    !namespace(functions.namespace()).equals("polaris")) {
                throw new UnsupportedOperationException("Use SHOW SYSTEM FUNCTIONS without a namespace");
            }
            result = "metadata";
        } else if (parsed instanceof AnalyzeTable || parsed instanceof AnalyzeColumn || parsed instanceof AnalyzeTables) {
            throw new UnsupportedOperationException("ANALYZE TABLE is unavailable for managed Iceberg V2 tables");
        } else if (parsed instanceof CallStatement) {
            permissions.add(maintenance((CallStatement)parsed));
            result = "metadata";
        } else {
            plan(parsed, Set.of(), tables);
            result = "query";
        }
        if (parsed instanceof UpdateTable || parsed instanceof DeleteFromTable || parsed instanceof MergeIntoTable) {
            var expressions = parsed.expressions().iterator();
            while (expressions.hasNext()) expression(expressions.next(), Set.of(), tables);
        }
        if (tables.size() > 32) throw new UnsupportedOperationException("Too many input tables");
        for (String name : tables) {
            var segments = name.split("\\.");
            String scope = metadataTable(segments) ? "metadataTable" : branchTable(segments) ? "branchTable" : name.contains(".") ? "table" : "temp";
            if (scope.equals("branchTable")) auditReference("branch", segments[segments.length - 1].substring(7));
            permissions.add(access(scope, "read", name));
        }
        for (String table : historyTables) permissions.add(access("table", "snapshotRead", table));
        if (operation.isEmpty() && !historyTables.isEmpty()) operation = "time_travel";
        String statement = result.equals("query") ? "Select" : parsed.getClass().getSimpleName();
        permissions.add("statement\t" + statement);
        if (!operation.isEmpty()) permissions.add("operation\t" + operation);
        permissions.addAll(auditReferences);
        permissions.addAll(targets);
        return result + "\n" + String.join("\n", permissions);
    }

    private static String referenceOperation(boolean create, boolean replace, String refType) {
        return (replace ? create ? "create_or_replace_" : "replace_" : "create_") + refType;
    }

    private void auditOption(String kind, scala.Option<Object> value) {
        if (value.isDefined()) auditReference(kind, value.get());
    }

    public static void main(String[] args) throws Exception {
        if (args.length == 2 && args[0].equals("--check")) {
            System.out.println(inspect(args[1]));
            return;
        }
        HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 18083), 16);
        server.setExecutor(Executors.newFixedThreadPool(2));
        server.createContext("/parse", exchange -> {
            int status = 400;
            byte[] response = "Unsupported or invalid Spark SQL.".getBytes(StandardCharsets.UTF_8);
            try {
                if (exchange.getRequestMethod().equals("POST")) {
                    byte[] body = exchange.getRequestBody().readNBytes(32769);
                    if (body.length <= 32768) {
                        String namespace = exchange.getRequestHeaders().getFirst("X-Sql-Namespace");
                        String views = exchange.getRequestHeaders().getFirst("X-Sql-Temp-Views");
                        Set<String> tempViews = views == null || views.isEmpty() ? Set.of() : Set.of(views.split(","));
                        response = new SqlGuard(namespace == null ? "" : namespace, tempViews)
                                .inspectStatement(new String(body, StandardCharsets.UTF_8))
                                .getBytes(StandardCharsets.UTF_8);
                        status = 200;
                    }
                }
            } catch (UnsupportedOperationException failure) {
                status = 422;
                response = failure.getMessage().getBytes(StandardCharsets.UTF_8);
            } catch (ParseException ignored) {
                // Catalyst parse errors can contain the input statement.
            } catch (StackOverflowError ignored) {
                status = 422;
                response = "SQL is too deeply nested".getBytes(StandardCharsets.UTF_8);
            } catch (Exception | LinkageError ignored) {
                status = 500;
                response = "Spark SQL parser failed".getBytes(StandardCharsets.UTF_8);
            }
            try {
                exchange.sendResponseHeaders(status, response.length);
                exchange.getResponseBody().write(response);
            } finally {
                exchange.close();
            }
        });
        server.start();
    }
}
