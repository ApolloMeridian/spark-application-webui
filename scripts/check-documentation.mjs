import { readFileSync, existsSync } from "node:fs";

const read = (path) => readFileSync(path, "utf8").replace(/\r\n/g, "\n");
const apiSources = [
  "backend/internal/api/server.go",
  "backend/internal/api/workflows.go",
];
const routes = [
  ...new Set(
    apiSources.flatMap((p) =>
      [
        ...read(p).matchAll(
          /"((?:GET|POST|PUT|PATCH|DELETE|OPTIONS) \/[^"\n]+)"/g,
        ),
      ].map((x) => x[1]),
    ),
  ),
];
const pluginRoutes = [
  ...read("object-storage/internal/storage/server.go").matchAll(
    /"((?:GET|POST|PUT|PATCH|DELETE|OPTIONS) \/v1\/[^"\n]+)"/g,
  ),
].map((x) => x[1].replace(" /v1/", " /v1/object-storage/"));
const allRoutes = [...new Set([...routes, ...pluginRoutes])].sort(
  (a, b) =>
    a.split(" ")[1].localeCompare(b.split(" ")[1]) || a.localeCompare(b),
);
const envKeys = (paths) =>
  [
    ...new Set(
      paths.flatMap((p) =>
        [
          ...read(p).matchAll(
            /(?:env|durationEnv|boolEnv|intEnv|os\.Getenv|fallback)\(\s*"([A-Z][A-Z0-9_]+)"/g,
          ),
        ].map((x) => x[1]),
      ),
    ),
  ].sort();
const backendEnv = envKeys(["backend/internal/config/config.go"]);
const storageEnv = [
  ...new Set([
    ...envKeys([
      "object-storage/cmd/server/main.go",
      "object-storage/internal/storage/gcs.go",
    ]),
    "GOMEMLIMIT",
  ]),
].sort();
const tables = [
  ...new Set(
    [
      "backend/internal/store/postgres.go",
      "backend/internal/store/workflows.go",
      "backend/internal/store/tenant_scope.go",
      "backend/internal/store/alert_outbox.go",
      "backend/internal/store/live_applications.go",
      "backend/internal/store/auth_budget.go",
      "object-storage/internal/storage/store.go",
      "object-storage/internal/storage/limits.go",
    ].flatMap((p) =>
      [...read(p).matchAll(/CREATE TABLE IF NOT EXISTS ([a-z_]+)/g)].map(
        (x) => x[1],
      ),
    ),
  ),
].sort();
const group = (key, en) => {
  if (key.startsWith("OIDC_"))
    return en
      ? "OIDC; explicit callback required, secrets server-side"
      : "OIDC；要求固定回调，密钥仅保存在服务端";
  if (key.startsWith("AUTH_"))
    return en
      ? "Local login/session; initial admin only in local mode"
      : "本地登录/会话；仅本地模式需要初始管理员";
  if (key.startsWith("DATABASE_"))
    return en
      ? "PostgreSQL connection/pool; credentials from Secret"
      : "PostgreSQL 连接/连接池；凭证来自 Secret";
  if (key.startsWith("WORKFLOW"))
    return en
      ? "Workflow scheduler, timezone and concurrency"
      : "工作流调度器、时区与并发限制";
  if (key.startsWith("OBJECT_STORAGE_"))
    return en
      ? "Optional authenticated storage gateway"
      : "选配对象存储签名网关";
  if (key.startsWith("PROMETHEUS_") || key.startsWith("LOKI_"))
    return en
      ? "Observability endpoint/query settings"
      : "观测服务地址与查询配置";
  if (key.startsWith("OVERVIEW_"))
    return en ? "Overview resource pool settings" : "总览资源池设置";
  if (key.startsWith("ALERT_"))
    return en
      ? "Evaluation interval or operator-managed webhook"
      : "告警评估间隔或运维配置的 Webhook";
  if (
    key.startsWith("KUBERNETES_") ||
    key.startsWith("WATCH_") ||
    key.startsWith("SPARKAPPLICATION_")
  )
    return en
      ? "Kubernetes connection/namespace scope; ServiceAccount in cluster"
      : "Kubernetes 连接/命名空间范围；集群内使用 ServiceAccount";
  return en
    ? "Runtime setting; see configuration source and Helm values"
    : "运行配置；详见配置源码与 Helm values";
};
function reference(en) {
  return (
    (en
      ? `# API and environment reference

This inventory is generated from route registrations, configuration sources and migrations. Run \`npm run docs:check\` to detect drift. Helm values remain the deployment interface; backend defaults are defined in \`backend/internal/config/config.go\`.

## HTTP contract

The public \`/api\` prefix is omitted. Except health/metrics, login/callback and sandbox CORS preflight, routes require the PostgreSQL session cookie; sandboxed Spark UI subresources instead accept an application-scoped, session-bound read-only ticket. Namespace and tenant policy apply independently of the UI. Mutations require a valid same-origin Origin/Referer; non-browser clients without these headers must use JSON or \`X-Requested-With: SparkControlCenter\`. Explicit cross-origin requests are always denied. Upload parts use the custom header. Spark UI is GET-only and sandboxed.

JSON models: \`src/types.ts\`, \`src/workflows.ts\`, \`src/objectStorage.ts\` and \`backend/internal/domain\`. Run actions are \`pause\`, \`resume\`, \`cancel\`, \`rerun_failed\` and \`delete\`; template/workflow versions use their separate restore/delete routes. Limits, error behavior and storage tickets are described in the [deployment](DEPLOYMENT.md), [workflow](WORKFLOWS.md) and [storage](OBJECT-STORAGE.md) guides.

| Method | Registered path |
| --- | --- |
`
      : `# API 与环境变量参考

本清单从路由注册、配置源码和数据库迁移生成。运行 \`npm run docs:check\` 检查是否漂移。部署配置以 Helm values 为入口，后端默认值以 \`backend/internal/config/config.go\` 为准。

## HTTP 接口约定

以下省略公开路径前缀 \`/api\`。除健康/指标、登录/回调和隔离页面的 CORS 预检外，都要求 PostgreSQL 会话 Cookie；沙箱内 Spark UI 子资源改用绑定当前作业与父会话的只读票据。服务端独立校验命名空间与租户策略。写请求要求合法的同源 Origin/Referer；无这些头的非浏览器客户端须使用 JSON 或 \`X-Requested-With: SparkControlCenter\`，显式跨源请求始终拒绝。上传分片使用自定义头。Spark UI 仅允许 GET 并执行沙箱隔离。

JSON 模型见 \`src/types.ts\`、\`src/workflows.ts\`、\`src/objectStorage.ts\` 与 \`backend/internal/domain\`。实例操作为 \`pause\`、\`resume\`、\`cancel\`、\`rerun_failed\` 与 \`delete\`；模板/工作流历史版本使用独立回溯/删除接口。限制、错误处理及存储票据见[部署](DEPLOYMENT.md)、[工作流](WORKFLOWS.md)和[对象存储](OBJECT-STORAGE.md)指南。

| 方法 | 注册路径 |
| --- | --- |
`) +
    allRoutes
      .map((x) => {
        const i = x.indexOf(" ");
        return `| ${x.slice(0, i)} | \`${x.slice(i + 1)}\` |`;
      })
      .join("\n") +
    (en
      ? `

## Backend environment

Set \`PUBLIC_URL\` (Helm \`backend.publicURL\`) to the canonical origin behind TLS proxies; Helm can derive it from OIDC callback or Ingress host/TLS. Untrusted Forwarded headers are ignored. \`EXTERNAL_API_PREFIX\` defaults to \`/api\`. \`OIDC_REDIRECT_URL\` must be explicitly configured when OIDC is enabled.

| Variable | Purpose |
| --- | --- |
`
      : `

## 核心后端环境变量

TLS 代理后须配置规范外部 Origin：\`PUBLIC_URL\`（Helm \`backend.publicURL\`）；Chart 可从 OIDC 回调或 Ingress host/TLS 派生。不信任客户端 Forwarded 头。\`EXTERNAL_API_PREFIX\` 默认 \`/api\`；启用 OIDC 时必须显式配置 \`OIDC_REDIRECT_URL\`。

| 变量 | 用途 |
| --- | --- |
`) +
    backendEnv.map((k) => `| \`${k}\` | ${group(k, en)} |`).join("\n") +
    (en
      ? `

## Object-storage process environment

Connection types, endpoints, credential-file mounts, buckets, bootstrap grants and limits live in the JSON file selected by \`STORAGE_CONFIG_FILE\`, rendered from \`objectStorage\` Helm values. GCS uses the configured KSA WIF identity, not browser OIDC tokens. \`GOMEMLIMIT\` is a Go runtime tuning hint, not a container memory limit.

| Variable | Purpose |
| --- | --- |
`
      : `

## 对象存储进程环境变量

连接类型、服务地址、凭证文件挂载、桶、初始授权与限制来自 \`STORAGE_CONFIG_FILE\` 指定的 JSON 文件，由 Helm \`objectStorage\` 配置渲染。GCS 使用配置的 KSA WIF 身份，不使用浏览器 OIDC Token。\`GOMEMLIMIT\` 仅是 Go 运行时调优参数，不是容器内存限制。

| 变量 | 用途 |
| --- | --- |
`) +
    storageEnv.map((k) => `| \`${k}\` | ${group(k, en)} |`).join("\n") +
    (en
      ? `

## Persistent tables

Startup migrations require schema/table/index privileges. Back up PostgreSQL before upgrading; the optional plugin migrates its own tables in the same identity database. Object bytes and temporary parts are not stored in PostgreSQL.

`
      : `

## 持久化数据表

启动迁移需要 schema/建表/索引权限。升级前备份 PostgreSQL；选配插件在同一个身份数据库中管理自己的表。对象文件内容与临时分片不进入 PostgreSQL。

`) +
    tables.map((t) => `- \`${t}\``).join("\n") +
    "\n"
  );
}
for (const [locale, en] of [
  ["en", true],
  ["zh-CN", false],
]) {
  const path = `docs/${locale}/REFERENCE.md`;
  const expected = reference(en);
  if (process.argv.includes("--patch")) {
    const old = existsSync(path) ? read(path) : null;
    process.stdout.write(
      `*** Begin Patch\n*** ${old === null ? "Add" : "Update"} File: ${path}\n${old === null ? "" : "@@\n"}${
        old === null
          ? ""
          : old
              .trimEnd()
              .split("\n")
              .map((x) => "-" + x)
              .join("\n") + "\n"
      }${expected
        .trimEnd()
        .split("\n")
        .map((x) => "+" + x)
        .join("\n")}\n*** End Patch\n`,
    );
  } else if (!existsSync(path) || read(path) !== expected) {
    throw new Error(
      `${path} is out of date; regenerate with scripts/check-documentation.mjs --patch`,
    );
  }
}
if (!process.argv.includes("--patch"))
  console.log(
    `PASS: bilingual reference matches ${allRoutes.length} HTTP patterns, ${backendEnv.length} backend variables and ${storageEnv.length} plugin/runtime variables.`,
  );
