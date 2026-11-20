package org.sparkconsole;

import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.HashSet;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Locale;
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
import org.apache.spark.sql.util.CaseInsensitiveStringMap;
import scala.collection.Seq;
import scala.collection.Iterator;

public final class SqlGuard {
    private final String namespace;
    private final Set<String> tempViews;

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
            throw new UnsupportedOperationException("Historical snapshot reads are managed by the platform");
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
                    ? alias(parts.head()) : resource(parts, false, true));
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
        return resource(parts, isNamespace, false);
    }

    private String resource(Seq<String> parts, boolean isNamespace, boolean allowMetadata) {
        if (parts.size() < (isNamespace ? 0 : 1) || parts.size() > (isNamespace ? 7 : allowMetadata ? 9 : 8)) {
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
            if (segments.length > 3 && segments[segments.length - 1].matches("(branch|tag|snapshot_id|at_timestamp)_.+")) {
                throw new UnsupportedOperationException("Historical snapshot reads and branch writes are managed by the platform");
            }
            boolean metadata = metadataTable(segments);
            if (segments.length < 3 || (metadata && !allowMetadata) ||
                    segments.length > (metadata ? 9 : 8)) {
                throw new UnsupportedOperationException("Unsupported Iceberg table identifier");
            }
        }
        return name.toString();
    }

    private static boolean metadataTable(String[] segments) {
        return segments.length > 3 && ICEBERG_METADATA_TABLES.contains(segments[segments.length - 1]);
    }

    private String object(LogicalPlan plan) {
        if (plan instanceof SubqueryAlias) return object(((SubqueryAlias)plan).child());
        if (plan instanceof UnresolvedRelation) {
            var relation = (UnresolvedRelation)plan;
            if (!writeOptionsOnly(relation.options())) throw new UnsupportedOperationException("Unsupported relation options");
            return resource(relation.multipartIdentifier(), false);
        }
        if (plan instanceof UnresolvedIdentifier) return resource(((UnresolvedIdentifier)plan).nameParts(), false);
        if (plan instanceof UnresolvedTableOrView) return resource(((UnresolvedTableOrView)plan).multipartIdentifier(), false);
        if (plan instanceof UnresolvedTable) return resource(((UnresolvedTable)plan).multipartIdentifier(), false);
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
        var action = maintenanceAction(name.apply(2));
        var values = new HashMap<String, Object>();
        var args = call.args().iterator();
        while (args.hasNext()) {
            var arg = args.next();
            if (!(arg instanceof NamedArgument) && (call.args().size() != 1 || !(arg instanceof PositionalArgument))) {
                throw new UnsupportedOperationException("Use named maintenance arguments");
            }
            var key = arg instanceof NamedArgument ? ((NamedArgument)arg).name().toLowerCase(Locale.ROOT) : "table";
            var expression = arg instanceof NamedArgument ? ((NamedArgument)arg).expr() : ((PositionalArgument)arg).expr();
            if (!(expression instanceof Literal) || ((Literal)expression).value() == null ||
                    values.putIfAbsent(key, ((Literal)expression).value()) != null) {
                throw new UnsupportedOperationException("Maintenance arguments must be unique and literal");
            }
        }
        Object table = values.remove("table");
        if (!(table instanceof org.apache.spark.unsafe.types.UTF8String) ||
                !maintenanceOptions(action, values)) {
            throw new UnsupportedOperationException("Unsupported maintenance arguments");
        }
        var parts = new SparkSqlParser().parseMultipartIdentifier(table.toString());
        if (parts.size() < 2) throw new UnsupportedOperationException("Qualify the maintenance table with its namespace");
        return access("table", action, resource(parts, false));
    }

    private static String maintenanceAction(String name) {
        switch (name.toLowerCase(Locale.ROOT)) {
            case "rewrite_data_files": return "rewriteDataFiles";
            case "rewrite_manifests": return "rewriteManifests";
            case "expire_snapshots": return "expireSnapshots";
            case "remove_orphan_files": return "previewOrphanFiles";
            default: throw new UnsupportedOperationException("Unsupported Iceberg maintenance procedure");
        }
    }

    private static boolean maintenanceOptions(String action, java.util.Map<String, Object> values) {
        switch (action) {
            case "rewriteDataFiles":
                return values.isEmpty();
            case "rewriteManifests":
                Object id = values.getOrDefault("spec_id", 0);
                return Set.of("spec_id").containsAll(values.keySet()) && id instanceof Integer && (Integer)id >= 0;
            case "expireSnapshots":
                Object retain = values.getOrDefault("retain_last", 1);
                return Set.of("retain_last", "stream_results").containsAll(values.keySet()) &&
                        Boolean.TRUE.equals(values.get("stream_results")) && retain instanceof Integer && (Integer)retain >= 1;
            case "previewOrphanFiles":
                return values.size() == 2 && Boolean.TRUE.equals(values.get("dry_run")) &&
                        Boolean.TRUE.equals(values.get("prefix_listing"));
            default:
                return false;
        }
    }

    private static String access(String scope, String action, String resource) {
        return scope + "\t" + action + "\t" + resource;
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
            permissions.add(access("table", insert.overwrite() ? "overwrite" : "write", object(insert.table())));
            plan(insert.query(), Set.of(), tables);
        } else if (parsed instanceof UpdateTable) {
            var update = (UpdateTable)parsed;
            permissions.add(access("table", "update", object(update.table())));
        } else if (parsed instanceof DeleteFromTable) {
            var delete = (DeleteFromTable)parsed;
            permissions.add(access("table", "deleteRows", object(delete.table())));
        } else if (parsed instanceof TruncateTable) {
            permissions.add(access("table", "deleteRows", object(((TruncateTable)parsed).table())));
        } else if (parsed instanceof MergeIntoTable) {
            var merge = (MergeIntoTable)parsed;
            permissions.add(access("table", "merge", object(merge.targetTable())));
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
        } else if (parsed instanceof AddPartitionField || parsed instanceof DropPartitionField ||
                parsed instanceof ReplacePartitionField || parsed instanceof SetWriteDistributionAndOrdering ||
                parsed instanceof SetIdentifierFields || parsed instanceof DropIdentifierFields) {
            permissions.add(access("table", "metadataWrite", resource(extensionTable(parsed), false)));
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
            String scope = metadataTable(name.split("\\.")) ? "metadataTable" : name.contains(".") ? "table" : "temp";
            permissions.add(access(scope, "read", name));
        }
        String statement = result.equals("query") ? "Select" : parsed.getClass().getSimpleName();
        permissions.add("statement\t" + statement);
        permissions.addAll(targets);
        return result + "\n" + String.join("\n", permissions);
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
