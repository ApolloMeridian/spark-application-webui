import assert from "node:assert/strict";
import { execFile, execFileSync } from "node:child_process";
import { promisify } from "node:util";
import { randomBytes } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";

const version = process.env.QA_VERSION || "1.5.0";
const prefix = `spark-storage-container-${randomBytes(4).toString("hex")}`;
const password = randomBytes(24).toString("hex");
const signing = randomBytes(32).toString("hex");
const containers = [];
const dockerAsync = promisify(execFile);
let network;
let cookie = "";
const docker = (...args) =>
  execFileSync("docker", args, {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();
const run = (...args) => {
  const id = docker(
    "run",
    "-d",
    "--label",
    `spark-console.qa=${prefix}`,
    ...args,
  );
  containers.push(id);
  return id;
};
const port = (id, p) =>
  JSON.parse(docker("inspect", id))[0].NetworkSettings.Ports[`${p}/tcp`][0]
    .HostPort;
const wait = async (fn) => {
  for (let i = 0; i < 60; i++) {
    try {
      const x = await fn();
      if (x) return x;
    } catch {}
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error("Readiness timeout");
};
const remove = (id) => {
  const c = JSON.parse(docker("inspect", id))[0];
  assert.equal(c.Config.Labels["spark-console.qa"], prefix);
  docker("rm", "-f", id);
  containers.splice(containers.indexOf(id), 1);
};
let origin = "";
async function api(path, method = "GET", body, headers = {}) {
  const response = await fetch(`${origin}/api/v1${path}`, {
    method,
    headers: {
      Cookie: cookie,
      ...(typeof body === "string"
        ? { "Content-Type": "application/json" }
        : {}),
      ...headers,
    },
    body,
  });
  const value = await response.json();
  if (!response.ok)
    throw new Error(`${response.status}: ${JSON.stringify(value)}`);
  return value;
}
const obj = (key, bucket = "spark-artifacts") =>
  `?${new URLSearchParams({ connection: "minio", bucket, key })}`;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// Node/Undici can unref a readiness socket while Docker is restarting. Keep the
// harness alive until finally runs, and bound each readiness request explicitly.
const keepAlive = setInterval(() => {}, 1000);
try {
  network = docker("network", "create", prefix);
  const db = run(
    "--name",
    `${prefix}-pg`,
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
  const kube = run(
    "--name",
    `${prefix}-kube`,
    "--network",
    network,
    "--network-alias",
    "qa-kube",
    "--mount",
    `type=bind,source=${resolve("scripts/fixtures/workflow-kubernetes.mjs")},target=/fixture.mjs,readonly`,
    "node:22-alpine",
    "node",
    "/fixture.mjs",
  );
  const minio = run(
    "--name",
    `${prefix}-minio`,
    "--network",
    network,
    "--network-alias",
    "qa-minio",
    "-e",
    "MINIO_ROOT_USER=storage-test",
    "-e",
    `MINIO_ROOT_PASSWORD=${password}`,
    "minio/minio:RELEASE.2025-04-22T22-12-26Z",
    "server",
    "/data",
  );
  const coreArgs = [
    "--name",
    `${prefix}-core`,
    "--network",
    network,
    "--network-alias",
    "qa-core",
    "-p",
    "127.0.0.1::8081",
    "-e",
    "KUBERNETES_API_URL=http://qa-kube:8080",
    "-e",
    "KUBERNETES_BEARER_TOKEN=qa-fixture",
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
    "WORKFLOW_SCHEDULER_ENABLED=false",
    "-e",
    "OBJECT_STORAGE_ENABLED=true",
    "-e",
    "OBJECT_STORAGE_URL=http://qa-storage:8082",
    "-e",
    `OBJECT_STORAGE_SIGNING_KEY=${signing}`,
    `spark-control-center-backend:${version}`,
  ];
  const core = run(...coreArgs);
  const corePort = port(core, 8081);
  await wait(
    async () =>
      (
        await fetch(`http://127.0.0.1:${corePort}/readyz`, {
          signal: AbortSignal.timeout(3000),
        })
      ).ok,
  );
  const fixtureDir = resolve(".qa", prefix);
  await mkdir(fixtureDir, { recursive: true });
  await writeFile(resolve(fixtureDir, "access-key"), "storage-test");
  await writeFile(resolve(fixtureDir, "secret-key"), password);
  const config = {
    connections: [
      {
        id: "minio",
        name: "MinIO integration",
        provider: "s3",
        buckets: ["spark-artifacts", "secondary-artifacts"],
        s3: {
          endpoint: "http://qa-minio:9000",
          region: "us-east-1",
          forcePathStyle: true,
          conditionalWrites: true,
          accessKeyFile: "/fixtures/access-key",
          secretKeyFile: "/fixtures/secret-key",
        },
      },
    ],
    bootstrapGrants: [
      {
        username: "qa-admin",
        tenantId: "admin",
        connection: "minio",
        bucket: "spark-artifacts",
        allBuckets: true,
        prefix: "team/a/",
        actions: [
          "list",
          "read",
          "download",
          "upload",
          "edit",
          "delete",
          "copy-source",
          "copy-destination",
        ],
      },
    ],
    requestsPerMinute: 600,
  };
  await writeFile(resolve(fixtureDir, "config.json"), JSON.stringify(config));
  const storageArgs = [
    "--name",
    `${prefix}-plugin`,
    "--network",
    network,
    "--network-alias",
    "qa-storage",
    "--memory",
    "256m",
    "-p",
    "127.0.0.1::8082",
    "--mount",
    `type=bind,source=${fixtureDir},target=/fixtures,readonly`,
    "-e",
    "STORAGE_CONFIG_FILE=/fixtures/config.json",
    "-e",
    `STORAGE_SIGNING_KEY=${signing}`,
    "-e",
    `DATABASE_URL=postgres://spark_test:${password}@qa-postgres:5432/spark_test?sslmode=disable`,
    "-e",
    "GOMEMLIMIT=180MiB",
    `spark-control-center-object-storage:${version}`,
  ];
  let plugin = run(...storageArgs);
  let pluginPort = port(plugin, 8082);
  await wait(
    async () => (await fetch(`http://127.0.0.1:${pluginPort}/readyz`)).ok,
  );
  // Fixture setup only: create a bucket using MinIO's in-container mc helper.
  docker(
    "exec",
    minio,
    "mc",
    "alias",
    "set",
    "qa",
    "http://127.0.0.1:9000",
    "storage-test",
    password,
  );
  docker("exec", minio, "mc", "mb", "qa/spark-artifacts");
  docker("exec", minio, "mc", "mb", "qa/secondary-artifacts");
  const nginx = `server {listen 8080;root /usr/share/nginx/html;location / {try_files $uri /index.html;}location /api/ {proxy_pass http://qa-core:8081/;proxy_http_version 1.1;proxy_request_buffering off;proxy_buffering off;client_max_body_size 17m;proxy_set_header Host $http_host;proxy_read_timeout 600s;}}
 `;
  await writeFile(resolve(fixtureDir, "nginx.conf"), nginx);
  const frontend = run(
    "--name",
    `${prefix}-web`,
    "--network",
    network,
    "-p",
    "127.0.0.1::8080",
    "--mount",
    `type=bind,source=${resolve(fixtureDir, "nginx.conf")},target=/etc/nginx/conf.d/default.conf,readonly`,
    `spark-control-center:${version}`,
  );
  origin = `http://127.0.0.1:${port(frontend, 8080)}`;
  await wait(async () => (await fetch(`${origin}/login`)).ok);
  const login = await fetch(`${origin}/api/v1/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username: "qa-admin", password }),
  });
  assert.equal(login.status, 200);
  cookie = login.headers.get("set-cookie").split(";")[0];
  const admin = await login.json();
  let info = await api("/object-storage/connections");
  assert.deepEqual(info.connections[0].roots.map((x) => x.bucket).sort(), [
    "secondary-artifacts",
    "spark-artifacts",
  ]);
  const minimalUser = await api(
    "/users",
    "POST",
    JSON.stringify({
      username: "qa-minimal",
      password,
      role: "viewer",
      displayName: "Minimal viewer",
    }),
  );
  assert.equal(minimalUser.tenantId, "viewer");
  const minimalLogin = await fetch(`${origin}/api/v1/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username: "qa-minimal", password }),
  });
  const minimalCookie = minimalLogin.headers.get("set-cookie").split(";")[0];
  const minimalSession = await (
    await fetch(`${origin}/api/v1/auth/me`, {
      headers: { Cookie: minimalCookie },
    })
  ).json();
  assert.equal(minimalSession.permissions.objectStorage, false);
  assert.equal(minimalSession.permissions.workflowsRead, false);
  for (const path of [
    "/object-storage/connections",
    "/templates",
    "/alerts",
    "/projects",
    "/diagnostics/fingerprints",
  ]) {
    assert.equal(
      (
        await fetch(`${origin}/api/v1${path}`, {
          headers: { Cookie: minimalCookie },
        })
      ).status,
      403,
      `minimum tenant allowed ${path}`,
    );
  }
  assert.equal(info.connections[0].roots[0].prefix, "team/a/");
  const first = await api(
    `/object-storage/uploads${obj("team/a/config.yaml")}`,
    "POST",
    JSON.stringify({ size: 7 }),
  );
  await api(
    "/object-storage/uploads/part?offset=0",
    "PUT",
    Buffer.from("initial"),
    { "X-Storage-Upload": first.ticket },
  );
  const created = await api(
    "/object-storage/uploads/complete",
    "POST",
    undefined,
    { "X-Storage-Upload": first.ticket },
  );
  const read = await api(`/object-storage/text${obj("team/a/config.yaml")}`);
  assert.equal(read.content, "initial");
  const edited = await api(
    `/object-storage/text${obj("team/a/config.yaml")}`,
    "PUT",
    JSON.stringify({ content: "updated", version: created.version }),
  );
  const conflict = await fetch(
    `${origin}/api/v1/object-storage/text${obj("team/a/config.yaml")}`,
    {
      method: "PUT",
      headers: { Cookie: cookie, "Content-Type": "application/json" },
      body: JSON.stringify({ content: "stale", version: created.version }),
    },
  );
  assert.equal(conflict.status, 409);
  const copied = await api(
    `/object-storage/copy${obj("team/a/config.yaml")}`,
    "POST",
    JSON.stringify({
      bucket: "spark-artifacts",
      key: "team/a/copied.yaml",
      version: edited.version,
    }),
  );
  const download = await fetch(
    `${origin}/api/v1/object-storage/download${obj("team/a/copied.yaml")}`,
    { headers: { Cookie: cookie } },
  );
  assert.equal(await download.text(), "updated");
  assert(download.headers.get("content-disposition").startsWith("attachment"));
  const hugeSize = 40 << 20;
  const huge = await api(
    `/object-storage/uploads${obj("team/a/large.txt")}`,
    "POST",
    JSON.stringify({ size: hugeSize }),
  );
  await api(
    "/object-storage/uploads/part?offset=0",
    "PUT",
    Buffer.alloc(8 << 20, 120),
    { "X-Storage-Upload": huge.ticket },
  );
  remove(plugin);
  plugin = run(...storageArgs);
  pluginPort = port(plugin, 8082);
  await wait(
    async () => (await fetch(`http://127.0.0.1:${pluginPort}/readyz`)).ok,
  );
  const status = await api("/object-storage/uploads", "GET", undefined, {
    "X-Storage-Upload": huge.ticket,
  });
  assert.equal(status.offset, 8 << 20);
  for (let offset = 8 << 20; offset < hugeSize; offset += 8 << 20)
    await api(
      `/object-storage/uploads/part?offset=${offset}`,
      "PUT",
      Buffer.alloc(8 << 20, 120),
      { "X-Storage-Upload": huge.ticket },
    );
  await api("/object-storage/uploads/complete", "POST", undefined, {
    "X-Storage-Upload": huge.ticket,
  });
  const preview = await fetch(
    `${origin}/api/v1/object-storage/text${obj("team/a/large.txt")}`,
    { headers: { Cookie: cookie } },
  );
  assert.equal(preview.status, 200);
  const previewText = await preview.json();
  assert.equal(previewText.truncated, true);
  assert.equal(previewText.readOnly, true);
  assert(Buffer.byteLength(previewText.content) <= 1048576);
  const largeSave = await fetch(
    `${origin}/api/v1/object-storage/text${obj("team/a/large.txt")}`,
    {
      method: "PUT",
      headers: {
        Cookie: cookie,
        "Content-Type": "application/json",
        Origin: origin,
      },
      body: JSON.stringify({
        content: "must not overwrite truncated object",
        version: previewText.object.version,
      }),
    },
  );
  assert.equal(largeSave.status, 413);
  const largeDownload = await fetch(
    `${origin}/api/v1/object-storage/download${obj("team/a/large.txt")}`,
    { headers: { Cookie: cookie } },
  );
  let total = 0;
  for await (const chunk of largeDownload.body) total += chunk.length;
  assert.equal(total, hugeSize);
  const usage = JSON.parse(
    docker("stats", "--no-stream", "--format", "{{json .}}", plugin),
  );
  console.log(
    `Storage memory snapshot under 256MiB container limit: ${usage.MemUsage}`,
  );
  // Four transfers exercise the configured slot limit without whole-file buffers.
  let sampling = true;
  const samples = [];
  const monitor = (async () => {
    while (sampling) {
      const result = await dockerAsync("docker", [
        "stats",
        "--no-stream",
        "--format",
        "{{json .}}",
        plugin,
      ]);
      samples.push(JSON.parse(result.stdout.trim()).MemUsage);
      await sleep(250);
    }
  })();
  try {
    const uploads = await Promise.all(
      Array.from({ length: 4 }, (_, i) =>
        api(
          `/object-storage/uploads${obj(`team/a/concurrent-${i}.bin`)}`,
          "POST",
          JSON.stringify({ size: hugeSize }),
        ),
      ),
    );
    for (let offset = 0; offset < hugeSize; offset += 8 << 20) {
      await Promise.all(
        uploads.map((u) =>
          api(
            `/object-storage/uploads/part?offset=${offset}`,
            "PUT",
            Buffer.alloc(8 << 20, 120),
            { "X-Storage-Upload": u.ticket },
          ),
        ),
      );
    }
    await Promise.all(
      uploads.map((u) =>
        api("/object-storage/uploads/complete", "POST", undefined, {
          "X-Storage-Upload": u.ticket,
        }),
      ),
    );
    await Promise.all(
      Array.from({ length: 4 }, async (_, i) => {
        const response = await fetch(
          `${origin}/api/v1/object-storage/download${obj(`team/a/concurrent-${i}.bin`)}`,
          { headers: { Cookie: cookie } },
        );
        assert.equal(response.status, 200);
        let bytes = 0;
        for await (const chunk of response.body) bytes += chunk.length;
        assert.equal(bytes, hugeSize);
      }),
    );
  } finally {
    sampling = false;
    await monitor;
  }
  const memoryMiB = (value) => {
    const match = value.match(/^([\d.]+)([KMGT]?)iB/);
    assert(match, `Unknown memory unit: ${value}`);
    return (
      Number(match[1]) *
      { "": 1 / 1048576, K: 1 / 1024, M: 1, G: 1024, T: 1048576 }[match[2]]
    );
  };
  assert(samples.length);
  assert(Math.max(...samples.map(memoryMiB)) < 256);
  assert.equal(JSON.parse(docker("inspect", plugin))[0].State.OOMKilled, false);
  console.log(
    `Maximum sampled memory during four concurrent 40MiB uploads/downloads: ${Math.max(...samples.map(memoryMiB)).toFixed(2)} MiB (256 MiB container limit).`,
  );
  const forbidden = await fetch(
    `${origin}/api/v1/object-storage/objects${obj("team/ab/")}`,
    { headers: { Cookie: cookie, "X-Spark-Storage-Identity": "forged" } },
  );
  assert.equal(forbidden.status, 403);
  const direct = await fetch(`http://127.0.0.1:${pluginPort}/v1/connections`, {
    headers: { "X-Spark-Storage-Identity": "forged" },
  });
  assert.equal(direct.status, 401);
  const viewer = await api(
    "/users",
    "POST",
    JSON.stringify({
      username: "qa-viewer",
      password,
      role: "viewer",
      displayName: "Viewer",
      tenantId: "admin",
    }),
  );
  await api(
    "/object-storage/policies",
    "POST",
    JSON.stringify({
      grant: {
        userId: viewer.id,
        connection: "minio",
        bucket: "spark-artifacts",
        prefix: "team/a/",
        actions: ["list", "read", "download"],
      },
      confirmRoot: false,
    }),
  );
  const viewerLogin = await fetch(`${origin}/api/v1/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username: "qa-viewer", password }),
  });
  const viewerCookie = viewerLogin.headers.get("set-cookie").split(";")[0];
  const readOnly = await fetch(
    `${origin}/api/v1/object-storage/objects${obj("team/a/config.yaml")}&version=${edited.version}`,
    { method: "DELETE", headers: { Cookie: viewerCookie } },
  );
  assert.equal(readOnly.status, 403);
  const records = await api("/object-storage/operations");
  assert.equal(records.filter((x) => x.action === "UPLOAD").length, 6);
  assert.equal(records.filter((x) => x.action === "DOWNLOAD").length, 6);
  await api(
    "/object-storage/policies",
    "POST",
    JSON.stringify({
      grant: {
        userId: viewer.id,
        connection: "minio",
        bucket: "spark-artifacts",
        prefix: "team/a/",
        actions: ["list", "read", "download", "upload", "edit"],
      },
      confirmRoot: false,
    }),
  );
  const viewerText = async (method, content, version = "") =>
    fetch(
      `${origin}/api/v1/object-storage/text${obj("team/a/viewer-created.json")}`,
      {
        method,
        headers: {
          Cookie: viewerCookie,
          "Content-Type": "application/json",
          Origin: origin,
        },
        body: JSON.stringify({ content, version }),
      },
    );
  const createdResponse = await viewerText("POST", '{"value":1}');
  assert.equal(createdResponse.status, 200);
  const createdText = await createdResponse.json();
  assert.equal((await viewerText("POST", "{}")).status, 409);
  assert.equal(
    (await viewerText("PUT", '{"value":2}', createdText.version)).status,
    200,
  );
  const viewerPolicy = await fetch(`${origin}/api/v1/object-storage/policies`, {
    method: "POST",
    headers: {
      Cookie: viewerCookie,
      "Content-Type": "application/json",
      Origin: origin,
    },
    body: "{}",
  });
  assert.equal(viewerPolicy.status, 403);
  const createdRead = await api(
    `/object-storage/text${obj("team/a/viewer-created.json")}`,
  );
  assert.equal(createdRead.content, '{"value":2}');
  await api(
    "/object-storage/policies",
    "POST",
    JSON.stringify({
      grant: {
        userId: viewer.id,
        connection: "minio",
        bucket: "spark-artifacts",
        prefix: "team/a/",
        actions: ["list", "read", "download"],
      },
      confirmRoot: false,
    }),
  );
  assert.equal(
    (await viewerText("PUT", "{}", createdRead.object.version)).status,
    403,
  );
  if (process.env.QA_STORAGE_VISUAL === "1") {
    const { stdout } = await dockerAsync(
      process.execPath,
      [resolve("scripts/visual-qa-object-storage.mjs")],
      {
        env: { ...process.env, QA_BASE_URL: origin },
        timeout: 240000,
      },
    );
    console.log(stdout.trim());
    const tenantVisual = await dockerAsync(
      process.execPath,
      [resolve("scripts/visual-qa-tenant-permissions.mjs")],
      { env: { ...process.env, QA_BASE_URL: origin }, timeout: 240000 },
    );
    console.log(tenantVisual.stdout.trim());
  }
  await api(
    `/object-storage/objects${obj("team/a/copied.yaml")}&version=${copied.version}`,
    "DELETE",
  );
  remove(plugin);
  assert.equal(
    (await fetch(`http://127.0.0.1:${corePort}/readyz`)).status,
    200,
  );
  assert.equal(
    (
      await fetch(`${origin}/api/v1/object-storage/connections`, {
        headers: { Cookie: cookie },
      })
    ).status,
    503,
  );
  assert.equal(
    (
      await fetch(`${origin}/api/v1/applications`, {
        headers: { Cookie: cookie },
      })
    ).status,
    200,
  );
  console.log(
    "PASS: three production images, core login/session, same-origin streaming, real MinIO upload/edit/copy/download/delete, 40MiB transfer, plugin replacement recovery, viewer/prefix enforcement and compact audits.",
  );
} catch (error) {
  for (const id of containers) {
    try {
      const meta = JSON.parse(docker("inspect", id))[0];
      if (meta.State.ExitCode !== 0 || meta.State.Status === "exited")
        console.error(docker("logs", "--tail", "8", id));
    } catch {}
  }
  throw error;
} finally {
  clearInterval(keepAlive);
  for (const id of [...containers].reverse()) remove(id);
  if (network) docker("network", "rm", network);
  // Fixture credentials are ephemeral. Leave only non-sensitive result artifacts.
  const { rm } = await import("node:fs/promises");
  const folder = resolve(".qa", prefix);
  assert(
    folder.startsWith(resolve(".qa") + "\\") ||
      folder.startsWith(resolve(".qa") + "/"),
  );
  await rm(folder, { recursive: true, force: true });
}
