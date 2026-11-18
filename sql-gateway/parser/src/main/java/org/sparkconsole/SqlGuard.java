package org.sparkconsole;

import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.HashSet;
import java.util.Set;
import java.util.TreeSet;
import java.util.concurrent.Executors;
import org.apache.spark.sql.catalyst.analysis.UnresolvedFunction;
import org.apache.spark.sql.catalyst.analysis.UnresolvedRelation;
import org.apache.spark.sql.catalyst.expressions.Expression;
import org.apache.spark.sql.catalyst.expressions.SubqueryExpression;
import org.apache.spark.sql.catalyst.parser.CatalystSqlParser$;
import org.apache.spark.sql.catalyst.plans.logical.LogicalPlan;
import org.apache.spark.sql.catalyst.plans.logical.UnresolvedWith;
import scala.collection.Iterator;

public final class SqlGuard {
    private static final Set<String> PLANS = Set.of(
            "Project", "Filter", "Aggregate", "Sort", "GlobalLimit", "LocalLimit",
            "Distinct", "Join", "Union", "Intersect", "Except", "SubqueryAlias",
            "UnresolvedRelation", "UnresolvedWith", "OneRowRelation", "Window",
            "RepartitionByExpression", "Repartition", "Limit", "Offset");
    private static final Set<String> FUNCTIONS = Set.of(
            "abs", "array", "avg", "cast", "ceil", "ceiling", "coalesce", "concat",
            "concat_ws", "count", "count_distinct", "current_date", "current_timestamp",
            "date_add", "date_format", "date_sub", "date_trunc", "datediff", "day",
            "dense_rank", "explode", "floor", "format_number", "from_unixtime",
            "get_json_object", "greatest", "if", "isnull", "lag", "lead", "least",
            "length", "lower", "lpad", "ltrim", "max", "min", "month", "nvl", "percentile_approx",
            "rank", "regexp_extract", "regexp_replace", "round", "row_number", "rpad",
            "rtrim", "size", "split", "stddev", "substr", "substring", "sum", "to_date",
            "to_json", "to_timestamp", "trim", "trunc", "unix_timestamp", "upper", "year");
    private static final Set<String> ICEBERG_METADATA_TABLES = Set.of(
            "entries", "files", "data_files", "delete_files", "history", "snapshots",
            "manifests", "partitions", "refs", "all_data_files", "all_delete_files",
            "all_files", "all_manifests", "all_entries", "position_deletes", "metadata_log_entries");

    private static String identifier(String value) {
        if (!value.matches("[a-z_][a-z0-9_]{0,127}")) {
            throw new IllegalArgumentException("Use lowercase table identifiers.");
        }
        return value;
    }

    private static void expression(Expression expression, Set<String> ctes, Set<String> tables) {
        if (expression instanceof UnresolvedFunction) {
            UnresolvedFunction function = (UnresolvedFunction) expression;
            if (function.nameParts().size() != 1 ||
                    !FUNCTIONS.contains(function.nameParts().head().toLowerCase(java.util.Locale.ROOT))) {
                throw new IllegalArgumentException("Function is outside the read-only allowlist.");
            }
        }
        if (expression instanceof SubqueryExpression) {
            plan((LogicalPlan) ((SubqueryExpression) expression).plan(), ctes, tables);
        }
        Iterator<Expression> children = expression.children().iterator();
        while (children.hasNext()) expression(children.next(), ctes, tables);
    }

    private static void plan(LogicalPlan plan, Set<String> ctes, Set<String> tables) {
        if (!PLANS.contains(plan.getClass().getSimpleName())) {
            throw new IllegalArgumentException("Only supported read-only SELECT statements are accepted.");
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
            if (parts.size() < 3 || parts.size() > 8 || !parts.head().equals("polaris")) {
                throw new IllegalArgumentException("Use a fully qualified polaris.namespace.table name.");
            }
            if (parts.size() > 3 && ICEBERG_METADATA_TABLES.contains(parts.last())) {
                throw new IllegalArgumentException("Iceberg metadata tables are outside the query allowlist.");
            }
            var names = parts.iterator();
            StringBuilder name = new StringBuilder();
            while (names.hasNext()) {
                if (name.length() > 0) name.append('.');
                name.append(identifier(names.next()));
            }
            tables.add(name.toString());
        }
        Iterator<Expression> expressions = plan.expressions().iterator();
        while (expressions.hasNext()) expression(expressions.next(), ctes, tables);
        Iterator<LogicalPlan> children = plan.children().iterator();
        while (children.hasNext()) plan(children.next(), ctes, tables);
    }

    static Set<String> tables(String sql) throws Exception {
        Set<String> tables = new TreeSet<>();
        plan(CatalystSqlParser$.MODULE$.parsePlan(sql), Set.of(), tables);
        if (tables.size() > 32) throw new IllegalArgumentException("Too many input tables.");
        return tables;
    }

    public static void main(String[] args) throws Exception {
        if (args.length == 2 && args[0].equals("--check")) {
            System.out.println(String.join("\n", tables(args[1])));
            return;
        }
        HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 18083), 16);
        server.setExecutor(Executors.newFixedThreadPool(2));
        server.createContext("/parse", exchange -> {
            int status = 400;
            byte[] response = "Unsupported or invalid read-only Spark SQL.".getBytes(StandardCharsets.UTF_8);
            try {
                if (exchange.getRequestMethod().equals("POST")) {
                    byte[] body = exchange.getRequestBody().readNBytes(32769);
                    if (body.length <= 32768) {
                        response = String.join("\n", tables(new String(body, StandardCharsets.UTF_8)))
                                .getBytes(StandardCharsets.UTF_8);
                        status = 200;
                    }
                }
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
