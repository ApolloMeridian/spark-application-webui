import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";

const { version } = JSON.parse(readFileSync("package.json", "utf8"));
const image = "spark-control-center-sql:" + (process.env.QA_VERSION || version);
function inspect(sql) {
  const result = spawnSync("docker", [
    "run", "--rm", "--network", "none", "--hostname", "sql-parser",
    "--add-host", "sql-parser:127.0.0.1", "--memory", "512m", "--entrypoint", "java",
    image, "-Xmx256m", "-cp", "/app/sql-guard.jar:/app/lib/*", "org.sparkconsole.SqlGuard", "--check", sql,
  ], { encoding: "utf8", timeout: 30000 });
  assert.ifError(result.error);
  return result;
}
const good = inspect(`
  WITH filtered AS (SELECT id FROM polaris.events.purchases WHERE amount > 10)
  SELECT f.id, count(*) AS matching FROM filtered f
  JOIN polaris.analytics.customers c ON c.id = f.id
  WHERE EXISTS (SELECT 1 FROM polaris.events.audit a WHERE a.id = f.id)
  GROUP BY f.id`);
assert.equal(good.status, 0, good.stderr);
assert.deepEqual(good.stdout.trim().split(/\r?\n/), [
  "query", "table\tread\tpolaris.analytics.customers", "table\tread\tpolaris.events.audit", "table\tread\tpolaris.events.purchases",
]);
const append = inspect("INSERT INTO polaris.events.purchases SELECT id FROM polaris.analytics.customers");
assert.equal(append.status, 0, append.stderr);
assert.deepEqual(append.stdout.trim().split(/\r?\n/), ["command", "table\twrite\tpolaris.events.purchases", "table\tread\tpolaris.analytics.customers"]);
for (const [sql, expected] of [
  ["SELECT * FROM events.purchases", "query\ntable\tread\tpolaris.events.purchases"],
  ["SELECT * FROM range(10)", "query"],
  ["SHOW CREATE TABLE events.purchases", "metadata\ntable\tmetadataRead\tpolaris.events.purchases"],
  ["INSERT OVERWRITE TABLE polaris.events.purchases SELECT 1", "command\ntable\toverwrite\tpolaris.events.purchases"],
  ["DROP TABLE polaris.events.purchases PURGE", "command\ntable\tpurge\tpolaris.events.purchases"],
  ["CREATE TABLE polaris.`with-dashes`.items (id INT) USING iceberg COMMENT 'comment' TBLPROPERTIES ('format-version'='2')",
    "command\nnamespace\tcreate\tpolaris.with-dashes"],
]) {
  const parsed = inspect(sql);
  assert.equal(parsed.status, 0, parsed.stderr);
  assert.equal(parsed.stdout.trim().replaceAll("\r\n", "\n"), expected);
}
for (const sql of [
  "SELECT * FROM parquet.`s3a://example/private/`",
  "SELECT * FROM polaris.events.purchases.files",
  "SELECT reflect('java.lang.System', 'getenv', 'PATH')",
  "SELECT * FROM spark_catalog.events.purchases",
  "CREATE TABLE polaris.events.purchases (id INT) USING iceberg LOCATION 's3a://example/private/'",
  "ALTER TABLE polaris.events.purchases SET TBLPROPERTIES ('write.data.path'='s3a://example/private/')",
  "SELECT 1; DROP TABLE polaris.events.purchases",
]) assert.notEqual(inspect(sql).status, 0, "Unexpectedly accepted: " + sql);
console.log("PASS: packaged Spark parser classifies read, metadata, DDL, and write permissions.");
