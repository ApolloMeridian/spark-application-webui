# SQL workspace

Spark Control Center uses an authenticated Kyuubi REST service, Spark 3.5 with the Driver Guard extension, and a Polaris 1.6 catalog. Use the dedicated service endpoint for the console and keep existing JDBC clients on their own Kyuubi instance.

## Configure Spark Control Center

Enable `objectStorage` and `sql`. Set `sql.results.connection`, `bucket`, and `prefix` to an existing object-storage connection, bucket, and directory. The chart validates the bucket association and requires S3 conditional writes. The storage service retains its S3 credentials or GCS Workload Identity.

The SQL gateway writes pages under `<prefix>pages/<result ID>/`. Console users with storage prefix privileges can browse the directory, preview or download pages, and copy pages to locations where they have write access. The plugin protects result pages, JARs, and staging objects from user writes and deletes. It hides JARs and staging objects from the browser. Keep the result location configured as long as data or Spark artifacts use it.

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
docker load -i spark-control-center-sql-1.6.1.tar
guard_container=$(docker create spark-control-center-sql:1.6.1)
docker cp "${guard_container}:/app/sql-guard.jar" ./spark-console-sql-guard-1.6.1.jar
docker rm "${guard_container}"

mc cp ./spark-console-sql-guard-1.6.1.jar \
  minio/example-bucket/spark-console-sql/artifacts/
```

Bake the same JAR into `/opt/kyuubi/jars/` in your Kyuubi image for `ConsoleKyuubiAuth`. Set `spark.jars` to the uploaded JAR's S3A URI. The Driver must have access to the JAR, the scoped Polaris Secret, and the engine signing Secret. Use the corresponding Hadoop GCS connector and workload identity for GCS staging.

```bash
helm upgrade --install kyuubi-scc ./kyuubi-0.1.0-scc.1.6.1.tgz \
  --namespace kyuubi-scc --create-namespace \
  -f kyuubi-values.yaml
```

Set `sql.kyuubi.url` to your dedicated REST address. The default Service in this example is `kyuubi-scc-rest.kyuubi-scc.svc.cluster.local:10099`. Production configurations should use TLS. When testing HTTP on an isolated network, explicitly set `sql.tls.allowHTTP: true`.

## Execute SQL and delegate access

Queries use one configured Polaris catalog. Both `analytics.orders` and `polaris.analytics.orders` resolve to the same table. Quote identifiers such as `` polaris.`analytics-prod`.orders `` one segment at a time. Scripts run sequentially in one Kyuubi session. `USE` and local temporary views remain available until the script ends. A failing statement stops the following statements. Completed mutations remain committed.

Grant access at the catalog, namespace, table, or view level. Namespace grants apply to objects in that namespace. Assign nested namespaces individually. Table and view grants follow the object identity and its name. Regrant access after a same-name replacement. View queries require access to the view and the objects it reads.

The legacy `write` action continues to grant append. Grant overwrite, row update, row delete, merge, catalog removal, and purge separately. For `DROP TABLE ... PURGE`, also enable the operation in Polaris and authorize the service principal to purge. Use ordinary `DROP TABLE` to remove the catalog entry while retaining data.

The gateway signs statement authorization for the requesting user and tenant. The Driver validates the signature and matches resolved catalog relations to granted object identities. Query submissions, per-statement outcomes, cancellations, grants, revocations, and downloads appear in the operation audit. SQL text stays in query history.

Managed grants and recorded expiry times from `1.6.0-patch` carry forward using the same PostgreSQL database and Polaris configuration. For upgrades from the original `1.6.0`, regrant access to the new managed catalog.
