# API 与环境变量参考

本清单从路由注册、配置源码和数据库迁移生成。运行 `npm run docs:check` 检查是否漂移。部署配置以 Helm values 为入口，后端默认值以 `backend/internal/config/config.go` 为准。

## HTTP 接口约定

以下省略公开路径前缀 `/api`。除健康/指标、登录/回调和隔离页面的 CORS 预检外，都要求 PostgreSQL 会话 Cookie；沙箱内 Spark UI 子资源改用绑定当前作业与父会话的只读票据。服务端独立校验命名空间与租户策略。写请求要求合法的同源 Origin/Referer；无这些头的非浏览器客户端须使用 JSON 或 `X-Requested-With: SparkControlCenter`，显式跨源请求始终拒绝。上传分片使用自定义头。Spark UI 仅允许 GET 并执行沙箱隔离。

JSON 模型见 `src/types.ts`、`src/workflows.ts`、`src/objectStorage.ts` 与 `backend/internal/domain`。实例操作为 `pause`、`resume`、`cancel`、`rerun_failed` 与 `delete`；模板/工作流历史版本使用独立回溯/删除接口。限制、错误处理及存储票据见[部署](DEPLOYMENT.md)、[工作流](WORKFLOWS.md)和[对象存储](OBJECT-STORAGE.md)指南。

| 方法 | 注册路径 |
| --- | --- |
| GET | `/healthz` |
| GET | `/metrics` |
| GET | `/readyz` |
| GET | `/v1/alert-rules` |
| POST | `/v1/alert-rules` |
| DELETE | `/v1/alert-rules/{id}` |
| PATCH | `/v1/alert-rules/{id}` |
| GET | `/v1/alerts` |
| PATCH | `/v1/alerts/{id}` |
| GET | `/v1/applications` |
| POST | `/v1/applications/batch/execute` |
| POST | `/v1/applications/batch/preview` |
| GET | `/v1/audit` |
| POST | `/v1/auth/login` |
| POST | `/v1/auth/logout` |
| GET | `/v1/auth/me` |
| GET | `/v1/auth/oidc/callback` |
| GET | `/v1/auth/oidc/login` |
| GET | `/v1/dashboard/summary` |
| GET | `/v1/diagnostics/fingerprints` |
| GET | `/v1/favorites` |
| POST | `/v1/namespaces/{namespace}/applications` |
| DELETE | `/v1/namespaces/{namespace}/applications/{name}` |
| GET | `/v1/namespaces/{namespace}/applications/{name}` |
| GET | `/v1/namespaces/{namespace}/applications/{name}/driver/logs` |
| GET | `/v1/namespaces/{namespace}/applications/{name}/executors/{pod}/logs` |
| PUT | `/v1/namespaces/{namespace}/applications/{name}/favorite` |
| DELETE | `/v1/namespaces/{namespace}/applications/{name}/history` |
| POST | `/v1/namespaces/{namespace}/applications/{name}/kill` |
| GET | `/v1/namespaces/{namespace}/applications/{name}/metrics` |
| GET | `/v1/namespaces/{namespace}/applications/{name}/prepare` |
| GET | `/v1/namespaces/{namespace}/applications/{name}/spark-ui` |
| GET | `/v1/namespaces/{namespace}/applications/{name}/spark-ui/{path...}` |
| OPTIONS | `/v1/namespaces/{namespace}/applications/{name}/spark-ui/{path...}` |
| POST | `/v1/namespaces/{namespace}/applications/dry-run` |
| GET | `/v1/object-storage/connections` |
| POST | `/v1/object-storage/copy` |
| GET | `/v1/object-storage/download` |
| DELETE | `/v1/object-storage/objects` |
| GET | `/v1/object-storage/objects` |
| GET | `/v1/object-storage/operations` |
| GET | `/v1/object-storage/policies` |
| POST | `/v1/object-storage/policies` |
| DELETE | `/v1/object-storage/policies/{id}` |
| GET | `/v1/object-storage/text` |
| POST | `/v1/object-storage/text` |
| PUT | `/v1/object-storage/text` |
| DELETE | `/v1/object-storage/uploads` |
| GET | `/v1/object-storage/uploads` |
| POST | `/v1/object-storage/uploads` |
| POST | `/v1/object-storage/uploads/complete` |
| PUT | `/v1/object-storage/uploads/part` |
| GET | `/v1/profile` |
| PATCH | `/v1/profile` |
| GET | `/v1/projects` |
| POST | `/v1/projects` |
| DELETE | `/v1/projects/{project}` |
| PATCH | `/v1/projects/{project}` |
| GET | `/v1/projects/{project}/backfills` |
| POST | `/v1/projects/{project}/backfills/{id}/cancel` |
| GET | `/v1/projects/{project}/runs` |
| POST | `/v1/projects/{project}/runs/{id}/{action}` |
| GET | `/v1/projects/{project}/schedules` |
| POST | `/v1/projects/{project}/schedules` |
| DELETE | `/v1/projects/{project}/schedules/{id}` |
| PATCH | `/v1/projects/{project}/schedules/{id}` |
| GET | `/v1/projects/{project}/task-runs` |
| GET | `/v1/projects/{project}/workflows` |
| POST | `/v1/projects/{project}/workflows` |
| DELETE | `/v1/projects/{project}/workflows/{id}` |
| PATCH | `/v1/projects/{project}/workflows/{id}` |
| POST | `/v1/projects/{project}/workflows/{id}/backfill` |
| POST | `/v1/projects/{project}/workflows/{id}/dry-run` |
| POST | `/v1/projects/{project}/workflows/{id}/trigger` |
| GET | `/v1/projects/{project}/workflows/{id}/versions` |
| DELETE | `/v1/projects/{project}/workflows/{id}/versions/{version}` |
| POST | `/v1/projects/{project}/workflows/{id}/versions/{version}/restore` |
| GET | `/v1/stream` |
| GET | `/v1/templates` |
| POST | `/v1/templates` |
| DELETE | `/v1/templates/{id}` |
| GET | `/v1/templates/{id}` |
| PATCH | `/v1/templates/{id}` |
| POST | `/v1/templates/{id}/copy` |
| POST | `/v1/templates/{id}/render` |
| DELETE | `/v1/templates/{id}/versions/{version}` |
| POST | `/v1/templates/{id}/versions/{version}/restore` |
| GET | `/v1/tenants` |
| POST | `/v1/tenants` |
| PATCH | `/v1/tenants/{id}` |
| GET | `/v1/users` |
| POST | `/v1/users` |
| DELETE | `/v1/users/{id}` |
| PATCH | `/v1/users/{id}` |
| POST | `/v1/workflows/cron-preview` |
| GET | `/v1/workflows/scheduler` |

## 核心后端环境变量

TLS 代理后须配置规范外部 Origin：`PUBLIC_URL`（Helm `backend.publicURL`）；Chart 可从 OIDC 回调或 Ingress host/TLS 派生。不信任客户端 Forwarded 头。`EXTERNAL_API_PREFIX` 默认 `/api`；启用 OIDC 时必须显式配置 `OIDC_REDIRECT_URL`。

| 变量 | 用途 |
| --- | --- |
| `ALERT_EVALUATION_INTERVAL` | 告警评估间隔或运维配置的 Webhook |
| `ALERT_WEBHOOK_TIMEOUT` | 告警评估间隔或运维配置的 Webhook |
| `ALERT_WEBHOOK_URL` | 告警评估间隔或运维配置的 Webhook |
| `AUTH_BCRYPT_COST` | 本地登录/会话；仅本地模式需要初始管理员 |
| `AUTH_INITIAL_ADMIN_DISPLAY_NAME` | 本地登录/会话；仅本地模式需要初始管理员 |
| `AUTH_INITIAL_ADMIN_PASSWORD` | 本地登录/会话；仅本地模式需要初始管理员 |
| `AUTH_INITIAL_ADMIN_USERNAME` | 本地登录/会话；仅本地模式需要初始管理员 |
| `AUTH_MODE` | 本地登录/会话；仅本地模式需要初始管理员 |
| `AUTH_SESSION_TTL` | 本地登录/会话；仅本地模式需要初始管理员 |
| `DATABASE_HOST` | PostgreSQL 连接/连接池；凭证来自 Secret |
| `DATABASE_MAX_OPEN_CONNECTIONS` | PostgreSQL 连接/连接池；凭证来自 Secret |
| `DATABASE_NAME` | PostgreSQL 连接/连接池；凭证来自 Secret |
| `DATABASE_PASSWORD` | PostgreSQL 连接/连接池；凭证来自 Secret |
| `DATABASE_PORT` | PostgreSQL 连接/连接池；凭证来自 Secret |
| `DATABASE_SSLMODE` | PostgreSQL 连接/连接池；凭证来自 Secret |
| `DATABASE_URL` | PostgreSQL 连接/连接池；凭证来自 Secret |
| `DATABASE_USERNAME` | PostgreSQL 连接/连接池；凭证来自 Secret |
| `EXTERNAL_API_PREFIX` | 运行配置；详见配置源码与 Helm values |
| `HISTORY_DEFAULT_DAYS` | 运行配置；详见配置源码与 Helm values |
| `HTTP_ADDR` | 运行配置；详见配置源码与 Helm values |
| `KUBERNETES_API_URL` | Kubernetes 连接/命名空间范围；集群内使用 ServiceAccount |
| `KUBERNETES_BEARER_TOKEN` | Kubernetes 连接/命名空间范围；集群内使用 ServiceAccount |
| `KUBERNETES_CA_FILE` | Kubernetes 连接/命名空间范围；集群内使用 ServiceAccount |
| `KUBERNETES_CLUSTER_NAME` | Kubernetes 连接/命名空间范围；集群内使用 ServiceAccount |
| `KUBERNETES_REQUEST_TIMEOUT` | Kubernetes 连接/命名空间范围；集群内使用 ServiceAccount |
| `KUBERNETES_SERVICE_HOST` | Kubernetes 连接/命名空间范围；集群内使用 ServiceAccount |
| `KUBERNETES_SERVICE_PORT_HTTPS` | Kubernetes 连接/命名空间范围；集群内使用 ServiceAccount |
| `LOKI_MAX_ENTRIES` | 观测服务地址与查询配置 |
| `LOKI_QUERY_TIMEOUT` | 观测服务地址与查询配置 |
| `LOKI_URL` | 观测服务地址与查询配置 |
| `OBJECT_STORAGE_ENABLED` | 选配对象存储签名网关 |
| `OBJECT_STORAGE_SIGNING_KEY` | 选配对象存储签名网关 |
| `OBJECT_STORAGE_URL` | 选配对象存储签名网关 |
| `OIDC_ADMIN_GROUPS` | OIDC；要求固定回调，密钥仅保存在服务端 |
| `OIDC_AUTO_CREATE` | OIDC；要求固定回调，密钥仅保存在服务端 |
| `OIDC_CA_FILE` | OIDC；要求固定回调，密钥仅保存在服务端 |
| `OIDC_CLIENT_ID` | OIDC；要求固定回调，密钥仅保存在服务端 |
| `OIDC_CLIENT_SECRET` | OIDC；要求固定回调，密钥仅保存在服务端 |
| `OIDC_ENABLED` | OIDC；要求固定回调，密钥仅保存在服务端 |
| `OIDC_GROUPS_CLAIM` | OIDC；要求固定回调，密钥仅保存在服务端 |
| `OIDC_ISSUER_URL` | OIDC；要求固定回调，密钥仅保存在服务端 |
| `OIDC_REDIRECT_URL` | OIDC；要求固定回调，密钥仅保存在服务端 |
| `OIDC_SCOPES` | OIDC；要求固定回调，密钥仅保存在服务端 |
| `OIDC_STATE_TTL` | OIDC；要求固定回调，密钥仅保存在服务端 |
| `OIDC_USERNAME_CLAIM` | OIDC；要求固定回调，密钥仅保存在服务端 |
| `OVERVIEW_AUTO_DETECT_VIRTUAL_NODES` | 总览资源池设置 |
| `OVERVIEW_ELASTIC_NODE_LABELS` | 总览资源池设置 |
| `OVERVIEW_FIXED_NODE_LABELS` | 总览资源池设置 |
| `OVERVIEW_RESOURCE_POOLS_ENABLED` | 总览资源池设置 |
| `PROMETHEUS_CPU_RATE_WINDOW` | 观测服务地址与查询配置 |
| `PROMETHEUS_LOOKBACK` | 观测服务地址与查询配置 |
| `PROMETHEUS_QUERY_STEP` | 观测服务地址与查询配置 |
| `PROMETHEUS_QUERY_TIMEOUT` | 观测服务地址与查询配置 |
| `PROMETHEUS_URL` | 观测服务地址与查询配置 |
| `PUBLIC_URL` | 运行配置；详见配置源码与 Helm values |
| `SPARKAPPLICATION_API_VERSION` | Kubernetes 连接/命名空间范围；集群内使用 ServiceAccount |
| `WATCH_NAMESPACES` | Kubernetes 连接/命名空间范围；集群内使用 ServiceAccount |
| `WORKFLOWS_ENABLED` | 工作流调度器、时区与并发限制 |
| `WORKFLOW_MAX_ACTIVE_RUNS` | 工作流调度器、时区与并发限制 |
| `WORKFLOW_MAX_ACTIVE_TASKS` | 工作流调度器、时区与并发限制 |
| `WORKFLOW_MAX_CATCHUP_RUNS` | 工作流调度器、时区与并发限制 |
| `WORKFLOW_MAX_NODES` | 工作流调度器、时区与并发限制 |
| `WORKFLOW_SCHEDULER_ENABLED` | 工作流调度器、时区与并发限制 |
| `WORKFLOW_SCHEDULER_INTERVAL` | 工作流调度器、时区与并发限制 |
| `WORKFLOW_TIMEZONE` | 工作流调度器、时区与并发限制 |

## 对象存储进程环境变量

连接类型、服务地址、凭证文件挂载、桶、初始授权与限制来自 `STORAGE_CONFIG_FILE` 指定的 JSON 文件，由 Helm `objectStorage` 配置渲染。GCS 使用配置的 KSA WIF 身份，不使用浏览器 OIDC Token。`GOMEMLIMIT` 仅是 Go 运行时调优参数，不是容器内存限制。

| 变量 | 用途 |
| --- | --- |
| `DATABASE_HOST` | PostgreSQL 连接/连接池；凭证来自 Secret |
| `DATABASE_NAME` | PostgreSQL 连接/连接池；凭证来自 Secret |
| `DATABASE_PASSWORD` | PostgreSQL 连接/连接池；凭证来自 Secret |
| `DATABASE_PORT` | PostgreSQL 连接/连接池；凭证来自 Secret |
| `DATABASE_SSLMODE` | PostgreSQL 连接/连接池；凭证来自 Secret |
| `DATABASE_URL` | PostgreSQL 连接/连接池；凭证来自 Secret |
| `DATABASE_USERNAME` | PostgreSQL 连接/连接池；凭证来自 Secret |
| `GOMEMLIMIT` | 运行配置；详见配置源码与 Helm values |
| `HTTP_ADDR` | 运行配置；详见配置源码与 Helm values |
| `STORAGE_CONFIG_FILE` | 运行配置；详见配置源码与 Helm values |
| `STORAGE_SIGNING_KEY` | 运行配置；详见配置源码与 Helm values |

## 持久化数据表

启动迁移需要 schema/建表/索引权限。升级前备份 PostgreSQL；选配插件在同一个身份数据库中管理自己的表。对象文件内容与临时分片不进入 PostgreSQL。

- `alert_evaluation_lease`
- `alert_rule_versions`
- `alert_rules`
- `alert_webhook_outbox`
- `application_alerts`
- `application_collections`
- `application_favorites`
- `application_settings`
- `application_template_versions`
- `application_templates`
- `authentication_rate_windows`
- `live_applications`
- `local_user_sessions`
- `local_users`
- `object_storage_bootstrap`
- `object_storage_budget_leases`
- `object_storage_grants`
- `object_storage_limit_nodes`
- `object_storage_operations`
- `object_storage_rate_windows`
- `oidc_login_states`
- `operation_audit`
- `projects`
- `spark_application_history`
- `spark_ui_tickets`
- `tenants`
- `workflow_backfills`
- `workflow_effects`
- `workflow_resources`
- `workflow_runs`
- `workflow_scheduler_state`
- `workflow_schedules`
- `workflow_task_runs`
- `workflow_versions`
- `workflows`
