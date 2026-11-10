# 容器与 Helm 部署

## 构建镜像

从代码库根目录执行，镜像名称不包含私有仓库前缀：

```bash
export DOCKER_BUILDKIT=1
docker build -t spark-control-center:1.5.4 .
docker build -f backend/Dockerfile -t spark-control-center-backend:1.5.4 .
```

如已缓存 Nginx 基础镜像，可先生成前端产物再使用预构建 Dockerfile：

```bash
npm ci
npm run build
docker build -f Dockerfile.prebuilt -t spark-control-center:1.5.4 .
```

前端监听 `8080`，以 UID 101 运行，支持只读根文件系统，并提供 `/healthz`。Helm 在运行时挂载 `/usr/share/nginx/html/config/config.js`，无需为不同环境重建镜像。

后端监听 `8081`，以 UID 65532 运行。启动时连接 PostgreSQL 并执行增量迁移，数据库账号需具有建表、建索引和修改表结构的权限。备份数据库后再升级；所有副本必须共享同一数据库。

## Helm 安装与升级

Chart 默认是生产 API 与 PostgreSQL 本地登录模式。只有显式使用以下示例才运行演示模式：

```bash
helm upgrade --install spark-console ./charts/spark-control-center \
  --namespace spark-console --create-namespace \
  -f ./charts/spark-control-center/examples/values-mock.yaml
```

真实部署先复制生产配置示例，再以私有配置覆盖环境参数：

```bash
helm show values ./spark-control-center-1.5.4.tgz > values-production.local.yaml
helm upgrade --install spark-console ./charts/spark-control-center \
  --namespace spark-console --create-namespace \
  -f values-production.local.yaml
```

配置镜像地址、命名空间、Ingress、PostgreSQL Secret、Prometheus/Loki 及认证服务。Chart 不安装 PostgreSQL、Prometheus、Loki、Keycloak 或 Spark Operator，需要提前准备这些服务。不要提交实际密码、令牌、证书、kubeconfig 或生产专属配置。

启动迁移维护用户/会话、租户隔离的模板与告警、审计/历史、工作流/调度/实例/补数及告警协调状态；选配存储进程在同一身份数据库中维护授权、操作与初始授权记录。数据库账号须有 schema/建表/索引权限，升级前备份。完整清单见 [API、环境变量与数据表参考](REFERENCE.md)。

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

CPU 使用 `container_cpu_usage_seconds_total`，内存使用 `container_memory_working_set_bytes`。查询失败时保留 Kubernetes 作业信息，缺少采样时显示“—”。历史查询支持所选 RFC3339 时间范围，并根据 SparkApplication 状态保留历史 Pod 名称。

### 运行总览资源池

固定池显示本池已调度 Spark Pod 的申请和使用量，以及固定节点的 Kubernetes `status.allocatable` 合计。进度条表示固定池的 Spark 请求占可分配容量的比例。弹性池显示本池已调度 Spark Pod 的申请和使用量，空闲时两者为 0。等待分配节点的 Pod 继续显示在调度分布中。

默认通过 TKE 的 `node.kubernetes.io/instance-type=eklet`、ACK 的 `type=virtual-kubelet` 和已知虚拟节点污点识别弹性节点。GKE 弹性池可使用 `cloud.google.com/gke-nodepool` 匹配池名。以下配置适用于三种平台的自定义 Node 标签。

```yaml
overview:
  resourcePools:
    enabled: true
    autoDetectVirtualNodes: false
    fixedMatchLabels:
      capacity.example.com/pool: fixed
    elasticMatchLabels:
      - capacity.example.com/pool: elastic
```

在节点池配置中设置相应的 **Kubernetes Node 标签**。同一规则内所有标签都要匹配，`elasticMatchLabels` 任意一项命中即可。弹性规则优先，`fixedMatchLabels: {}` 会把其余节点归入固定池。启用 `autoDetectVirtualNodes` 时，内置识别与自定义弹性规则同时生效。

后端每 30 秒刷新节点清单。Chart 为后端 ServiceAccount 创建独立的 `nodes/list` 只读授权。如果 `rbac.create: false`，请在自管 RBAC 中授予该权限。节点清单不可用时，总览显示作业资源汇总。无需资源池数据的部署可以设置 `overview.resourcePools.enabled: false`。

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

以下仅为速览，全部变量（含外部 Kubernetes 连接、数据库 URL/连接池、告警、工作流和对象存储进程）见[完整参考](REFERENCE.md)。

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

下表仅列常用接口，模板/收藏/告警/工作流/定时/补数/对象存储和 Driver 日志范围接口的完整注册清单见[接口参考](REFERENCE.md)。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/v1/dashboard/summary?from=...&to=...` | 实时汇总与数据库历史提交/失败统计 |
| GET | `/v1/applications` | 作业列表，支持筛选、排序与分页 |
| GET | `/v1/stream` | Kubernetes Watch 驱动的 SSE 变更流 |
| GET | `/v1/namespaces/{ns}/applications/{name}` | 完整详情 |
| POST | `/v1/namespaces/{ns}/applications/dry-run` | 具备提交权限时预检 `{yaml}` |
| GET | `/v1/namespaces/{ns}/applications/{name}/prepare?mode=clone\|retry` | 清理后的克隆/重试清单 |
| POST | `/v1/namespaces/{ns}/applications` | 具备提交权限时创建 `{yaml}` |
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

密码采用 bcrypt，随机会话令牌只存 SHA-256 哈希。Cookie 使用 HttpOnly/SameSite，写请求执行严格同源检查。TLS 代理后应配置 `backend.publicURL`，或由明确的 OIDC 回调 / Ingress host 与 TLS 派生；不再信任客户端转发头，HTTPS 外部地址会生成 Secure Cookie。启用 OIDC 时必须设置与身份提供商完全一致的固定 `oidc.redirectURL`。升级行为与限制见[安全加固说明](HARDENING.md)。

## 作业与观测行为

### Driver 日志窗口

Driver 日志页使用独立查询，不受详情快照中最后 5000 行诊断日志的限制。支持“最新日志”和“指定行范围”，可前后翻段，每次最多 10000 行 / 4 MiB。Kubernetes 行偏移通过流式跳过前面内容实现，不将整份日志加载到内存；较晚的行段查询可能更慢，仍受请求超时约束。行号相对于 Kubernetes 当前保留的容器日志，不是永久不变的完整作业行号。搜索、级别筛选只作用于当前窗口，保留原行号。

已轮转或删除的 Driver 日志可选择“Loki 持久化日志”，按时间范围及正倒序查询；Alloy 必须采集 namespace、pod、spark_role="driver" 标签。Loki 每次仍受配置的条目上限限制，提示达到上限时请缩小时间范围。未被 Kubernetes 保留、也未被 Loki 采集的日志无法恢复。日志查询沿用命名空间/租户权限，不产生操作审计。

终止不是删除：先把 Spark 重启策略设为 `Never`，再给 Driver Pod 设置已过期的 `activeDeadlineSeconds`。Kubelet 终止容器并保留失败 Pod，事件和日志仍可诊断；关联 Executor 清除。删除终态记录时使用前台传播，由所有者关系清理 Driver 等资源。具体协调时序取决于 Spark Operator。

提交最大 1 MiB，要求匹配配置的 API 版本、Kind 和授权命名空间。服务端清理运行状态和托管元数据，执行 `dryRun=All` 后展示规范化差异，使用 CREATE 创建而非覆盖；注解 `spark-control-center.io/submitted-by` 保存实际提交者，外部提交显示 `unknown`。

运行中 Spark UI 代理使用 Driver Service 和端口，重写重定向、集群内部绝对地址和根路径资源链接，无需 `pods/exec` 或本地端口转发。终态作业仅在 History Server 已启用且 YAML 包含开启的 EventLog 配置时跳转历史地址。

Executor 日志通过 Loki `query_range` 查询，Pod 删除后仍可按时间范围和正/倒序查看。PostgreSQL 保存已删除作业的生命周期快照与诊断；SSE 之外保留定时刷新以恢复断线。`/healthz` 检查进程存活，`/readyz` 检查 Kubernetes 和 PostgreSQL。

## 1.5.3 租户权限与生产默认配置

Chart 默认使用真实后端与 PostgreSQL 登录，不再默认 Mock。在单个 values 文件中指定初始管理员密码 Secret 和数据库 Secret。仅使用 OIDC 时设置 runtimeConfig.auth.mode=oidc、启用 oidc 并配置客户端 Secret/回调，此模式不要求初始本地管理员 Secret。存储仍需显式开启。

新管理员/普通账户默认进入对应内置租户；升级保留已有账户与项目所属租户。在用户管理中配置租户权限，API 与调度器从 PostgreSQL 读取策略。参见[租户权限](WORKFLOWS.md)与[对象存储](OBJECT-STORAGE.md)。发布包含无私有前缀的前端/后端/存储三个 TAR 和 Helm TGZ，不生成校验文件。

## 1.5.3 作业实例与终止操作

详情地址采用 `/applications/<namespace>/<Kubernetes UID>`。Spark ID 在启动前可能为空，Operator 重试时也可能变化，因此作为运行信息展示，不用作资源身份。旧名称链接会解析到当前或最新可见实例。CR 删除后保留已观测的 PostgreSQL 快照；删除后重建的同名资源在列表中分别显示。只有分页作业浏览器包含历史实例，总览资源统计及调度器仍使用 Kubernetes 实时状态。

历史 Loki 和 Prometheus 查询限制在该实例的生命周期内。旧 UID 不能终止、删除或代理同名新实例的 Spark UI。已归档实例的 Kubernetes 日志请求会明确提示不可用，可选 Loki 或已保存的诊断尾部。采集器无法补回从未观测过的资源、已经轮转的日志或已过期指标。

终止时使用 UID/资源版本条件原子写入 `spark-control-center.io/stop-requested-at` 与 `restartPolicy.type: Never`，随后设置已验证 Driver 的 active deadline 并清理 Executor。后端副本会恢复未完成的停止请求，继续终止该 UID 下重新提交的 Driver。控制台保留 CR，不通过删除 Driver 来终止；旧版 Spark Operator 可能在规格变更触发失效重提时自行清理原 Driver，应使用 Loki 持久化日志。所有后端都不可用时，已在途的重提可能运行至恢复协调。克隆/重试会移除停止标记并创建新资源；删除后重建的同名资源不会继承旧 UID 的停止请求。
