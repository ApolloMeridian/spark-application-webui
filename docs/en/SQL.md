# SQL workspace

Spark Control Center uses an authenticated Kyuubi REST service, Spark 3.5 with the Driver Guard extension, and a Polaris 1.6 catalog. Use the dedicated service endpoint for the console and keep existing JDBC clients on their own Kyuubi instance.

## Configure Spark Control Center

Enable `objectStorage` and `sql`. Set `sql.results.connection`, `bucket`, and `prefix` to an existing object-storage connection, bucket, and directory. The chart validates the bucket association and requires S3 conditional writes. The storage service retains its S3 credentials or GCS Workload Identity.

The SQL gateway writes pages under `<prefix>pages/<result ID>/`. Console users with storage prefix privileges can browse the directory, preview or download pages, and copy pages to locations where they have write access. The service manages result pages. The regular storage ACL applies to `artifacts/`, `staging/`, and other directories. Grant write access to runtime JARs only to release operators.

Set `sql.catalog.credentialsSecret` to a Secret with the scoped Polaris client ID and secret. Use the same principal, role, realm, warehouse, and REST URI in the gateway and Driver. The gateway shares PostgreSQL and the signing Secret with the core console and object-storage plugin.

| Helm value | Default | Effect |
| --- | --- | --- |
| `sql.limits.resultTTL` | `1h` | Each statement's result expires after completion. Use durations such as `30s`, `10m`, `4h`, or `1d`. |
| `sql.history.retention` | `30d` | Run history and original SQL text. Expand a row to read its SQL. |
| `sql.audit.retention` | `90d` | SQL audit entries. |

Each day is 24 hours. Expired SQL links stop serving pages and CSV immediately. The gateway removes expired pages in the background through the storage service and retries failed deletes. Keep the SQL gateway and storage service running until their pending cleanup has finished.

## Install a dedicated Kyuubi instance

The release includes an Apache Kyuubi 1.11.1 chart with the container `args` template fixed. Use [the dedicated values example](../../charts/kyuubi/examples/values-spark-console.yaml), change its image, catalog and bucket endpoints, and create its referenced Secrets in `kyuubi-scc`. The chart creates the `kyuubi` service account and namespaced RBAC when enabled. Restrict Kyuubi REST ingress to the console gateway. Keep the engine namespace outside generic SparkApplication submission scope.

```bash
docker load -i spark-control-center-sql-1.6.1-patch-v2.tar
guard_container=$(docker create spark-control-center-sql:1.6.1-patch-v2)
docker cp "${guard_container}:/app/sql-guard.jar" ./spark-console-sql-guard-1.6.1-patch-v2.jar
docker rm "${guard_container}"

mc cp ./spark-console-sql-guard-1.6.1-patch-v2.jar \
  minio/example-bucket/spark-console-sql/artifacts/
```

Bake the same JAR into `/opt/kyuubi/jars/` in your Kyuubi image for `ConsoleKyuubiAuth`. Set `spark.jars` to the uploaded JAR's S3A URI. The Driver must have access to the JAR, the scoped Polaris Secret, and the engine signing Secret. Use the corresponding Hadoop GCS connector and workload identity for GCS staging.

```bash
helm upgrade --install kyuubi-scc ./kyuubi-0.1.0-scc.1.6.1-patch-v2.tgz \
  --namespace kyuubi-scc --create-namespace \
  -f kyuubi-values.yaml
```

Set `sql.kyuubi.url` to your dedicated REST address. The default Service in this example is `kyuubi-scc-rest.kyuubi-scc.svc.cluster.local:10099`. Production configurations should use TLS. When testing HTTP on an isolated network, explicitly set `sql.tls.allowHTTP: true`.

## Execute SQL and delegate access

Queries use one configured Polaris catalog. Both `analytics.orders` and `polaris.analytics.orders` resolve to the same table. Quote identifiers such as `` polaris.`analytics-prod`.orders `` one segment at a time. Scripts run sequentially in one Kyuubi session. `USE` and local temporary views remain available until the script ends. A failing statement stops the following statements. Completed mutations remain committed.

Run query and `Ctrl+Enter` (`⌘+Enter` on macOS) execute the editor selection when present. With an empty selection, they execute the full editor contents. Iceberg metadata tables such as `files`, `snapshots`, and `history` use names like `polaris.namespace.table.files` and require both `read` and `metadataRead` on the base table.

Grant access at the catalog, namespace, table, or view level. Namespace grants apply to objects in that namespace. Assign nested namespaces individually. Table and view grants follow the object identity and its name. Regrant access after a same-name replacement. View queries require access to the view and the objects it reads.

The legacy `write` action continues to grant append. Grant overwrite, row update, row delete, merge, catalog removal, and purge separately. For `DROP TABLE ... PURGE`, also enable the operation in Polaris and authorize the service principal to purge. Use ordinary `DROP TABLE` to remove the catalog entry while retaining data.

The gateway signs statement authorization for the requesting user and tenant. The Driver validates the signature and matches resolved catalog relations to granted object identities. Query submissions, per-statement outcomes, cancellations, grants, revocations, and downloads appear in the operation audit. SQL text stays in query history.

Statement audit events include the target, authorization scope, statement fingerprint, and Kyuubi operation ID. A statement that fails before execution records its phase and reason. A script's terminal event reports total, attempted, engine-accepted, and skipped statements. The audit API exposes these structured fields under `sqlDetails`.

Managed grants and recorded expiry times from `1.6.0-patch` carry forward using the same PostgreSQL database and Polaris configuration. For upgrades from the original `1.6.0`, regrant access to the new managed catalog.

## Iceberg maintenance SQL and permissions

The target is Spark 3.5.9 and Iceberg 1.10.2 (Spark 3.5, Scala 2.12 runtime). Upgrade the gateway and Driver Guard JAR together, then restart the dedicated Kyuubi SQL engines. Each submission accepts up to 20 statements, 32 KiB of SQL, and a 64 KiB JSON body. Each statement accepts up to 32 input relations and 64 access checks after expanding views. Persistent names use lowercase ASCII letters, digits and underscores, start with a letter or underscore, and accept hyphens in backtick-quoted segments. CTE and session temporary view aliases are case-insensitive.

The following actions are Spark Control Center grants. Polaris independently authorizes the scoped workload principal used by the gateway and Driver.

| SQL operation | Console permission | Notes |
| --- | --- | --- |
| `SELECT`, `EXPLAIN SELECT`, `DESCRIBE QUERY SELECT` | Source relation `read` | Views also require access to their dependencies. `SHOW SYSTEM FUNCTIONS` lists built-ins. |
| `SHOW CREATE TABLE`, `DESCRIBE TABLE`, `SHOW TBLPROPERTIES` | Object `metadataRead` | Iceberg metadata relations also require base-table `read`. |
| `CREATE TABLE`, CTAS, create view or namespace | Parent `create` and source `read` | Use managed Iceberg tables. Table creation supports format v1 and v2. |
| `INSERT INTO`, `INSERT OVERWRITE` | Table `write` or `overwrite` and source `read` | Use hidden Iceberg partitioning. |
| `UPDATE`, `DELETE`, `MERGE`, `TRUNCATE TABLE` | Table `update`, `deleteRows`, `merge`, `deleteRows` | Subqueries and MERGE source tables require `read`. TRUNCATE removes current rows and preserves the table and its history. |
| Column or property changes, partition-field evolution, write ordering, identifier fields | Table `metadataWrite` | Editing snapshot retention also requires `expireSnapshots`. |
| `ALTER VIEW ... AS` and `SET/UNSET TBLPROPERTIES` | View `metadataWrite` and source `read` | The editable view property is `comment`. |
| Rename table or view | Source `metadataWrite` and destination namespace `create` | The destination name must be unoccupied. |
| Drop table, purge, drop view or empty namespace | Object `delete` or table `purge` | Polaris must support the purge operation. |
| `CALL polaris.system.rewrite_data_files('db.tbl')` | Table `rewriteDataFiles` | Default compaction can consume substantial compute and temporary storage. |
| `CALL polaris.system.rewrite_manifests('db.tbl')` | Table `rewriteManifests` | Optional nonnegative named `spec_id`. |
| `CALL polaris.system.expire_snapshots(table => 'db.tbl', stream_results => true)` | Table `expireSnapshots` | Physically removes files reachable only from expired snapshots. Optional positive `retain_last`. |
| `CALL polaris.system.remove_orphan_files(table => 'db.tbl', dry_run => true, prefix_listing => true)` | Table `previewOrphanFiles` | Reports candidates through Iceberg FileIO. Platform operators handle deletion. |

Procedure arguments use literals and a table qualified with its namespace. Location overrides, free-form predicates and external inputs are outside this interface. Delegate expiration to a dedicated maintenance role. Orphan previews list the managed table location with Iceberg FileIO and can use Polaris-vended credentials. Snapshot retention settings have a floor of one day or one snapshot. Commit retries, Parquet layout and write properties are individually allowlisted. Direct table grants bind to the table UUID. On upgrade, Helm bootstrap administrators who still have the full original grant receive the new actions once.

Submission errors distinguish syntax (400), platform capability (422), authorization (403) and parser failure or unavailability (503). Queued scripts record failure phases in history and audit. These responses omit SQL text and upstream stack traces.

### Platform-managed operations

- Explicit time travel and branch or tag reads, writes and management are disabled. Authorized metadata tables can still expose historical metadata. Account for snapshots, references and storage retention when planning history removal.
- Implicit schema evolution is disabled. Use `ALTER TABLE` under `metadataWrite` before a regular MERGE under `merge`.
- `CREATE OR REPLACE TABLE` and `REPLACE TABLE` are disabled. Atomic replacement changes table data and schema and needs separate authorization and identity checks. Use `CREATE TABLE IF NOT EXISTS` or `INSERT OVERWRITE` as appropriate.
- For `CREATE TABLE LIKE`, review `SHOW CREATE TABLE` output and edit the target. `ANALYZE TABLE` is unavailable for managed Iceberg V2 tables. Query `.files` or `.partitions` for operational statistics. These queries leave Spark optimizer statistics unchanged.
- Explicit `INSERT ... PARTITION`, `REPLACE WHERE`, format upgrades, arbitrary locations, external UDFs and unlisted settings are denied. Other maintenance capabilities include position-delete file rewrites, snapshot rollback or switching, physical orphan deletion, and external table registration.

See the upstream [Iceberg 1.10.2 DDL](https://iceberg.apache.org/docs/1.10.2/spark-ddl/), [writes](https://iceberg.apache.org/docs/1.10.2/spark-writes/), [maintenance procedures](https://iceberg.apache.org/docs/1.10.2/spark-procedures/) and [Polaris 1.6.0](https://polaris.apache.org/releases/1.6.0/) documentation.
