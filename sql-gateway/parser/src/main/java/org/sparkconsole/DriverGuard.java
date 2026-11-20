package org.sparkconsole;

import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Proxy;
import java.util.StringJoiner;
import java.util.Set;
import org.apache.iceberg.BaseMetadataTable;
import org.apache.iceberg.BaseTable;
import org.apache.iceberg.spark.SparkCachedTableCatalog;
import org.apache.iceberg.spark.SparkTableCache;
import org.apache.iceberg.spark.source.SparkTable;
import org.apache.spark.sql.SparkSession;
import org.apache.spark.sql.SparkSessionExtensions;
import org.apache.spark.sql.SparkSessionExtensionsProvider;
import org.apache.spark.sql.catalyst.analysis.ResolvedIdentifier;
import org.apache.spark.sql.catalyst.analysis.ResolvedNamespace;
import org.apache.spark.sql.catalyst.analysis.ResolvedTable;
import org.apache.spark.sql.catalyst.analysis.UnresolvedTable;
import org.apache.spark.sql.catalyst.parser.ParserInterface;
import org.apache.spark.sql.catalyst.plans.logical.InsertIntoStatement;
import org.apache.spark.sql.catalyst.plans.logical.LogicalPlan;
import org.apache.spark.sql.catalyst.plans.logical.MergeIntoTable;
import org.apache.spark.sql.catalyst.plans.logical.RenameTable;
import org.apache.spark.sql.catalyst.plans.logical.SetTableProperties;
import org.apache.spark.sql.catalyst.plans.logical.TruncateTable;
import org.apache.spark.sql.catalyst.plans.logical.UnsetTableProperties;
import org.apache.spark.sql.catalyst.plans.logical.V2WriteCommand;
import org.apache.spark.sql.connector.catalog.CatalogPlugin;
import org.apache.spark.sql.connector.catalog.Identifier;
import org.apache.spark.sql.execution.datasources.v2.DataSourceV2Relation;
import org.apache.spark.sql.execution.datasources.LogicalRelation;
import org.apache.spark.sql.catalyst.catalog.HiveTableRelation;
import scala.runtime.AbstractFunction1;
import scala.runtime.AbstractFunction2;
import scala.runtime.BoxedUnit;

/** Installed only in the console's dedicated Spark SQL engine. */
public final class DriverGuard implements SparkSessionExtensionsProvider {
    private static final String READY = "SELECT spark_console_guard_version()";
    private static final String INSTALL = "SET " + DriverPolicy.TICKET + "=";
    private static final Set<String> COMPACTION_READ_OPTIONS = Set.of("path", "scan-task-set-id", "split-size", "file-open-cost");
    private static final Set<String> COMPACTION_WRITE_OPTIONS = Set.of("path", "rewritten-file-scan-task-set-id",
            "target-file-size-bytes", "distribution-mode", "output-spec-id");
    private static final Set<String> SETTINGS = Set.of(
            "spark.sql.catalog.polaris",
            "spark.sql.catalog.polaris.catalog-impl", "spark.sql.catalog.polaris.console-principal-id",
            "spark.sql.catalog.polaris.scope", "spark.sql.catalog.polaris.console-table-bindings",
            "spark.sql.catalog.polaris.uri", "spark.sql.catalog.polaris.warehouse",
            "spark.sql.catalog.polaris.header.Polaris-Realm", "spark.sql.catalog.polaris.cache-enabled",
            "spark.sql.defaultCatalog", "spark.sql.variable.substitute", "spark.sql.sparkconsole.session");

    @Override
    public BoxedUnit apply(SparkSessionExtensions extensions) {
        extensions.injectParser(new AbstractFunction2<SparkSession, ParserInterface, ParserInterface>() {
            @Override
            public ParserInterface apply(SparkSession session, ParserInterface delegate) {
                return (ParserInterface) Proxy.newProxyInstance(ParserInterface.class.getClassLoader(),
                        new Class<?>[]{ParserInterface.class}, (proxy, method, args) -> {
                            if (method.getName().equals("parsePlan")) {
                                String statement = (String)args[0];
                                if (statement.equals(READY)) return delegate.parsePlan("SELECT 'spark-console-driver-v3' AS version");
                                if (statement.startsWith(INSTALL)) {
                                    DriverPolicy.install(session, statement.substring(INSTALL.length()));
                                    return delegate.parsePlan("SELECT 1");
                                }
                                authorize(session, statement);
                                var parsed = delegate.parsePlan(statement);
                                if ((parsed instanceof MergeIntoTable || parsed instanceof InsertIntoStatement) &&
                                        !"false".equalsIgnoreCase(session.conf().get("spark.sql.iceberg.merge-schema", "false"))) {
                                    throw new IllegalArgumentException("Implicit Iceberg schema evolution is disabled for console SQL.");
                                }
                                if ((parsed instanceof SetTableProperties && ((SetTableProperties)parsed).properties().contains("format-version")) ||
                                        (parsed instanceof UnsetTableProperties &&
                                                ((UnsetTableProperties)parsed).propertyKeys().contains("format-version"))) {
                                    throw new IllegalArgumentException("Iceberg table format changes are managed by the platform.");
                                }
                                if (parsed instanceof TruncateTable) {
                                    var table = ((TruncateTable)parsed).table();
                                    if (!(table instanceof UnresolvedTable)) throw DriverPolicy.denied();
                                    var parts = ((UnresolvedTable)table).multipartIdentifier().iterator();
                                    var name = new StringJoiner(".");
                                    while (parts.hasNext()) {
                                        var part = parts.next();
                                        if (!part.matches("[a-z_][a-z0-9_-]{0,127}")) throw DriverPolicy.denied();
                                        name.add("`" + part + "`");
                                    }
                                    return delegate.parsePlan("DELETE FROM " + name);
                                }
                                if (parsed instanceof RenameTable) {
                                    var rename = (RenameTable)parsed;
                                    // Spark treats every destination segment before the table as a namespace.
                                    if (rename.newName().size() > 2 && rename.newName().head().equals("polaris")) {
                                        return rename.copy(rename.child(), (scala.collection.Seq<String>)rename.newName().drop(1), rename.isView());
                                    }
                                }
                                return parsed;
                            }
                            try {
                                return method.invoke(delegate, args);
                            } catch (InvocationTargetException failure) {
                                throw failure.getCause();
                            }
                        });
            }
        });
        extensions.injectCheckRule(new AbstractFunction1<SparkSession, scala.Function1<LogicalPlan, BoxedUnit>>() {
            @Override
            public scala.Function1<LogicalPlan, BoxedUnit> apply(SparkSession session) {
                return new AbstractFunction1<LogicalPlan, BoxedUnit>() {
                    @Override
                    public BoxedUnit apply(LogicalPlan plan) {
                        check(plan, session);
                        return BoxedUnit.UNIT;
                    }
                };
            }
        });
        return BoxedUnit.UNIT;
    }

    private static void authorize(SparkSession session, String sql) {
        if (sql.equals("SELECT 1") || SETTINGS.stream().anyMatch(key -> sql.equals("SET `" + key + "`"))) return;
        if (!DriverPolicy.forSession(session).accepts(sql)) throw DriverPolicy.denied();
    }

    private static void requireCatalog(CatalogPlugin catalog) {
        if (!catalog.name().equals("polaris") || !(catalog instanceof ManagedSparkCatalog)) {
            throw DriverPolicy.denied();
        }
    }

    private static void requireBinding(CatalogPlugin catalog, Identifier identifier, SparkSession session) {
        requireCatalog(catalog);
        String name = String.join(".", identifier.namespace()) + "." + identifier.name();
        if (DriverPolicy.forSession(session).binding(name) == null) throw DriverPolicy.denied();
    }

    private static void check(LogicalPlan plan, SparkSession session) {
        if (plan instanceof DataSourceV2Relation) {
            var relation = (DataSourceV2Relation) plan;
            if (relation.catalog().isDefined() && relation.identifier().isDefined() &&
                    SqlGuard.writeOptionsOnly(relation.options())) {
                requireBinding(relation.catalog().get(), relation.identifier().get(), session);
            } else if (!maintenanceRelation(relation, session)) {
                throw DriverPolicy.denied();
            }
        } else if (plan instanceof ResolvedTable) {
            var table = (ResolvedTable) plan;
            requireBinding(table.catalog(), table.identifier(), session);
        } else if (plan instanceof ResolvedIdentifier) {
            requireCatalog(((ResolvedIdentifier)plan).catalog());
        } else if (plan instanceof ResolvedNamespace) {
            requireCatalog(((ResolvedNamespace)plan).catalog());
        } else if (plan instanceof LogicalRelation || plan instanceof HiveTableRelation) {
            throw DriverPolicy.denied();
        }
        if (plan instanceof V2WriteCommand) check((LogicalPlan)((V2WriteCommand)plan).table(), session);
        var nested = plan.subqueries().iterator();
        while (nested.hasNext()) check(nested.next(), session);
        var children = plan.children().iterator();
        while (children.hasNext()) check(children.next(), session);
    }

    private static boolean maintenanceRelation(DataSourceV2Relation relation, SparkSession session) {
        if (!(relation.table() instanceof SparkTable)) return false;
        String expected;
        if (relation.catalog().isEmpty() && relation.identifier().isEmpty() && relation.options().isEmpty()) {
            // Maintenance metadata scans have no catalog identifier.
            var policy = DriverPolicy.forSession(session);
            expected = policy.binding("@maintenance");
            if (expected == null) expected = policy.binding("@compaction");
        } else if (compactionRelation(relation)) {
            expected = DriverPolicy.compactionBinding(session);
        } else {
            return false;
        }
        var table = ((SparkTable)relation.table()).table();
        if (table instanceof BaseMetadataTable) table = ((BaseMetadataTable)table).table();
        return expected != null && table instanceof BaseTable &&
                expected.equals(((BaseTable)table).operations().current().uuid());
    }

    private static boolean compactionRelation(DataSourceV2Relation relation) {
        if (relation.catalog().isEmpty() || !(relation.catalog().get() instanceof SparkCachedTableCatalog)) return false;
        var options = relation.options();
        String task = options.get("scan-task-set-id");
        if (task != null && !COMPACTION_READ_OPTIONS.containsAll(options.keySet())) return false;
        if (task == null) {
            task = options.get("rewritten-file-scan-task-set-id");
            if (!COMPACTION_WRITE_OPTIONS.containsAll(options.keySet())) return false;
        }
        return task != null && task.matches("[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}") &&
                task.equals(options.get("path")) &&
                SparkTableCache.get().get(task) == ((SparkTable)relation.table()).table();
    }
}
