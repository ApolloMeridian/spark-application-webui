package org.sparkconsole;

import org.apache.iceberg.spark.SparkCatalog;
import org.apache.spark.sql.connector.catalog.Identifier;

public final class ManagedSparkCatalog extends SparkCatalog {
    @Override
    public boolean purgeTable(Identifier identifier) {
        // Use Polaris's authorized purge operation. SparkCatalog's default
        // unregisters first and deletes files on the Driver.
        if (!(icebergCatalog() instanceof ManagedRESTCatalog)) throw DriverPolicy.denied();
        return icebergCatalog().dropTable(buildIdentifier(identifier), true);
    }
}
