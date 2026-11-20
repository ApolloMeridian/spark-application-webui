package org.sparkconsole;

import java.util.Arrays;
import org.apache.iceberg.spark.SparkCatalog;
import org.apache.iceberg.spark.source.SparkTable;
import org.apache.spark.sql.catalyst.analysis.NoSuchTableException;
import org.apache.spark.sql.connector.catalog.Identifier;
import org.apache.spark.sql.connector.catalog.Table;

public final class ManagedSparkCatalog extends SparkCatalog {
    @Override
    public Table loadTable(Identifier identifier) throws NoSuchTableException {
        var parts = identifier.namespace();
        if (parts.length < 2 || !identifier.name().startsWith("branch_")) return super.loadTable(identifier);
        var base = Identifier.of(Arrays.copyOf(parts, parts.length - 1), parts[parts.length - 1]);
        var policy = DriverPolicy.current();
        var expected = policy.binding("@branch:" + buildIdentifier(identifier));
        if (expected == null || !expected.equals(policy.binding(buildIdentifier(base).toString()))) throw DriverPolicy.denied();
        var table = super.loadTable(base);
        if (!(table instanceof SparkTable)) throw DriverPolicy.denied();
        var sparkTable = (SparkTable)table;
        String branch = identifier.name().substring(7);
        var ref = sparkTable.table().refs().get(branch);
        if (ref == null || !ref.isBranch()) throw new NoSuchTableException(identifier);
        return sparkTable.copyWithBranch(branch);
    }

    @Override
    public Table loadTable(Identifier identifier, String version) throws NoSuchTableException {
        requireHistory(identifier);
        return super.loadTable(identifier, version);
    }

    @Override
    public Table loadTable(Identifier identifier, long timestamp) throws NoSuchTableException {
        requireHistory(identifier);
        return super.loadTable(identifier, timestamp);
    }

    private void requireHistory(Identifier identifier) {
        var policy = DriverPolicy.current();
        var name = buildIdentifier(identifier).toString();
        var expected = policy.binding("@history:" + name);
        if (expected == null || !expected.equals(policy.binding(name))) throw DriverPolicy.denied();
    }

    @Override
    public boolean purgeTable(Identifier identifier) {
        // Use Polaris's authorized purge operation. SparkCatalog's default
        // unregisters first and deletes files on the Driver.
        if (!(icebergCatalog() instanceof ManagedRESTCatalog)) throw DriverPolicy.denied();
        return icebergCatalog().dropTable(buildIdentifier(identifier), true);
    }
}
