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
  "polaris.analytics.customers", "polaris.events.audit", "polaris.events.purchases",
]);
for (const sql of [
  "SELECT * FROM parquet.`s3a://example/private/`",
  "SELECT * FROM polaris.events.purchases.files",
  "SELECT reflect('java.lang.System', 'getenv', 'PATH')",
  "SELECT * FROM default.unqualified",
  "SELECT * FROM range(10)",
  "SHOW TABLES",
  "SELECT 1; DROP TABLE polaris.events.purchases",
]) assert.notEqual(inspect(sql).status, 0, "Unexpectedly accepted: " + sql);
console.log("PASS: packaged Spark parser resolves CTE, JOIN and subquery tables and rejects unsupported operations.");
