import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";

const version = process.env.QA_VERSION || "1.5.1";
const prefix = `spark-workflow-qa-${randomBytes(4).toString("hex")}`;
const password = randomBytes(24).toString("base64url");
const containers = [];
let network;
const docker = (...args) =>
  execFileSync("docker", args, {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();
const run = (...args) => {
  const id = docker("run", "-d", "--label", "spark-console.qa=true", ...args);
  containers.push(id);
  return id;
};
const wait = async (fn) => {
  for (let i = 0; i < 60; i++) {
    try {
      const value = await fn();
      if (value) return value;
    } catch {}
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  throw new Error("Container readiness timeout");
};
let cookie = "";
async function api(path, method = "GET", body) {
  const response = await fetch(`http://127.0.0.1:18081/v1${path}`, {
    method,
    headers: { Cookie: cookie, "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const json = await response.json();
  if (!response.ok)
    throw new Error(`${response.status} ${JSON.stringify(json)}`);
  return json;
}
const manifest =
  'apiVersion: sparkoperator.k8s.io/v1beta2\nkind: SparkApplication\nmetadata:\n  name: container-test\nspec:\n  image: apache/spark:3.5.3\n  arguments: ["$[yyyy-MM-dd-1]"]\n  restartPolicy:\n    type: Never\n';
try {
  network = docker("network", "create", prefix);
  const db = run(
    "--name",
    `${prefix}-db`,
    "--network",
    network,
    "--network-alias",
    "qa-postgres",
    "-e",
    "POSTGRES_USER=spark_test",
    "-e",
    `POSTGRES_PASSWORD=${password}`,
    "-e",
    "POSTGRES_DB=spark_test",
    "postgres:15-alpine",
  );
  await wait(() =>
    docker("exec", db, "pg_isready", "-U", "spark_test").includes(
      "accepting connections",
    ),
  );
  const fixture = run(
    "--name",
    `${prefix}-kube`,
    "--network",
    network,
    "--network-alias",
    "qa-kube",
    "-p",
    "127.0.0.1:18082:8080",
    "--mount",
    `type=bind,source=${resolve("scripts/fixtures/workflow-kubernetes.mjs")},target=/fixture.mjs,readonly`,
    "node:22-alpine",
    "node",
    "/fixture.mjs",
  );
  await wait(async () => (await fetch("http://127.0.0.1:18082/test/stats")).ok);
  const backendArgs = [
    "--name",
    `${prefix}-backend`,
    "--network",
    network,
    "-p",
    "127.0.0.1:18081:8081",
    "-e",
    "KUBERNETES_API_URL=http://qa-kube:8080",
    "-e",
    "KUBERNETES_BEARER_TOKEN=qa-http-fixture",
    "-e",
    "WATCH_NAMESPACES=spark",
    "-e",
    "PROMETHEUS_URL=http://qa-kube:8080",
    "-e",
    "DATABASE_HOST=qa-postgres",
    "-e",
    "DATABASE_NAME=spark_test",
    "-e",
    "DATABASE_USERNAME=spark_test",
    "-e",
    `DATABASE_PASSWORD=${password}`,
    "-e",
    "AUTH_INITIAL_ADMIN_USERNAME=qa-admin",
    "-e",
    `AUTH_INITIAL_ADMIN_PASSWORD=${password}`,
    "-e",
    "AUTH_BCRYPT_COST=10",
    "-e",
    "WORKFLOW_TIMEZONE=Asia/Shanghai",
    "-e",
    "WORKFLOW_SCHEDULER_INTERVAL=1s",
    `spark-control-center-backend:${version}`,
  ];
  let backend = run(...backendArgs);
  await wait(async () => (await fetch("http://127.0.0.1:18081/readyz")).ok);
  const login = await fetch("http://127.0.0.1:18081/v1/auth/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username: "qa-admin", password }),
  });
  assert.equal(login.status, 200);
  cookie = login.headers.get("set-cookie").split(";")[0];
  const project = await api("/projects", "POST", {
    name: "Container pipeline",
    description: "Production images + PostgreSQL; Kubernetes HTTP fixture",
  });
  const flow = await api(`/projects/${project.id}/workflows`, "POST", {
    name: "Daily pipeline",
    enabled: true,
    timeZone: "Asia/Shanghai",
    nodes: [
      {
        id: "extract",
        name: "Extract",
        namespace: "spark",
        manifest,
        parameters: [],
        values: {},
        dependsOn: [],
        strategy: "parallel",
        retries: 0,
        timeoutMinutes: 5,
      },
    ],
  });
  const trigger = await api(
    `/projects/${project.id}/workflows/${flow.id}/trigger`,
    "POST",
    {},
  );
  await wait(
    async () =>
      (await api(`/projects/${project.id}/runs`))[0]?.status === "running",
  );
  assert.equal(
    (await (await fetch("http://127.0.0.1:18082/test/stats")).json())
      .submissions,
    1,
  );
  // Kill and replace only the exact backend container created above.
  docker("rm", "-f", backend);
  containers.splice(containers.indexOf(backend), 1);
  backend = run(...backendArgs);
  await wait(async () => (await fetch("http://127.0.0.1:18081/readyz")).ok);
  const restored = (await api(`/projects/${project.id}/runs`))[0];
  assert.equal(restored.id, trigger.id);
  assert.equal(restored.snapshot.version, 1);
  await fetch("http://127.0.0.1:18082/test/state", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ state: "COMPLETED" }),
  });
  await wait(
    async () =>
      (await api(`/projects/${project.id}/runs`))[0]?.status === "succeeded",
  );
  assert.equal(
    (await (await fetch("http://127.0.0.1:18082/test/stats")).json())
      .submissions,
    1,
  );
  assert(
    (await api("/audit")).some((x) => x.operation === "WORKFLOW_FINISHED"),
  );
  const changed = await api(
    `/projects/${project.id}/workflows/${flow.id}`,
    "PATCH",
    { ...flow, name: "Edited pipeline" },
  );
  assert.equal(changed.version, 2);
  const pointer = await api(
    `/projects/${project.id}/workflows/${flow.id}/versions/1/restore`,
    "POST",
    {},
  );
  assert.equal(pointer.version, 1);
  assert.equal(
    (await api(`/projects/${project.id}/workflows/${flow.id}/versions`)).length,
    2,
  );
  const backfillPath = `/projects/${project.id}/workflows/${flow.id}/backfill`;
  const backfill = {
    cron: "* * * * * *",
    timeZone: "UTC",
    from: "2026-10-01T00:00:00Z",
    to: "2026-10-01T00:03:24Z",
    direction: "backward",
    failurePolicy: "continue",
    confirm: false,
  };
  const occurrences = await api(backfillPath, "POST", backfill);
  assert.equal(occurrences.length, 50);
  assert.equal(occurrences[0], backfill.to);
  await api(backfillPath, "POST", { ...backfill, confirm: true });
  await wait(async () =>
    (await api(`/projects/${project.id}/runs`)).some(
      (r) => r.trigger === "backfill" && r.status === "running",
    ),
  );
  let batch = (await api(`/projects/${project.id}/backfills`))[0];
  assert.equal(batch.createdCount, 1);
  assert.equal(batch.direction, "backward");
  assert.equal(batch.failurePolicy, "continue");
  docker("rm", "-f", backend);
  containers.splice(containers.indexOf(backend), 1);
  backend = run(...backendArgs);
  await wait(async () => (await fetch("http://127.0.0.1:18081/readyz")).ok);
  batch = (await api(`/projects/${project.id}/backfills`))[0];
  assert.equal(batch.createdCount, 1);
  assert.equal(
    (await (await fetch("http://127.0.0.1:18082/test/stats")).json())
      .submissions,
    2,
  );
  await api(`/projects/${project.id}/backfills/${batch.id}/cancel`, "POST", {});
  assert.equal(
    (await api(`/projects/${project.id}/backfills`))[0].status,
    "cancelled",
  );
  const pending = (await api(`/projects/${project.id}/runs`)).find(
    (r) => r.trigger === "backfill",
  );
  await api(`/projects/${project.id}/runs/${pending.id}/cancel`, "POST", {});
  await wait(
    async () =>
      (await api(`/projects/${project.id}/runs`)).find(
        (r) => r.id === pending.id,
      )?.status === "cancelled",
  );
  assert.equal(
    (await api(`/projects/${project.id}/backfills`))[0].createdCount,
    1,
  );
  await mkdir(".qa/container", { recursive: true });
  const nginx = `server { listen 8080; root /usr/share/nginx/html; location /api/ { proxy_pass http://${prefix}-backend:8081/; } location / { try_files $uri $uri/ /index.html; } }`;
  await writeFile(".qa/container/nginx.conf", nginx);
  await writeFile(
    ".qa/container/config.js",
    "window.__SPARK_CONTROL_CENTER_CONFIG__ = " +
      JSON.stringify({
        dataMode: "api",
        auth: { mode: "local" },
        cluster: { name: "qa-cluster", namespaces: ["spark"] },
        features: { workflows: true },
      }) +
      ";",
  );
  const frontend = run(
    "--name",
    `${prefix}-frontend`,
    "--network",
    network,
    "-p",
    "127.0.0.1:18080:8080",
    "--mount",
    `type=bind,source=${resolve(".qa/container/nginx.conf")},target=/etc/nginx/conf.d/default.conf,readonly`,
    "--mount",
    `type=bind,source=${resolve(".qa/container/config.js")},target=/usr/share/nginx/html/config/config.js,readonly`,
    `spark-control-center:${version}`,
  );
  await wait(async () => (await fetch("http://127.0.0.1:18080/login")).ok);
  assert.equal(
    (
      await fetch("http://127.0.0.1:18080/api/v1/projects", {
        headers: { Cookie: cookie },
      })
    ).status,
    200,
  );
  console.log(
    "PASS: production images, PostgreSQL, pointer restore, 50-item backfill preview, durable reverse/serial backfill recovery and cancellation, no duplicate submissions, audit, timezone and same-origin proxy. Kubernetes is an HTTP fixture, not a real cluster.",
  );
} finally {
  for (const container of containers.reverse()) {
    try {
      assert.equal(
        docker(
          "inspect",
          "--format",
          '{{index .Config.Labels "spark-console.qa"}}',
          container,
        ),
        "true",
      );
      docker("rm", "-f", container);
    } catch {}
  }
  if (network) {
    try {
      docker("network", "rm", network);
    } catch {}
  }
}
