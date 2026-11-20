package org.sparkconsole;

import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.HashSet;
import java.util.ArrayList;
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
import org.apache.spark.sql.catalyst.analysis.FunctionRegistry;
import org.apache.spark.sql.catalyst.FunctionIdentifier;
import org.apache.spark.sql.catalyst.expressions.Expression;
import org.apache.spark.sql.catalyst.expressions.SubqueryExpression;
import org.apache.spark.sql.catalyst.plans.logical.*;
import org.apache.spark.sql.execution.SparkSqlParser;
import org.apache.spark.sql.execution.command.ExplainCommand;
import org.apache.spark.sql.execution.command.CreateViewCommand;
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
    private static final Set<String> PLANS = Set.of(
            "Project", "Filter", "Aggregate", "Sort", "GlobalLimit", "LocalLimit",
            "Distinct", "Join", "Union", "Intersect", "Except", "SubqueryAlias",
            "UnresolvedRelation", "UnresolvedWith", "OneRowRelation", "Window",
            "RepartitionByExpression", "Repartition", "Limit", "Offset", "UnresolvedInlineTable",
            "Generate", "UnresolvedTableValuedFunction", "Sample", "UnresolvedHint", "Pivot",
            "Unpivot", "WithWindowDefinition", "UnresolvedSubqueryColumnAliases", "Tail");
    private static final Set<String> UNSAFE_FUNCTIONS = Set.of("reflect", "reflect2", "java_method",
            "raise_error", "assert_true", "input_file_name", "input_file_block_start", "input_file_block_length");
    private static final Set<String> TABLE_FUNCTIONS = Set.of("range", "explode", "explode_outer",
            "inline", "inline_outer", "posexplode", "posexplode_outer", "stack", "json_tuple");
    private static final Set<String> TABLE_PROPERTIES = Set.of("format-version", "comment",
            "write.format.default", "write.parquet.compression-codec", "write.orc.compression-codec",
            "write.avro.compression-codec", "write.distribution-mode", "write.target-file-size-bytes",
            "write.delete.mode", "write.update.mode", "write.merge.mode", "write.delete.isolation-level",
            "write.update.isolation-level", "write.merge.isolation-level");
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
        if (!PLANS.contains(plan.getClass().getSimpleName())) {
            throw new UnsupportedOperationException("Unsupported query operation");
        }
        if (plan instanceof UnresolvedWith) {
            UnresolvedWith with = (UnresolvedWith) plan;
            Set<String> aliases = new HashSet<>(ctes);
            var items = with.cteRelations().iterator();
            while (items.hasNext()) {
                var item = items.next();
                plan(item._2(), aliases, tables);
                aliases.add(identifier(item._1()));
            }
            plan(with.child(), aliases, tables);
            return;
        }
        if (plan instanceof UnresolvedRelation) {
            UnresolvedRelation table = (UnresolvedRelation) plan;
            var parts = table.multipartIdentifier();
            if (parts.size() == 1 && ctes.contains(parts.head())) return;
            if (!table.options().isEmpty()) {
                throw new UnsupportedOperationException("Unsupported relation options");
            }
            tables.add(parts.size() == 1 && tempViews.contains(parts.head())
                    ? parts.head() : resource(parts, false, true));
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
        properties(specification.properties().keysIterator(), TABLE_PROPERTIES);
    }

    private static void validateSchema(StructType schema) {
        for (var field : schema.fields()) {
            if (field.metadata().contains("CURRENT_DEFAULT") || field.metadata().contains("EXISTS_DEFAULT")) {
                throw new UnsupportedOperationException("Column defaults are managed by the platform");
            }
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

    private String inspectStatement(String sql) throws Exception {
        Set<String> tables = new TreeSet<>();
        List<String> permissions = new ArrayList<>();
        List<String> targets = new ArrayList<>();
        var parsed = new SparkSqlParser().parsePlan(sql);
        String result = "command";
        if (parsed instanceof InsertIntoStatement) {
            var insert = (InsertIntoStatement) parsed;
            if (insert.ifPartitionNotExists() || !insert.partitionSpec().isEmpty()) {
                throw new UnsupportedOperationException("Unsupported partition clause");
            }
            permissions.add(access("table", insert.overwrite() ? "overwrite" : "write", object(insert.table())));
            plan(insert.query(), Set.of(), tables);
        } else if (parsed instanceof UpdateTable) {
            var update = (UpdateTable)parsed;
            permissions.add(access("table", "update", object(update.table())));
        } else if (parsed instanceof DeleteFromTable) {
            var delete = (DeleteFromTable)parsed;
            permissions.add(access("table", "deleteRows", object(delete.table())));
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
            result = "temp\t" + identifier(create.name().table());
            plan(create.plan(), Set.of(), tables);
        } else if (parsed instanceof AlterViewAs) {
            var alter = (AlterViewAs)parsed;
            permissions.add(access("view", "metadataWrite", object(alter.child())));
            plan(alter.query(), Set.of(), tables);
        } else if (parsed instanceof DropView) {
            permissions.add(access("view", "delete", object(((DropView)parsed).child())));
        } else if (parsed instanceof RenameTable) {
            var rename = (RenameTable)parsed;
            String source = object(rename.child());
            String target = rename.newName().size() == 1 ? parent(source) + "." + identifier(rename.newName().head()) : resource(rename.newName(), false);
            targets.add(access("target", "renameFrom", source));
            targets.add(access("target", "renameTo", target));
            permissions.add(access(rename.isView() ? "view" : "table", "metadataWrite", source));
            permissions.add(access("namespace", "create", parent(target)));
        } else if (Set.of("AddColumns", "DropColumns", "RenameColumn", "AlterColumn",
                "SetTableProperties", "UnsetTableProperties").contains(parsed.getClass().getSimpleName())) {
            if (parsed instanceof SetTableProperties) properties(((SetTableProperties)parsed).properties().keysIterator(), TABLE_PROPERTIES);
            if (parsed instanceof UnsetTableProperties) properties(((UnsetTableProperties)parsed).propertyKeys().iterator(), TABLE_PROPERTIES);
            if (parsed instanceof AlterColumn && ((AlterColumn)parsed).setDefaultExpression().nonEmpty()) {
                throw new UnsupportedOperationException("Column defaults are managed by the platform");
            }
            if (parsed instanceof AddColumns) {
                var columns = ((AddColumns)parsed).columnsToAdd().iterator();
                while (columns.hasNext()) {
                    // QualifiedColType's seventh case-class field is the default SQL expression.
                    if (((scala.Option<?>)columns.next().productElement(6)).nonEmpty()) {
                        throw new UnsupportedOperationException("Column defaults are managed by the platform");
                    }
                }
            }
            permissions.add(access("table", "metadataWrite", object(parsed.children().head())));
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
            } catch (StackOverflowError | Exception ignored) {
                // Do not return parser errors containing SQL or sensitive literals.
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
