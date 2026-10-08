# 工作流编排与定时调度（1.4.0）

[完整英文说明](WORKFLOWS.md)。首版仅支持 SparkApplication 节点，不执行任意 Bash、SQL 或 HTTP 脚本。

## 租户与项目

已有用户及 OIDC 自动创建的用户默认进入 `default` 租户。管理员在“用户管理 → 租户管理”创建/编辑租户，在用户编辑窗口绑定租户；OIDC 再次登录不会覆盖租户绑定。项目 → 工作流 → 任务按所属租户强制隔离，管理员也不绕过此边界。跨租户管理需显式切换自身绑定或使用对应租户的管理员。

普通用户只读其租户与授权命名空间内的配置和执行历史，可以打开作业详情、日志和 Spark UI。跨命名空间 DAG 采用整体权限检查。模板仍为按命名空间授权的共享库，外部 kubectl 作业沿用命名空间权限。

## 配置和执行

在项目内新建工作流，通过画布或任务表单配置依赖。画布可拖动布局；选择节点后点击“从选中节点连线”，再选择下游节点。所有上游成功后才能执行，独立分支可并行；上游最终失败会跳过下游。

节点可以使用独立 SparkApplication YAML 或指定模板版本，参数定义使用表单，运行值使用 YAML 键值格式。保存后产生包含整个 DAG、任务 YAML/参数的不可变版本。每次派发前都执行 Kubernetes Dry Run。建议 `restartPolicy: Never`，让工作流控制重试，避免和 Spark Operator 的内部重试叠加。

工作流和节点分别支持跨实例策略：并行（`parallel`）、串行等待（`serial_wait`）、串行抛弃（`serial_discard`）。这些策略控制实例重叠，不替代 DAG 的前置依赖。重试上限为 10 次，指数退避从 10 秒开始、最长 5 分钟。暂停只停止新任务派发，已运行任务继续监控；终止沿用 Driver deadline 逻辑并等待 Kubernetes 确认终态，保留 SparkApplication/Driver 用于诊断。集群不可用或 Driver 无法终止时保持“终止中”并显示原因，不误报完成。失败节点重跑使用原冻结配置和原调度基准，不重复成功节点。

项目内分别提供工作流与任务执行历史，可筛选并查看实例、节点、尝试次数、时间和错误。实例名包含年月日时分秒毫秒及唯一标识，任务关联作业详情。

## 名称和日期参数

默认开启作业名称唯一后缀：UTC 逻辑调度时间 + 随机任务 ID 的 64 位片段 + 尝试号，自动截断基础名称以满足 63 字符限制。重试产生新作业，旧失败记录保留。关闭后仅使用 Kubernetes CREATE，同名冲突失败，不会 apply 覆盖；因此重试场景建议保持开启。

```yaml
businessDate: $[yyyy-MM-dd-1]
previousHour: $[yyyy-MM-dd HH:mm:ss-1/24]
nextDay: $[yyyyMMdd+1]
runTimestamp: $[yyyyMMddHHmmssSSS]
```

格式支持 `yyyy/MM/dd/HH/mm/ss/SSS`。整数偏移按日历天计算，保留夏令时墙钟时间；分数按经过时长计算，`-1/24` 为前一小时，`-1/1440` 为前一分钟。以工作流时区解释基准；正常手动执行基准为当前时间，自动执行为计划时间，补数为各次对应时间，而不是实际出队时间。敏感信息必须引用已有 Kubernetes Secret，不要写进 YAML/参数。

## 定时与补数

Cron 使用五段：分钟、小时、日、月、星期，并显式保存 IANA 时区。日和星期都受限时采用标准 OR 语义；夏令时不存在的墙钟时间跳过，重复墙钟时间可能对应两个 UTC 执行时间。可设置有效开始/结束、启用状态并预览未来 10 次。

错过策略包括：跳过（早于两次扫描间隔加 1 秒的时间）、只补最近一次、有限补跑（超过 Helm 限额后跳过其余并审计）。工作流禁用时不积累补跑；调度修改从当前时间重新计算后续时间。补数须先预览后确认，范围最多 366 天、一次最多 200 个实例；再次显式确认会创建新的实例。

## 版本、持久化和审计

模板/工作流版本可查看、回溯、删除。回溯会创建递增的新版本，当前版本受到保护；删除历史版本会实际删除 PostgreSQL 中相应内容。已执行实例保留独立冻结快照，不受版本删除影响。删除执行历史会级联删除任务历史，但不会删除集群作业；删除项目/工作流须先显式清除对应执行历史。

配置、版本、调度时间、队列、实例、关联关系和审计均保存在 PostgreSQL，后端 Pod 无状态。多副本通过数据库事务锁协调派发；事务失效不能提交旧进度，Kubernetes 已接受的 CREATE 通过确定性名称和 task-instance annotation 认领恢复。管理员降权、禁用、租户迁移或命名空间权限撤销后，调度器会阻止自动派发并停止活跃实例。

人工变更、回溯/删除、执行控制和补数，以及自动触发、派发、阻断和完成均写操作审计；只读轮询不重复审计，也不记录密码或请求正文。

## Helm 与升级

```yaml
workflows:
  enabled: true
  timeZone: Asia/Shanghai
  maxNodes: 50
  scheduler:
    enabled: true
    scanInterval: 5s
    maxActiveRuns: 20
    maxActiveTasks: 50
    maxCatchupRuns: 20
```

前端入口从后端和工作流开关派生，纯前端 Mock 演示不模拟工作流。升级前备份 PostgreSQL，启动时增量迁移。关闭 scheduler 会暂停所有协调动作（包括排队中的终止），已提交 Kubernetes 作业仍继续运行；重新启用后恢复数据库状态。执行历史暂不自动清理。多副本必须共享同一数据库和 Kubernetes 上下文，回滚时不要让旧后端与新调度器同时运行。

```bash
docker build -t spark-control-center:1.4.0 .
docker build -f backend/Dockerfile -t spark-control-center-backend:1.4.0 .
helm upgrade --install spark-console ./charts/spark-control-center \
  --namespace spark-console --create-namespace \
  -f charts/spark-control-center/examples/values-production.yaml \
  -f values.local.yaml
```

验证使用真实 PostgreSQL、生产容器与 Kubernetes HTTP 测试替身，不能代替你实际 Spark Operator/准入策略的测试。启用生产定时任务前，请先在非生产命名空间运行一个小型工作流。
