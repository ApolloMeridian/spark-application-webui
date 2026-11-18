# SQL workspace

Spark Control Center 1.6.0 provides an optional read-only workspace for Kyuubi 1.11.0, Spark 3.5 and Apache Polaris 1.6.0. It accepts one supported SELECT statement with fully qualified `polaris.namespace.table` references. Common CTEs, joins, aggregates, window functions and selected Spark/Hive built-ins are supported. The catalog tree shows granted tables and loads the current schema on selection. Use the catalog's administrative tools for namespace and table DDL.

The gateway submits a single user statement to Kyuubi, stores bounded result pages in S3, and closes the operation and session. Paging and CSV downloads reuse the saved result. CSV contains the rows retained for that query, including when the result was truncated. PostgreSQL stores table grants, query state, and audit records. The operation audit records actors, SQL fingerprints, input tables, decisions and operation IDs. SQL text is removed from the query record when execution ends. Spark and Kyuubi have their own logging and retention settings.

## Configure a dedicated Kyuubi endpoint

Use a private Kyuubi service dedicated to this gateway. Allow only the gateway to connect. Enable authenticated Basic REST with LDAP or Kyuubi JDBC authentication and disable anonymous access. For JDBC authentication, use a dedicated read-only database login and verify password hashes with the database's password hashing extension. Store the authentication configuration in a Secret. Use one Kyuubi replica, or ensure all requests in a REST session reach the instance that owns its handles. [Kyuubi authentication](https://kyuubi.readthedocs.io/en/v1.11.0/security/jdbc.html) · [Kyuubi REST API](https://kyuubi.readthedocs.io/en/v1.11.0/client/rest/rest_api.html).

```properties
kyuubi.authentication=JDBC
kyuubi.frontend.protocols=REST
kyuubi.frontend.rest.bind.port=10099
kyuubi.frontend.rest.ui.enabled=false
kyuubi.engine.type=SPARK_SQL
kyuubi.engine.share.level=USER
kyuubi.engine.doAs.enabled=false
kyuubi.engine.single.spark.session=false
kyuubi.operation.interrupt.on.cancel=true
kyuubi.operation.query.timeout=PT10M
kyuubi.session.engine.idle.timeout=PT5M
kyuubi.session.idle.timeout=PT10M
kyuubi.session.engine.spark.max.lifetime=PT2H
kyuubi.engine.submit.timeout=PT180S
kyuubi.engine.kubernetes.submit.timeout=PT180S
```

Use a dedicated Spark driver namespace, ServiceAccount, Pod quotas and network policy. Limit access to mounted credentials, Spark UI, logs, and workloads in this namespace. In particular, generic SparkApplication submission and the legacy Kyuubi endpoint must have no access to the protected namespace or protected table data. Separate the staging and event-log locations and credentials from business tables and query results.

Add `/app/sql-guard.jar` from `spark-control-center-sql:1.6.0` to `/opt/spark/jars` in the Spark driver image. Use JDK 11 or later, Iceberg Spark 3.5/Scala 2.12 runtime and a compatible Iceberg AWS bundle. The submitter and driver may have different Hadoop versions. Match `hadoop-aws` to the Hadoop version actually loaded in each image. Hadoop 3.3.x uses the AWS SDK v1, while Hadoop 3.4.x uses v2 for S3A. Iceberg S3FileIO has its own runtime dependencies. [Hadoop AWS setup](https://hadoop.apache.org/docs/r3.4.3/hadoop-aws/tools/hadoop-aws/index.html) · [Polaris Spark client](https://polaris.apache.org/releases/1.6.0/polaris-spark-client/).

```bash
container="$(docker create spark-control-center-sql:1.6.0)"
docker cp "$container:/app/sql-guard.jar" ./sql-guard.jar
docker rm "$container"
```

Set the following Spark properties on the dedicated Kyuubi service. Omit the catalog `type` property when using `catalog-impl`. Put a single `clientId:clientSecret` credential in a Secret key named `credential` for each Spark user. Inject the corresponding Secret only into that user's driver. Use explicit Polaris principal roles for each user.

```properties
spark.sql.extensions=org.apache.iceberg.spark.extensions.IcebergSparkSessionExtensions
spark.sql.catalog.polaris=org.apache.iceberg.spark.SparkCatalog
spark.sql.catalog.polaris.catalog-impl=org.sparkconsole.MountedRESTCatalog
spark.sql.catalog.polaris.uri=https://polaris.example.com/api/catalog
spark.sql.catalog.polaris.warehouse=example_catalog
spark.sql.catalog.polaris.oauth2-server-uri=https://polaris.example.com/api/catalog/v1/oauth/tokens
spark.sql.catalog.polaris.header.Polaris-Realm=POLARIS
spark.sql.catalog.polaris.cache-enabled=false
spark.sql.defaultCatalog=polaris
spark.sql.variable.substitute=false
spark.driver.maxResultSize=128m
```

Add per-user defaults to Kyuubi's configuration. Keep the username, client ID and principal role in sync with the gateway profile. Check the Kyuubi launch configuration and recycle the corresponding engine after changing its defaults. Leave inline catalog `credential` and `token` unset.

```properties
___user_alice___.spark.kubernetes.driver.secrets.polaris-user-alice=/var/run/spark-console/polaris/user_alice
___user_alice___.spark.sql.catalog.polaris.console-credential-file=/var/run/spark-console/polaris/user_alice/credential
___user_alice___.spark.sql.catalog.polaris.console-principal-id=ALICE_CLIENT_ID
___user_alice___.spark.sql.catalog.polaris.scope=PRINCIPAL_ROLE:alice_reader
```

Polaris must vend credentials appropriate for the business bucket. For lab MinIO, configure the catalog's S3 endpoint and path-style access when creating the catalog, and verify Iceberg reads from Spark as a non-root principal. In COS, confirm STS delegation and S3FileIO with the actual catalog and its configured roles. Configure a separate results bucket with a lifecycle rule for the `spark-console-sql/` prefix. Its gateway credential requires `s3:PutObject` and `s3:GetObject` on that prefix. SQL results should be inaccessible through the object-storage browser.

## Configure Helm and grant access

Put all overrides under `sql` in the same production values file. Set `sql.enabled: true`, the private REST endpoints, the warehouse, and the results endpoint and bucket. Use TLS with both REST services and S3 in production. Mount a private CA using `sql.tls.caConfigMap` if needed. An isolated MinIO test deployment can use the settings in [values-sql-minio.yaml](../../charts/spark-control-center/examples/values-sql-minio.yaml). This plugin has no Kubernetes API permissions.

Create the referenced Secrets in the console release namespace.

| Setting | Secret data |
| --- | --- |
| `sql.internalAuth.existingSecret` | `signing-key` containing at least 32 random bytes |
| `sql.database.existingSecret` | `dsn` pointing to the core PostgreSQL database and schema |
| `sql.identities.existingSecret` | `profiles.json` indexed by immutable console user ID |
| `sql.results.credentialsSecret` | `access-key` and `secret-key` for the dedicated results bucket |

Example profile structure. Read the immutable console user ID and tenant ID from an authorized user record in the core API or PostgreSQL. Use separately provisioned Kyuubi and Polaris credentials for each user. Restart the gateway after updating the Secret.

```json
{
  "CONSOLE_USER_ID": {
    "tenant": "TENANT_ID",
    "kyuubiUser": "user_alice",
    "kyuubiPassword": "Kyuubi password from secret manager",
    "polarisClientId": "ALICE_CLIENT_ID",
    "polarisClientSecret": "Polaris secret from secret manager",
    "polarisPrincipalRole": "alice_reader"
  }
}
```

Enable SQL access on the user's tenant. Only administrators in a tenant with SQL grant management can grant SELECT. Provision the minimum Polaris role first, then grant access to the fully qualified table through the workspace. Grants apply to the current table identity and the user's tenant. Revoking a console grant invalidates existing results and cancels affected work. Change the underlying Polaris permission separately when access should also be removed outside the console.

## Query limits and engine tuning

The default result limit is 10,000 rows or 16 MiB, and the protocol fetches at most 100 rows per request. At most one query runs per user, with two workers by default. Tune these settings under `sql.limits`. Use a dedicated bucket lifecycle so partially written results and expired results are reclaimed. Browser pages and CSV downloads expire after the configured result TTL. Set a Kyuubi-side query timeout and session idle timeout so a query whose gateway has stopped will also be reclaimed. Restarting the gateway does not re-execute an interrupted statement.

An idle USER engine can serve another query from the same account. For Kubernetes, test an initial executor count of one, zero minimum executors and a 60–120 second executor idle timeout. Preserve dedicated warm capacity if measurements justify it. Driver startup, image pulls and executor scheduling still determine cold-query latency. Keep Spark's result-size guard enabled. Increasing the gateway's persisted-result budget requires validating Spark result collection, memory headroom and data shape. Large asynchronous file-backed export is outside this release. [Kyuubi dynamic allocation](https://kyuubi.readthedocs.io/en/v1.11.0/deployment/spark/dynamic_allocation.html) · [Kyuubi large-result collection](https://kyuubi.readthedocs.io/en/v1.11.0/deployment/spark/large_query_results.html).
