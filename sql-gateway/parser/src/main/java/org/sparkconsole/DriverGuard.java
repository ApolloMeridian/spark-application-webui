package org.sparkconsole;

import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Proxy;
import java.util.Set;
import org.apache.spark.sql.SparkSession;
import org.apache.spark.sql.SparkSessionExtensions;
import org.apache.spark.sql.SparkSessionExtensionsProvider;
import org.apache.spark.sql.catalyst.analysis.ResolvedIdentifier;
import org.apache.spark.sql.catalyst.analysis.ResolvedNamespace;
import org.apache.spark.sql.catalyst.analysis.ResolvedTable;
import org.apache.spark.sql.catalyst.parser.ParserInterface;
import org.apache.spark.sql.catalyst.plans.logical.LogicalPlan;
import org.apache.spark.sql.catalyst.plans.logical.RenameTable;
import org.apache.spark.sql.catalyst.plans.logical.V2WriteCommand;
import org.apache.spark.sql.connector.catalog.CatalogPlugin;
import org.apache.spark.sql.connector.catalog.Identifier;
import org.apache.spark.sql.execution.datasources.v2.DataSourceV2Relation;
import scala.runtime.AbstractFunction1;
import scala.runtime.AbstractFunction2;
import scala.runtime.BoxedUnit;

/** Installed only in the console's dedicated Spark SQL engine. */
public final class DriverGuard implements SparkSessionExtensionsProvider {
    private static final String READY = "SELECT spark_console_guard_version()";
    private static final String INSTALL = "SET " + DriverPolicy.TICKET + "=";
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
                                if (statement.equals(READY)) return delegate.parsePlan("SELECT 'spark-console-driver-v2' AS version");
                                if (statement.startsWith(INSTALL)) {
                                    DriverPolicy.install(session, statement.substring(INSTALL.length()));
                                    return delegate.parsePlan("SELECT 1");
                                }
                                authorize(session, statement);
                                var parsed = delegate.parsePlan(statement);
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
            if (relation.catalog().isEmpty() || relation.identifier().isEmpty() ||
                    !SqlGuard.writeOptionsOnly(relation.options())) throw DriverPolicy.denied();
            requireBinding(relation.catalog().get(), relation.identifier().get(), session);
        } else if (plan instanceof ResolvedTable) {
            var table = (ResolvedTable) plan;
            requireBinding(table.catalog(), table.identifier(), session);
        } else if (plan instanceof ResolvedIdentifier) {
            requireCatalog(((ResolvedIdentifier)plan).catalog());
        } else if (plan instanceof ResolvedNamespace) {
            requireCatalog(((ResolvedNamespace)plan).catalog());
        } else if (plan.getClass().getSimpleName().equals("LogicalRelation") ||
                plan.getClass().getSimpleName().equals("HiveTableRelation")) {
            throw DriverPolicy.denied();
        }
        if (plan instanceof V2WriteCommand) check((LogicalPlan)((V2WriteCommand)plan).table(), session);
        var nested = plan.subqueries().iterator();
        while (nested.hasNext()) check(nested.next(), session);
        var children = plan.children().iterator();
        while (children.hasNext()) check(children.next(), session);
    }
}
