# 容器与 Helm 部署

## 构建镜像

从代码库根目录执行，镜像名称不包含私有仓库前缀：

```bash
docker build -t spark-control-center:1.5.1 .
docker build -f backend/Dockerfile -t spark-control-center-backend:1.5.1 .
```

如已缓存 Nginx 基础镜像，可先生成前端产物再使用预构建 Dockerfile：

```bash
npm ci
npm run build
docker build -f Dockerfile.prebuilt -t spark-control-center:1.5.1 .
```

前端监听 `8080`，以 UID 101 运行，支持只读根文件系统，并提供 `/healthz`。Helm 在运行时挂载 `/usr/share/nginx/html/config/config.js`，无需为不同环境重建镜像。

后端监听 `8081`，以 UID 65532 运行。启动时连接 PostgreSQL 并执行增量迁移，数据库账号需具有建表、建索引和修改表结构的权限。备份数据库后再升级；所有副本必须共享同一数据库。

## Helm 安装与升级

默认配置运行前端演示模式：

```bash
helm upgrade --install spark-console ./charts/spark-control-center \
  --namespace spark-console --create-namespace \
  -f ./charts/spark-control-center/examples/values-mock.yaml
```

真实部署先复制生产配置示例，再以私有配置覆盖环境参数：

```bash
helm show values ./spark-control-center-1.5.1.tgz > values-production.local.yaml
helm upgrade --install spark-console ./charts/spark-control-center \
  --namespace spark-console --create-namespace \
  -f values-production.local.yaml
```

配置镜像地址、命名空间、Ingress、PostgreSQL Secret、Prometheus/Loki 及认证服务。Chart 不安装 PostgreSQL、Prometheus、Loki、Keycloak 或 Spark Operator，需要提前准备这些服务。不要提交实际密码、令牌、证书、kubeconfig 或生产专属配置。

## OIDC 与本地认证

打包默认配置引用已有 PostgreSQL Secret 的 `username`、`password` 字段，使用 `runtimeConfig.auth.mode=local`。配置自己的数据库 Secret 引用及初始管理员密码 Secret。仅使用 OIDC 时将 mode 改为 `oidc` 并启用、配置提供方；OIDC 用户与会话保存在 PostgreSQL，该模式不要求初始本地管理员。

配置集中在服务端 `oidc` 和 `oidcSecret` 字段，浏览器入口由 Chart 自动派生。Keycloak 中创建启用客户端认证、标准流程的 OpenID Connect 客户端 `spark-control-center`：

- 有效回调地址：`https://spark-console.example.com/api/v1/auth/oidc/callback`。
- Web 来源：`https://spark-console.example.com`。
- 配置 Group Membership 映射，将 `groups` 写入 ID Token。
- `oidc.scopes` 可设为 `[openid, profile, email, groups]`；`groupsClaim` 和 `usernameClaim` 指定声明字段。
- `oidc.adminGroups` 中的组映射为管理员，其他用户只能是只读用户。

Keycloak 有效回调地址必须与 `oidc.redirectURL` 完全一致。`spark-console-oidc` 是 Kubernetes Secret 名称，不是客户端密钥本身：

```bash
kubectl -n spark-console create secret generic spark-console-oidc \
  --from-literal=client-secret='REPLACE_WITH_KEYCLOAK_CLIENT_SECRET' \
  --dry-run=client -o yaml | kubectl apply -f -
```

OIDC 使用授权码流程和 PKCE，验证 issuer、签名、audience、到期时间、state、nonce；不存储 Keycloak 令牌。账号以 `(issuer, subject)` 标识，开启 `autoCreate` 时首次登录自动创建。

本地账号模式设为 `runtimeConfig.auth.mode=local`，关闭 `oidc.enabled`，配置 `backend.auth.initialAdmin.existingSecret` 或 `createSecret=true`。只有本地模式要求初始管理员密码；本地模式也可启用可选 OIDC 入口。

## 内部 CA 证书

ConfigMap 不能跨命名空间挂载，需要将证书复制到控制台命名空间：

```bash
oidc_ca_file="$(mktemp)"
trap 'rm -f "${oidc_ca_file}"' EXIT
kubectl -n identity get configmap oidc-ca -o json | \
  jq -r '.data | to_entries[0].value' > "${oidc_ca_file}"
kubectl -n spark-console create configmap oidc-ca \
  --from-file=ca.crt="${oidc_ca_file}" \
  --dry-run=client -o yaml | kubectl apply -f -
```

配置 `oidc.ca.existingConfigMap: oidc-ca`、`oidc.ca.key: ca.crt`。Chart 只读挂载 PEM 并设置 `OIDC_CA_FILE`，无需手工配置额外卷。不要通过跳过 TLS 校验解决证书问题。

## 短任务的 Prometheus 分辨率

查询默认使用一小时历史、15 秒步长和一分钟 CPU 速率窗口。Prometheus 的 kubelet/cAdvisor 抓取间隔也应为 15 秒，降低查询步长不会补出未采集的样本。

采用 `prometheus-community/prometheus` 时，保留已安装 Chart 版本并应用示例：

```bash
PROMETHEUS_CHART_VERSION="$(helm list -n prometheus -o json | jq -r '.[] | select(.name == "prometheus") | .chart | sub("^prometheus-"; "")')"
helm upgrade prometheus prometheus-community/prometheus \
  --namespace prometheus --version "${PROMETHEUS_CHART_VERSION}" \
  --reuse-values \
  -f ./charts/spark-control-center/examples/prometheus-values-high-resolution.yaml
kubectl -n prometheus get configmap prometheus-server \
  -o jsonpath='{.data.prometheus\.yml}' | \
  grep -n -E 'scrape_interval|job_name|cadvisor'
```

CPU 使用 `container_cpu_usage_seconds_total`，内存使用 `container_memory_working_set_bytes`，集群容量使用 `kube_node_status_allocatable`。查询失败时保留 Kubernetes 作业信息，指标显示缺失而非假定为零。历史查询支持所选 RFC3339 时间范围，并根据 SparkApplication 状态保留历史 Pod 名称。

## 配置边界与环境变量

| 配置 | 用途 |
| --- | --- |
| `runtimeConfig.*` | 浏览器可见的公共配置，不能保存密钥 |
| `runtimeConfig.timeZone` | IANA 时区如 `Asia/Shanghai`；留空使用浏览器时区 |
| `database.credentials.*` | PostgreSQL Secret 引用，仅后端使用 |
| `backend.auth.initialAdmin.*` | 本地模式初始管理员配置 |
| `oidc` / `oidcSecret` | OIDC 服务端参数与客户端密钥引用 |
| `rbac.namespaces` / `rbac.clusterWide` | 默认使用指定命名空间的 Role，不默认授予集群权限 |
| `runtimeConfig.historyServer` | 终态作业 History Server 链接开关与基础地址 |
| `workflows.*` | 工作流、调度扫描、并发与时区，详见[工作流说明](WORKFLOWS.md) |

后端注入的主要变量：

- Kubernetes：`KUBERNETES_CLUSTER_NAME`、`WATCH_NAMESPACES`、`SPARKAPPLICATION_API_VERSION` 和 ServiceAccount 令牌。
- Prometheus：`PROMETHEUS_URL`、`PROMETHEUS_QUERY_TIMEOUT`、`PROMETHEUS_LOOKBACK`、`PROMETHEUS_QUERY_STEP`、`PROMETHEUS_CPU_RATE_WINDOW`。
- Loki：`LOKI_URL`、`LOKI_QUERY_TIMEOUT`、`LOKI_MAX_ENTRIES`；Executor 查询按 Alloy 标签 `namespace`、`pod`、`spark_role=executor` 关联。
- 历史：`HISTORY_DEFAULT_DAYS`；浏览器刷新使用 `runtimeConfig.dashboard.refreshIntervalSeconds`。
- 数据库：`DATABASE_HOST`、`DATABASE_PORT`、`DATABASE_NAME`、`DATABASE_SSLMODE`、`DATABASE_USERNAME`、`DATABASE_PASSWORD`。
- 认证：`AUTH_MODE`、`AUTH_SESSION_TTL`、`AUTH_BCRYPT_COST`；本地模式额外使用 `AUTH_INITIAL_ADMIN_USERNAME`、`AUTH_INITIAL_ADMIN_DISPLAY_NAME` 和 Secret 中的 `AUTH_INITIAL_ADMIN_PASSWORD`。
- OIDC：`OIDC_ENABLED`、`OIDC_ISSUER_URL`、`OIDC_CLIENT_ID`、`OIDC_CLIENT_SECRET`、`OIDC_SCOPES`、`OIDC_USERNAME_CLAIM`、`OIDC_GROUPS_CLAIM`、`OIDC_ADMIN_GROUPS`、`OIDC_AUTO_CREATE`、`OIDC_REDIRECT_URL`、`OIDC_STATE_TTL`、`OIDC_CA_FILE`。

`backend.extraEnv`、`extraVolumes`、`extraVolumeMounts` 可用于额外身份和信任配置。客户端密钥、密码和服务令牌不进入浏览器配置。

## API 与权限

浏览器仅访问同源 `/api`，下表省略该前缀。JSON 结构见 `src/types.ts` 与 `src/workflows.ts`。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/v1/dashboard/summary?from=...&to=...` | 实时汇总与数据库历史提交/失败统计 |
| GET | `/v1/applications` | 作业列表，支持筛选、排序与分页 |
| GET | `/v1/stream` | Kubernetes Watch 驱动的 SSE 变更流 |
| GET | `/v1/namespaces/{ns}/applications/{name}` | 完整详情 |
| POST | `/v1/namespaces/{ns}/applications/dry-run` | 管理员预检 `{yaml}` |
| GET | `/v1/namespaces/{ns}/applications/{name}/prepare?mode=clone\|retry` | 清理后的克隆/重试清单 |
| POST | `/v1/namespaces/{ns}/applications` | 管理员提交 `{yaml}` |
| POST | `/v1/namespaces/{ns}/applications/{name}/kill` | 终止运行或已提交作业 `{reason}` |
| DELETE | `/v1/namespaces/{ns}/applications/{name}` | 删除终态记录 `{reason}` |
| GET | `/v1/namespaces/{ns}/applications/{name}/executors/{pod}/logs` | Loki 日志，支持 `from/to/direction/limit` |
| GET | `/v1/namespaces/{ns}/applications/{name}/spark-ui/{path...}` | Driver UI 同源代理 |
| GET | `/v1/audit` | 管理员操作审计 |
| POST | `/v1/auth/login` | 本地用户名密码登录 |
| GET | `/v1/auth/oidc/login`、`/v1/auth/oidc/callback` | OIDC 发起与回调 |
| GET | `/v1/auth/me` | 当前登录用户 |
| POST | `/v1/auth/logout` | 注销服务端会话 |
| GET/PATCH | `/v1/profile` | 管理自己的资料与本地密码 |
| GET/POST | `/v1/users` | 管理员查询/创建用户 |
| PATCH/DELETE | `/v1/users/{id}` | 修改、重置密码、禁用或删除用户 |
| GET | `/healthz`、`/readyz`、`/metrics` | 存活、依赖就绪与可选指标 |

数据接口要求有效的 PostgreSQL 会话，后端独立检查角色、命名空间与租户。命名空间分配留空表示所有 Helm 指定的命名空间，不能越过该范围。审计使用真实登录用户名，不信任客户端传入操作人。最后一名有效管理员不能被禁用、降权或删除，也不能删除自己的账号。

密码采用 bcrypt，随机会话令牌只存 SHA-256 哈希。Cookie 使用 HttpOnly/SameSite，状态变更请求执行同源校验。对外部署需在 Ingress 终止 TLS，前端保留原始转发协议以便后端设置 Secure Cookie。

## 作业与观测行为

终止不是删除：先把 Spark 重启策略设为 `Never`，再给 Driver Pod 设置已过期的 `activeDeadlineSeconds`。Kubelet 终止容器并保留失败 Pod，事件和日志仍可诊断；关联 Executor 清除。删除终态记录时使用前台传播，由所有者关系清理 Driver 等资源。具体协调时序取决于 Spark Operator。

提交最大 1 MiB，要求匹配配置的 API 版本、Kind 和授权命名空间。服务端清理运行状态和托管元数据，执行 `dryRun=All` 后展示规范化差异，使用 CREATE 创建而非覆盖；注解 `spark-control-center.io/submitted-by` 保存实际提交者，外部提交显示 `unknown`。

运行中 Spark UI 代理使用 Driver Service 和端口，重写重定向、集群内部绝对地址和根路径资源链接，无需 `pods/exec` 或本地端口转发。终态作业仅在 History Server 已启用且 YAML 包含开启的 EventLog 配置时跳转历史地址。

Executor 日志通过 Loki `query_range` 查询，Pod 删除后仍可按时间范围和正/倒序查看。PostgreSQL 保存已删除作业的生命周期快照与诊断；SSE 之外保留定时刷新以恢复断线。`/healthz` 检查进程存活，`/readyz` 检查 Kubernetes 和 PostgreSQL。

## 1.5.1 租户权限与生产默认配置

Chart 默认使用真实后端与 PostgreSQL 登录，不再默认 Mock。在单个 values 文件中指定初始管理员密码 Secret 和数据库 Secret。仅使用 OIDC 时设置 runtimeConfig.auth.mode=oidc、启用 oidc 并配置客户端 Secret/回调，此模式不要求初始本地管理员 Secret。存储仍需显式开启。

新管理员/普通账户默认进入对应内置租户；升级保留已有账户与项目所属租户。在用户管理中配置租户权限，API 与调度器从 PostgreSQL 读取策略。参见[租户权限](WORKFLOWS.md)与[对象存储](OBJECT-STORAGE.md)。发布包含无私有前缀的前端/后端/存储三个 TAR 和 Helm TGZ，不生成校验文件。
