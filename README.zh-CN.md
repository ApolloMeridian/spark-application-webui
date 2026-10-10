# Spark Control Center

简体中文 · [English](README.md)

Spark Control Center 是面向 Kubernetes 上 Apache Spark 作业的运维控制台，将作业生命周期管理、实时及历史资源指标、Driver UI、Executor 持久化日志、操作审计和权限控制整合为一套可独立部署的应用。

## 核心能力

- 查询指定命名空间中的 SparkApplication，展示状态、Owner、生命周期时间和资源使用情况。
- 查看 Driver/Executor 的申请量、实时用量、峰值、调度信息、Kubernetes 事件、日志、YAML 和可选时间范围的 Prometheus 历史曲线。
- 通过后端代理访问运行中的 Driver Spark UI；终态作业在启用 EventLog 后可跳转 Spark History Server。
- 从 Loki 查询持久化 Executor 日志，即使对应 Pod 已被删除仍可查看。
- 提交 SparkApplication YAML；终止 `RUNNING` 或卡住的 `SUBMITTED` 作业并保留失败 Driver Pod；删除终态记录。
- 提交前调用 Kubernetes 服务端 dry-run 校验，并对比原始 YAML 与服务端规范化清单。
- 从已清理服务端元数据和运行状态的清单克隆或重试作业。
- 在 PostgreSQL 中保存精简 YAML 模板，通过参数表单定义和校验参数，生成保留正确类型的最终清单，不保存 Secret 明文。[模板使用说明](docs/zh-CN/TEMPLATES-AND-ALERTS.md)。
- 使用可分享的 URL 组合筛选、服务端分页/排序及收藏，并在执行前预览受权限约束的批量终止或删除操作。
- 配置带版本记录的作业失败、长时间 Pending、资源压力、重复重试及可扩展诊断指纹告警（包含挂载故障），支持确认、静默、恢复及可选通用 Webhook 通知。
- 通过 Kubernetes Watch 和 SSE 实时推送变更，同时保留定时刷新作为恢复机制。
- 支持 PostgreSQL 本地账号和可选 OIDC/Keycloak 登录，并在前后端同时落实 `viewer`、`admin` 及用户级命名空间权限。
- 使用 PostgreSQL 保存用户、会话、生命周期快照、失败诊断和操作审计。
- 通过内置 Helm Chart 和命名空间级 RBAC 部署。
- 支持租户隔离的项目、版本化 DAG 工作流及 Spark YAML 任务，通过连接点拖拽连线、下拉菜单配置秒级带时区 Cron，或预览确认补数。[工作流使用说明](docs/zh-CN/WORKFLOWS.md)。
- 回溯或永久删除模板/工作流历史版本，按项目查看两类执行历史，并从 PostgreSQL 恢复后端重建前的编排状态。

## 功能界面

### 作业运维

| 运行总览 | 作业详情与资源历史 |
| --- | --- |
| ![运行总览](docs/images/overview-1440.png) | ![作业资源历史](docs/images/detail-1440.png) |

### 工作流与自动化

| 工作流与任务 | 定时调度 |
| --- | --- |
| ![工作流编辑器](docs/images/workflow-editor-1440.png) | ![定时任务配置](docs/images/workflow-schedule-1440.png) |

### 配置与治理

| 作业模板 | 操作审计 |
| --- | --- |
| ![作业模板](docs/images/templates-1440.png) | ![操作审计](docs/images/audit-1440.png) |

更多作业、日志、提交、告警和执行历史界面见[界面导览](docs/zh-CN/UI.md)。

## 使用文档

- [部署与配置](docs/zh-CN/DEPLOYMENT.md)：容器、Helm、认证、RBAC 与观测服务。
- [模板与告警](docs/zh-CN/TEMPLATES-AND-ALERTS.md)：YAML 参数、版本管理和诊断规则。
- [工作流与调度](docs/zh-CN/WORKFLOWS.md)：租户、任务依赖、Cron、补数和持久化。
- [安全策略](docs/zh-CN/SECURITY.md)：漏洞报告与部署责任。
- [对象存储](docs/zh-CN/OBJECT-STORAGE.md)：可选 S3/MinIO、GCS WIF 插件、目录权限与本地验证。

## 技术架构

- **前端：** React、TypeScript、Vite、Ant Design、ECharts
- **后端：** Go HTTP 服务
- **集成：** Kubernetes Spark Operator API、Prometheus、Loki、PostgreSQL、可选 OIDC
- **部署：** 非 root 容器、Nginx 同源 API 代理、Helm、最小范围 RBAC

浏览器只访问同源 `/api`，Kubernetes、Prometheus、Loki、PostgreSQL 和 OIDC 凭证全部保留在服务端。

## 本地开发

需要 Node.js 22+ 和 npm：

```bash
npm ci
npm run dev
```

本机 `npm run dev` 使用浏览器本地 Mock 数据和 Mock 登录；Helm 默认配置使用真实 API 与 PostgreSQL 登录。

运行前端验证：

```bash
npm test
npm run build
```

后端验证需要 Go 1.24+：

```bash
cd backend
go test ./...
go vet ./...
```

## 容器镜像

使用不含仓库前缀的名称构建：

```bash
docker build -t spark-control-center:1.5.1 .
docker build -f backend/Dockerfile -t spark-control-center-backend:1.5.1 .
docker build -f object-storage/Dockerfile -t spark-control-center-object-storage:1.5.1 .
```

[Releases](https://github.com/ApolloMeridian/spark-application-webui/releases) 包含三个镜像 TAR 和一个 Helm TGZ，不附带校验文件。镜像名不包含私有仓库前缀；对象存储镜像始终包含在发布附件中，插件默认关闭。

```bash
docker load -i spark-control-center-1.5.1.tar
docker load -i spark-control-center-backend-1.5.1.tar
docker load -i spark-control-center-object-storage-1.5.1.tar
```

## Helm 部署

Chart 默认 values 已是完整生产模板，启用真实 API 与 PostgreSQL 登录；对象存储和 OIDC 默认关闭。修改端点和已有 Secret 引用后，仅需一个 values 文件。Mock 模式需显式使用其专用示例：

```bash
helm show values ./spark-control-center-1.5.1.tgz > values-production.local.yaml

helm upgrade --install spark-console ./spark-control-center-1.5.1.tgz \
  --namespace spark-console \
  --create-namespace \
  -f values-production.local.yaml
```

请在私有 values 文件中配置镜像仓库、监控命名空间、PostgreSQL Secret 引用、Prometheus/Loki 地址、Ingress 和可选 OIDC。不要向代码库提交密码、Client Secret、访问令牌、证书、kubeconfig 或生产环境专属 values。

完整接口、安全模型、RBAC 行为、Keycloak 配置和高分辨率 Prometheus 配置见[部署与配置说明](docs/zh-CN/DEPLOYMENT.md)。

## 权限模型

- 新 `viewer` 账户默认进入最小权限 `viewer` 租户，仅开放总览、作业、日志和 Spark UI。
- 新 `admin` 账户默认进入全权限 `admin` 租户，并可管理用户、租户和审计。
- 管理员按租户配置模块只读/管理、命名空间与作业操作权限；普通账户可获得明确授予的操作权限。对象存储还需目录级授权。
- 升级保留现有用户与项目的租户归属；历史租户未明确配置前保留原权限。[权限说明](docs/zh-CN/WORKFLOWS.md)。

所有高权限操作都会由后端独立校验，前端隐藏按钮不作为授权依据。

## 安全说明

- 仓库和容器镜像不包含任何生产凭证。
- 密码使用 bcrypt 哈希；随机会话令牌仅以 SHA-256 哈希形式保存。
- 会话使用 HttpOnly/SameSite Cookie，状态变更接口执行同源校验。
- OIDC 使用 Authorization Code Flow + PKCE，并校验 issuer、签名、audience、过期时间、state 和 nonce。
- Kubernetes 权限默认限制在明确配置的命名空间内。

## 许可证

本项目使用 [Apache License 2.0](LICENSE)。
