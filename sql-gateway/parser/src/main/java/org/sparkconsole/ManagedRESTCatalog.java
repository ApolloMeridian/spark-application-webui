package org.sparkconsole;

import java.util.HashMap;
import java.util.Locale;
import java.util.Map;
import org.apache.iceberg.BaseTable;
import org.apache.iceberg.Table;
import org.apache.iceberg.catalog.TableIdentifier;
import org.apache.iceberg.exceptions.NoSuchTableException;
import org.apache.iceberg.exceptions.NoSuchViewException;
import org.apache.iceberg.rest.RESTCatalog;
import org.apache.iceberg.view.View;

/** Uses the dedicated SQL workload's catalog-scoped Polaris principal. */
public final class ManagedRESTCatalog extends RESTCatalog {
    @Override
    public void initialize(String name, Map<String, String> props) {
        if (props.containsKey("credential") || props.containsKey("token")) {
            throw new IllegalArgumentException("Inline Polaris credentials are forbidden.");
        }
        var config = new HashMap<>(props);
        String id = System.getenv("SPARK_CONSOLE_POLARIS_CLIENT_ID");
        String secret = System.getenv("SPARK_CONSOLE_POLARIS_CLIENT_SECRET");
        String expected = config.remove("console-principal-id");
        config.remove("console-table-bindings");
        String scope = config.getOrDefault("scope", "");
        if (id == null || !id.equals(expected) || id.toLowerCase(Locale.ROOT).contains("root") ||
                secret == null || secret.isBlank() || !scope.matches("PRINCIPAL_ROLE:[A-Za-z0-9_.-]+") ||
                scope.equalsIgnoreCase("PRINCIPAL_ROLE:ALL")) {
            throw new IllegalArgumentException("A scoped Polaris workload principal is required.");
        }
        config.put("credential", id + ":" + secret);
        config.put("header.X-Iceberg-Access-Delegation", "vended-credentials");
        super.initialize(name, config);
    }

    @Override
    public Table loadTable(TableIdentifier identifier) {
        String expected = DriverPolicy.current().binding(identifier.toString());
        if (expected == null) {
            throw new NoSuchTableException("Table is outside the console authorization binding.");
        }
        Table table = super.loadTable(identifier);
        if (!(table instanceof BaseTable) || !expected.equals(((BaseTable) table).operations().current().uuid())) {
            throw new NoSuchTableException("Table identity changed.");
        }
        return table;
    }

    @Override
    public View loadView(TableIdentifier identifier) {
        String expected = DriverPolicy.current().binding(identifier.toString());
        if (expected == null) {
            throw new NoSuchViewException("View is outside the console authorization binding.");
        }
        View view = super.loadView(identifier);
        if (view.uuid() == null || !expected.equals(view.uuid() + ":" + view.currentVersion().versionId())) {
            throw new NoSuchViewException("View identity changed.");
        }
        return view;
    }

    @Override
    public boolean dropTable(TableIdentifier identifier, boolean purge) {
        loadTable(identifier);
        return super.dropTable(identifier, purge);
    }

    @Override
    public void renameTable(TableIdentifier from, TableIdentifier to) {
        loadTable(from);
        super.renameTable(from, to);
    }

    @Override
    public boolean dropView(TableIdentifier identifier) {
        loadView(identifier);
        return super.dropView(identifier);
    }

    @Override
    public void renameView(TableIdentifier from, TableIdentifier to) {
        loadView(from);
        super.renameView(from, to);
    }
}
