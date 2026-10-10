import { execFileSync, spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { resolve } from "node:path";
import { existsSync } from "node:fs";
import { ensureMinIOFixture } from "./minio-fixture.mjs";

const minioImage = ensureMinIOFixture();
const prefix = `spark-storage-qa-${randomBytes(4).toString("hex")}`;
const password = randomBytes(24).toString("hex");
const ids = [];
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
  ids.push(id);
  return id;
};
const wait = async (fn) => {
  for (let i = 0; i < 60; i++) {
    try {
      if (await fn()) return;
    } catch {}
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error("Readiness timeout");
};
const go =
  process.platform === "win32" && existsSync(".tools/go/bin/go.exe")
    ? resolve(".tools/go/bin/go.exe")
    : "go";
const command = (cwd, args, env) =>
  new Promise((resolvePromise, reject) => {
    const child = spawn(go, args, {
      cwd,
      env: {
        ...process.env,
        GOCACHE: resolve(".tools/gocache"),
        GOPATH: resolve(".tools/gopath"),
        ...env,
      },
      stdio: "inherit",
    });
    child.on("error", reject);
    child.on("close", (n) =>
      n === 0
        ? resolvePromise()
        : reject(new Error(`Go ${args.join(" ")} failed (${n})`)),
    );
  });
try {
  const db = run(
    "--name",
    `${prefix}-pg`,
    "-p",
    "127.0.0.1::5432",
    "-e",
    "POSTGRES_USER=spark_test",
    "-e",
    `POSTGRES_PASSWORD=${password}`,
    "-e",
    "POSTGRES_DB=spark_test",
    "postgres:15-alpine",
  );
  const minio = run(
    "--name",
    `${prefix}-minio`,
    "-p",
    "127.0.0.1::9000",
    "-e",
    "MINIO_ROOT_USER=storage-test",
    "-e",
    `MINIO_ROOT_PASSWORD=${password}`,
    minioImage,
    "server",
    "/data",
  );
  const pgPort = JSON.parse(docker("inspect", db))[0].NetworkSettings.Ports[
    "5432/tcp"
  ][0].HostPort;
  const s3Port = JSON.parse(docker("inspect", minio))[0].NetworkSettings.Ports[
    "9000/tcp"
  ][0].HostPort;
  await wait(() =>
    docker("exec", db, "pg_isready", "-U", "spark_test").includes(
      "accepting connections",
    ),
  );
  await wait(
    async () =>
      (await fetch(`http://127.0.0.1:${s3Port}/minio/health/live`)).ok,
  );
  const env = {
    TEST_POSTGRES_DSN: `postgres://spark_test:${password}@127.0.0.1:${pgPort}/spark_test?sslmode=disable`,
    TEST_S3_ENDPOINT: `http://127.0.0.1:${s3Port}`,
    TEST_S3_ACCESS_KEY: "storage-test",
    TEST_S3_SECRET_KEY: password,
  };
  await command(resolve("backend"), ["test", "-count=1", "./..."], env);
  await command(resolve("backend"), ["vet", "./..."], env);
  await command(
    resolve("object-storage"),
    ["test", "-count=1", "-v", "./..."],
    env,
  );
  await command(resolve("object-storage"), ["vet", "./..."], env);
  console.log(
    "PASS: core regression, PostgreSQL storage authorization/audits and real MinIO conditional/multipart/copy/recovery tests.",
  );
} finally {
  for (const id of ids.reverse()) {
    const container = JSON.parse(docker("inspect", id))[0];
    if (container.Config.Labels["spark-console.qa"] !== prefix)
      throw new Error("Refusing to remove non-test container");
    docker("rm", "-f", id);
  }
}
