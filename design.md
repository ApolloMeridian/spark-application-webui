可以做，而且**难度没有你想象得大**。以你现在已经具备的 Spark Operator、Prometheus、Keycloak、PostgreSQL 基础，我会把它定义为一个“Spark on Kubernetes Job Console”，而不是重新开发 YARN ResourceManager。

我建议采用：

**React / Vue UI + Go Backend + PostgreSQL + Kubernetes API + Prometheus + Keycloak**

其中 Kubernetes 是事实数据源，Prometheus 是资源使用数据源，PostgreSQL 是历史索引和审计数据源。不要让 PostgreSQL 承担时序监控数据。

### 1. 整体架构

```text
                         ┌─────────────────────┐
                         │      Keycloak       │
                         │  OIDC / RBAC / SSO  │
                         └──────────┬──────────┘
                                    │
                                    ▼
┌───────────────┐          ┌──────────────────────┐
│    Browser    │─────────▶│ Spark Job Console UI │
│ React / Vue   │          └──────────┬───────────┘
└───────────────┘                     │ REST / WebSocket
                                      ▼
                            ┌──────────────────────┐
                            │   Console Backend    │
                            │        Go            │
                            └───┬────┬────┬───────┘
                                │    │    │
                 ┌──────────────┘    │    └──────────────┐
                 ▼                   ▼                   ▼
        ┌────────────────┐   ┌───────────────┐   ┌────────────────┐
        │ Kubernetes API │   │  Prometheus   │   │   PostgreSQL   │
        ├────────────────┤   ├───────────────┤   ├────────────────┤
        │ SparkApplication│  │ CPU usage     │   │ Job history    │
        │ Driver Pod      │  │ Memory usage  │   │ User / owner   │
        │ Executor Pods   │  │ Historical TS │   │ Kill audit     │
        │ Events          │  │ Node metrics  │   │ Status history │
        └────────────────┘   └───────────────┘   └────────────────┘
```

Spark Operator 本身已经把很多你需要的信息放到了 `SparkApplication.status`：

```yaml
status:
  applicationState:
    state: RUNNING

  driverInfo:
    podName: xxx-driver

  executorState:
    xxx-exec-1: RUNNING
    xxx-exec-2: RUNNING
```

官方 CRD 当前就定义了 `applicationState`、`driverInfo`、`executorState`，状态包括 `RUNNING / COMPLETED / FAILED / SUBMISSION_FAILED / SUBMITTED / ...`，所以你实际上不需要自己推导 Spark 作业状态。([GitHub][1])

---

## 2. UI 可以做到非常接近 YARN

首页我建议直接做成这种表格：

| Application        | Owner  | Status      | Duration | Driver    | Executors | CPU Request | CPU Used | Memory Request | Memory Used | Action        |
| ------------------ | ------ | ----------- | -------: | --------- | --------: | ----------: | -------: | -------------: | ----------: | ------------- |
| parquet-to-iceberg | Jeremy | 🟢 RUNNING  |      18m | 2C / 10Gi |         6 |         26C |    17.3C |          190Gi |       143Gi | Detail / Kill |
| can-merge-hourly   | system | ✅ COMPLETED |      24m | 2C / 10Gi |         4 |         18C |        - |          130Gi |           - | Detail        |
| qb-parser          | Jeremy | 🔴 FAILED   |       3m | 2C / 10Gi |         6 |         26C |        - |          190Gi |           - | Detail        |

颜色可以统一：

```text
RUNNING             绿色
COMPLETED           蓝色 / 深绿色
FAILED              红色
SUBMISSION_FAILED   红色
PENDING/SUBMITTED   黄色
FAILING             橙色
UNKNOWN             灰色
```

再加上顶部汇总：

```text
Spark Applications

RUNNING       12
PENDING        3
FAILED         2
COMPLETED     38

CPU Requested      216 / 288 cores
CPU Used           137 / 288 cores

Memory Requested   1.68 / 2.4 TiB
Memory Used        1.12 / 2.4 TiB
```

这基本就是一个更适合 Kubernetes Spark 的 YARN ResourceManager UI。

---

# 3. Driver / Executor 资源怎么获得

这里有一个非常重要的设计原则：

> **申请资源看 Kubernetes Pod，实际资源使用看 Prometheus。**

不要直接用：

```yaml
spec:
  driver:
    cores: 2
    memory: 8g
    memoryOverhead: 2g

  executor:
    cores: 4
    memory: 24g
    memoryOverhead: 6g
```

作为最终的 Kubernetes Request。

因为最终 Pod 可能还有：

* memoryOverhead
* init container
* sidecar
* CPU request / limit
* admission webhook 修改
* 动态资源配置

所以后端应该读取：

```text
Pod
└── spec.containers[]
        resources:
          requests:
            cpu:
            memory:
          limits:
            cpu:
            memory:
```

这样显示出来的是 Kubernetes Scheduler **真正看到的资源请求**。

尤其你现在正在调查：

> Spark executor 是否真的被调度到 GKE 弹性节点。

这个 UI 后面甚至可以增加：

```text
Executor

Pod                         CPU Req   CPU Use   Mem Req   Mem Use   Node             NodePool
-------------------------------------------------------------------------------------------------
xxx-exec-1                  4         2.7       30Gi      21Gi      node-001         baseline
xxx-exec-2                  4         3.2       30Gi      25Gi      node-002         baseline
xxx-exec-3                  4         3.8       30Gi      27Gi      auto-node-01     autoscale
xxx-exec-4                  4         3.4       30Gi      24Gi      auto-node-02     autoscale
```

对你现在这个 GKE 基线池 + 弹性池方案会特别有价值。

---

# 4. 实际 CPU / Memory 使用量直接查 Prometheus

你完全不需要自己采集。

你的 Backend 调 Prometheus HTTP API 即可。Prometheus 本身支持 `/api/v1/query` 和 `/api/v1/query_range`。([Prometheus][2])

CPU：

```promql
sum by (pod) (
  rate(
    container_cpu_usage_seconds_total{
      namespace="spark",
      container!="",
      container!="POD"
    }[2m]
  )
)
```

Memory：

```promql
sum by (pod) (
  container_memory_working_set_bytes{
    namespace="spark",
    container!="",
    container!="POD"
  }
)
```

页面展示：

```text
Driver

CPU
Request     2 cores
Current     1.37 cores
Peak        1.91 cores

Memory
Request     10 GiB
Current     7.8 GiB
Peak        9.1 GiB
```

Executor 则可以聚合：

```text
Executors: 6

CPU
Requested       24 cores
Current         17.8 cores
Peak            22.4 cores

Memory
Requested       180 GiB
Current         143 GiB
Peak            172 GiB
```

点击展开后再看到每个 executor。

---

# 5. SparkApplication 与 Pod 的关联其实已经很好解决

Spark Operator 创建的 Pod 会带上类似：

```text
sparkoperator.k8s.io/app-name
sparkoperator.k8s.io/submission-id
sparkoperator.k8s.io/launched-by-spark-operator
```

这些 label 就可以把：

```text
SparkApplication

        │
        ├─ Driver Pod
        │
        ├─ Executor-1
        ├─ Executor-2
        ├─ Executor-3
        └─ Executor-N
```

关联起来。当前 Operator 的提交逻辑也确实会给 driver 和 executor 添加这些标签。([GitHub][3])

所以数据库里根本不需要自己维护复杂的映射关系。

---

# 6. Kill 操作尤其简单

前端：

```text
┌──────────────────────────┐
│ Kill Spark Application?  │
│                          │
│ parquet-to-iceberg       │
│                          │
│ [Cancel]       [Kill]    │
└──────────────────────────┘
```

后端**不要执行 shell：**

```bash
kubectl delete sparkapp xxx
```

而是直接调 Kubernetes API：

```text
DELETE
/apis/sparkoperator.k8s.io/v1beta2
/namespaces/spark
/sparkapplications/parquet-to-iceberg
```

它在语义上就相当于：

```bash
kubectl -n spark delete sparkapp parquet-to-iceberg
```

Spark Operator 官方明确说明：

> 删除一个正在运行的 SparkApplication，会 kill 对应应用，并删除或垃圾回收相关 Kubernetes 资源。([GitHub][4])

因此不需要分别：

```text
delete driver
delete executor
delete service
```

---

# 7. Backend 权限要非常克制

给你的 Console Backend 一个独立 ServiceAccount：

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: spark-console
  namespace: spark-console
```

权限只需要类似：

```yaml
rules:
- apiGroups:
    - sparkoperator.k8s.io
  resources:
    - sparkapplications
  verbs:
    - get
    - list
    - watch
    - delete

- apiGroups:
    - ""
  resources:
    - pods
    - pods/log
    - events
  verbs:
    - get
    - list
    - watch
```

Kubernetes CRD 本身走 Kubernetes 原生 authentication、authorization 和 audit，因此这里可以完全使用 RBAC 管控。([Kubernetes][5])

这比：

```text
后端拥有 cluster-admin
```

安全得多。

---

# 8. Keycloak 非常适合直接复用

不用在 PostgreSQL 重新维护：

```text
users
password
password_hash
login_sessions
```

让 Keycloak 做：

```text
Authentication
        │
        ▼
Keycloak OIDC
        │
        ▼
JWT
        │
        ▼
Spark Console Backend
```

可以定义三个角色：

```text
spark-viewer
    查看 Application
    查看 Driver/Executor
    查看资源
    查看日志

spark-operator
    viewer +
    Kill Application

spark-admin
    operator +
    跨 Namespace
    系统配置
```

Keycloak 原生支持 OIDC 和 Web Application 的 Authorization Code Flow，很适合这种门户。([Keycloak][6])

甚至可以继续沿用你现有的 `k8s-infra` realm。

---

# 9. PostgreSQL 到底存什么

这里我强烈建议保持数据库简单。

### application

```text
spark_application
-----------------------------
id
cluster
namespace
name
uid
submission_id
spark_application_id

owner
team

state

created_at
started_at
finished_at

driver_pod
executor_count

cpu_request
memory_request

error_message

created_at
updated_at
```

### 状态历史

```text
application_state_history
------------------------------
application_id

old_state
new_state

timestamp
message
```

例如：

```text
12:00:01  NEW       -> SUBMITTED
12:00:05  SUBMITTED -> RUNNING
12:42:17  RUNNING   -> COMPLETED
```

### 操作审计

```text
application_operation
------------------------------
id
application_id

operator
operation

timestamp
result
message
```

例如：

```text
jeremy
KILL
2026-09-15 14:32:18
SUCCESS
```

这个表很重要。

因为不能出现：

> Spark 作业为什么没了不知道。

至少应该能追踪：

```text
谁
什么时候
Kill 了哪个作业
为什么
结果是什么
```

---

# 10. 不建议把 CPU / Memory 时序数据写 PostgreSQL

例如不要每 10 秒写：

```text
executor_metrics

time
pod
cpu
memory
```

否则几年以后就是一个劣化版 Prometheus。

正确模型是：

```text
PG
│
├── Application metadata
├── State history
├── User operation
└── Audit

Prometheus
│
├── CPU
├── Memory
├── Network
├── disk
└── historical time series
```

UI Detail 页面需要曲线时：

```text
UI
 ↓
Backend
 ↓
Prometheus query_range
```

实时查询即可。

---

# 11. 最关键的组件其实是 Watcher

不要：

```text
每 10 秒 kubectl get sparkapp
```

应该使用 Kubernetes Watch / Informer。

比如：

```text
SparkApplication ADDED
         │
         ▼
       PG insert

SparkApplication MODIFIED
         │
         ▼
       PG update
         │
         ├── RUNNING
         ├── COMPLETED
         └── FAILED

SparkApplication DELETED
         │
         ▼
     mark deleted
```

Spark Operator 自己本身也是这种事件驱动模型：controller watch `SparkApplication` 和 Spark Pod，再据此更新状态。([GitHub][7])

所以你的 Console 本质上只是另一个**只读为主的 observer/controller**。

---

# 12. 一个 Application Detail 页面可以做到很实用

例如：

```text
parquet-to-iceberg-test                       RUNNING

Overview
----------------------------------------------------
Namespace          spark
Owner              Jeremy
Started            2026-09-15 14:21:03
Duration           00:32:17
Spark ID           spark-a182...
Submission ID      e76c...
Image              xxx/spark:3.5.0

Resource
----------------------------------------------------
                  Request       Current      Peak

Driver CPU         2             1.6          1.9
Driver Memory      10 GiB        8.1 GiB      9.2 GiB

Executor CPU       24            17.3         22.1
Executor Memory    180 GiB       142 GiB      174 GiB


Executors
----------------------------------------------------
Pod           State      CPU       Memory      Node
exec-1        RUNNING    3.1/4     26/30Gi     node-01
exec-2        RUNNING    2.8/4     24/30Gi     node-02
exec-3        RUNNING    3.4/4     28/30Gi     auto-01
...


[ Spark UI ] [ Driver Logs ] [ Events ] [ YAML ] [ Kill ]
```

这个页面对实际运维的价值已经相当高。

---

# 13. 我还建议加入一个很有用的 Scheduling 页面

尤其针对你现在的 GKE 场景：

```text
Scheduling
────────────────────────────────────────────────────────

Driver
node:       gke-baseline-xxx
nodepool:   fr-gcy-qb-bigdata-pool-2026091

Executors

Baseline Pool           4
Autoscale Pool          2

┌─────────────────────────────────────────────────────┐
│ Baseline ████████████████████ 4                    │
│ Autoscale ██████████           2                    │
└─────────────────────────────────────────────────────┘
```

再增加：

```text
Pending Reason
```

读取 Pod Conditions / Events：

```text
0/9 nodes are available:
3 Insufficient memory
6 node(s) didn't match Pod's node affinity/selector
```

这恰好能够解决你最近遇到的：

> memory requests 瞬间超过 100%，任务失败，但事后很难回溯为什么没有进入弹性节点。

这种信息如果存进 PG：

```text
pod_scheduling_event
```

以后就可以直接追溯。

---

# 14. 技术选型

我比较推荐：

```text
Frontend
React
TypeScript
Ant Design
ECharts

Backend
Go
Gin / Echo
client-go
controller-runtime

Database
PostgreSQL

Authentication
Keycloak OIDC

Monitoring
Prometheus

Deployment
Helm
Kubernetes
```

其中 **Go 比 FastAPI 更适合这个项目**。

主要因为你的核心工作其实就是：

```text
Kubernetes Watch
Informer
CRD
Pod
RBAC
Context
Concurrent workers
```

这正是 Go `client-go/controller-runtime` 的强项。

如果只是验证 MVP，FastAPI + Kubernetes Python Client 也没问题，但如果未来准备长期作为大数据平台门户，我会直接用 Go。

---

# 15. 开发难度怎么评估

我会把它划成三个阶段。

| 阶段   | 能力                                            | 难度    |
| ---- | --------------------------------------------- | ----- |
| V0.1 | SparkApplication List + 状态颜色 + Detail + Kill  | ★★☆☆☆ |
| V0.2 | Driver/Executor + CPU/Memory + Prometheus     | ★★★☆☆ |
| V1.0 | Keycloak + RBAC + PG历史 + Event + Logs + Audit | ★★★☆☆ |
| V2.0 | 多集群 + 调度分析 + 失败诊断 + 成本分析                      | ★★★★☆ |

一个熟悉 Kubernetes 的开发人员，做出真正能用的 MVP，我认为大约是：

```text
后端骨架             1–2 天
Kubernetes Watch     1–2 天
Application API      1 天
Prometheus API       1 天
Kill API             <1 天
Keycloak             1 天
PG                    1–2 天
Frontend             3–5 天
Deployment/测试       2–3 天
```

所以不是几个月的工程。

**两周左右完全可以形成一个内部可用的 V0.1/V0.2。**

做到比较完整、可长期生产使用的版本，大概是 **3–6 周量级**，主要时间会花在 UI、异常场景、权限和审计，而不是 Kubernetes/Spark API 本身。

---

## 我最推荐你的范围

第一版不要做“大数据调度平台”。

只做这 6 个功能：

1. **SparkApplication 列表 + RUNNING/FAILED/COMPLETED 状态颜色**
2. **Driver / Executor Pod 列表**
3. **Request CPU/Memory + Actual CPU/Memory**
4. **Node / NodePool 调度位置**
5. **Events / Driver Logs**
6. **Keycloak 登录 + Kill SparkApplication + 操作审计**

这已经能覆盖你现在绝大多数 Spark 运维场景。

而且它和 YARN UI 相比还有一个明显优势：**它可以直接告诉你 Executor 究竟跑在 GKE baseline node pool 还是 autoscale node pool、Pod 为什么 Pending、当时 Request 是多少、实际用了多少。**

如果以你当前的技术栈继续做，我会把这个项目定位成：

> **Spark Control Center / Spark Console**

而不是另一个调度器。调度继续交给 Kubernetes + Spark Operator，你只在它上面构建一个 **可观察、可审计、可操作的控制面 UI**。这条路线复杂度最低，而且后续扩展到 ACK/GKE/EKS 时，应用层基本可以保持一致。

[1]: https://github.com/kubeflow/spark-operator/blob/master/docs/api-docs.md?utm_source=chatgpt.com "spark-operator/docs/api-docs.md at master · kubeflow/spark-operator · GitHub"
[2]: https://prometheus.io/docs/prometheus/3.12/querying/api/?utm_source=chatgpt.com "HTTP API | Prometheus"
[3]: https://github.com/kubeflow/spark-operator/issues/2277?utm_source=chatgpt.com "[BUG] executor pod successfully completed but executorState in SparkApplication is failed · Issue #2277 · kubeflow/spark-operator · GitHub"
[4]: https://github.com/kubeflow/spark-operator/blob/master/docs/website/user-guide/working-with-sparkapplication.md?plain=true&utm_source=chatgpt.com "spark-operator/docs/website/user-guide/working-with-sparkapplication.md at master · kubeflow/spark-operator · GitHub"
[5]: https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/?utm_source=chatgpt.com "Custom Resources | Kubernetes"
[6]: https://www.keycloak.org/securing-apps/oidc-layers?utm_source=chatgpt.com "Securing applications and services with OpenID Connect - Keycloak"
[7]: https://github.com/kubeflow/spark-operator/blob/master/docs/website/overview/index.md?plain=true&utm_source=chatgpt.com "spark-operator/docs/website/overview/index.md at master · kubeflow/spark-operator · GitHub"
