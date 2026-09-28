# Spark Control Center 版本路线图

本文档是项目版本规划的唯一权威来源。开始新一轮开发前，先读取本文档并选择状态为“下一版本”的阶段；完成发布后，在同一个提交中更新版本状态、实际发布日期和后续目标。

## 当前版本

- 当前稳定版本：`1.1.0`
- 下一目标版本：`1.2.0`
- 版本策略：遵循 Semantic Versioning；补丁版修复兼容性问题，次版本增加向后兼容能力，主版本允许引入架构或配置不兼容变更。

## 版本阶段

| 版本 | 阶段 | 状态 | 目标 |
| --- | --- | --- | --- |
| `1.0.1` | 生产基线 | 已发布 | 完成 Kubernetes、Prometheus、Loki、PostgreSQL、OIDC、Helm 和核心作业操作闭环。 |
| `1.1.0` | 安全操作与实时体验 | 已发布 | 增加 Dry Run、命名空间权限、Watch/SSE、克隆/重试、生命周期快照与失败诊断。 |
| `1.2.0` | 作业模板与批量运维 | 下一版本 | 降低重复提交成本，完善作业检索、复用和批量管理能力。 |
| `1.3.0` | 主动告警与诊断增强 | 规划中 | 从被动查看升级为主动发现、聚合和通知异常。 |
| `1.4.0` | 容量与资源效率 | 规划中 | 提供容量趋势、资源利用率和 Spark 配置优化建议。 |
| `2.0.0` | 多集群与企业治理 | 长期规划 | 支持多 Kubernetes 集群、集中策略和更完整的租户治理。 |

## `1.0.1`：生产基线（已发布）

- 真实查询和操作 Kubernetes SparkApplication。
- Prometheus 实时及历史资源指标。
- Loki Executor 持久化日志。
- PostgreSQL 用户、会话、审计和历史统计。
- 本地账号及可选 OIDC/Keycloak 登录。
- Driver Spark UI 同源代理及 Spark History Server 跳转。
- Helm Chart、命名空间级 RBAC、Ingress、证书及外部依赖配置。

## `1.1.0`：安全操作与实时体验（已发布）

- Kubernetes Server-Side Dry Run 和原始/规范化 YAML 对比。
- 用户级命名空间授权，后端同时约束读取、操作、审计和实时事件。
- Kubernetes Watch 通过 SSE 推送到前端，并保留轮询恢复机制。
- SparkApplication 克隆和终态重试，自动清理服务端字段及运行状态。
- PostgreSQL 生命周期快照和常见镜像、调度、挂载、OOM、Executor、RBAC 故障诊断。

## `1.2.0`：作业模板与批量运维（下一版本）

计划功能：

- PostgreSQL 持久化 SparkApplication 模板，支持创建、编辑、复制、禁用和版本记录。
- 从现有作业或 YAML 保存模板；提交时填写参数并生成最终清单。
- 模板参数支持必填、默认值、类型、枚举和正则校验，不在模板中保存 Secret 明文。
- 作业列表增加组合筛选、可分享的 URL 查询条件、服务端分页和排序。
- 增加受权限约束的批量终止与批量删除预览，逐项展示可执行性并记录独立审计结果。
- 增加收藏筛选和常用模板入口；保持中英文及常见笔记本宽度可用。

验收条件：

- viewer 只能读取授权命名空间内允许查看的模板和作业，不能保存、提交或批量操作。
- admin 只能在自己的命名空间范围内管理模板和执行作业操作。
- 所有提交仍必须经过 Kubernetes Dry Run；批量操作必须二次确认且不可绕过后端权限检查。
- 数据库迁移可从 `1.1.0` 原地升级，已有用户、会话、审计和快照保持可用。
- 前后端测试、Go vet、生产构建、Helm lint/template、Docker 构建及视觉回归全部通过。

明确不包含：

- 定时调度和工作流编排。
- Secret 内容托管。
- 多集群管理。

## `1.3.0`：主动告警与诊断增强（规划中）

- 可配置的失败、长时间 Pending、资源异常和重复重试规则。
- 告警中心，支持确认、静默、恢复和关联 SparkApplication 生命周期。
- 通用 Webhook 通知接口，为 Slack、Teams 等集成保留适配边界。
- 基于 Kubernetes Event、Driver/Executor 日志和 Prometheus 指标的故障指纹聚合。
- 诊断规则版本化，并展示证据、置信级别和建议操作，不自动执行破坏性修复。

## `1.4.0`：容量与资源效率（规划中）

- Namespace、Owner、镜像和时间范围维度的 CPU/内存趋势。
- Request、实际使用量、峰值和浪费率对比。
- Driver/Executor 规格与实例数调整建议，并显示推导依据。
- 可配置保留周期和 CSV 导出。
- 总览页增加容量风险、热点节点池和资源效率视图。

## `2.0.0`：多集群与企业治理（长期规划）

- 多 Kubernetes 集群注册、健康状态和上下文切换。
- 集群与命名空间双层权限、OIDC 组映射和策略管理。
- 每集群独立的 Prometheus、Loki、History Server 和凭证引用。
- 高可用 Watch/事件处理和跨副本一致性。
- 审计保留策略、合规导出和升级兼容性工具。

该版本允许配置结构或数据库模型发生不兼容变化，但必须提供迁移说明和回滚路径。

## 每次版本开发流程

1. 读取本文件，确认“下一目标版本”、范围、验收条件和明确不包含项。
2. 将目标阶段状态改为“开发中”，必要时补充已经确认的需求，但不要把临时环境信息写入路线图。
3. 实现功能并同步 API 类型、数据库迁移、前端、后端、Helm、测试和双语文档。
4. 更新所有版本入口：
   - `package.json` 与 `package-lock.json`
   - `charts/spark-control-center/Chart.yaml`
   - `charts/spark-control-center/values.yaml`
   - `charts/spark-control-center/examples/*.yaml`
   - README、部署文档和 `.github/release-notes/<version>.md`
5. 执行前端测试及生产构建、Go test/vet、Helm lint/template/package、两个 Docker 镜像构建和桌面视觉回归。
6. 扫描凭证、私有地址和个人信息；生产 Secret、证书、私有 values、kubeconfig 和镜像 tar 不得提交。
7. 提交并推送 `main`，创建 `v<version>` 标签，确认 CI 与 Release 资产成功生成。
8. 将阶段状态改为“已发布”，记录实际结果，并把下一阶段标记为“下一版本”。

## 路线图维护规则

- 已发布版本只补充事实，不回写尚未实现的能力。
- 新需求先归入已有阶段；只有范围明显独立时才新增版本。
- 紧急缺陷使用 `1.1.x`、`1.2.x` 等补丁版本，不改变后续次版本目标。
- 未经明确批准，不把规划中的功能提前并入当前版本。
- 本文件不得包含真实域名、账号、密码、Client Secret、Token、证书、集群地址或个人信息。
- 本地草稿使用 `ROADMAP.local.md`，该文件由 `.gitignore` 排除。
