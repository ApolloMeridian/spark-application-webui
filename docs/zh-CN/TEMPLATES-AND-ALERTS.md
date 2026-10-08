# 作业模板与诊断告警

## 精简 YAML 模板

克隆、重试和保存模板只保留 `apiVersion`、`kind`、`spec` 及必要元数据（名称、命名空间、自定义标签与注解）。运行 `status`、`managedFields`、所有权/finalizer 字段、旧提交者和 `kubectl.kubernetes.io/last-applied-configuration` 均会移除。

从作业保存模板时，编辑窗口展示清理后的 YAML。旧 JSON 模板在编辑、复制、渲染时自动转换。保存会生成不可变的历史版本。

## 参数定义

通过表单逐项配置名称、类型、必填、默认值、枚举、正则和描述，无需编写 JSON。示例：

```yaml
apiVersion: sparkoperator.k8s.io/v1beta2
kind: SparkApplication
metadata:
  name: "{{name}}"
  namespace: spark
spec:
  type: Scala
  mode: cluster
  image: "{{image}}"
  mainClass: org.apache.spark.examples.SparkPi
  mainApplicationFile: local:///opt/spark/examples/jars/spark-examples.jar
  sparkVersion: "3.5.3"
  driver:
    cores: 1
    memory: 1g
    serviceAccount: spark
  executor:
    instances: "{{executorCount}}"
    cores: 1
    memory: 2g
```

将 `name`、`image` 定义为字符串，`executorCount` 定义为整数。完整标量占位符渲染后保持整数、数字或布尔类型；嵌入较长字符串的占位符仍为字符串。值按 YAML 数据转义，不能注入新字段。Spark 配置项要求字符串时，应使用字符串参数。

渲染结果仍须经过 Kubernetes 服务端预检和权限检查。敏感信息引用已有 Kubernetes Secret，不要保存明文凭证。工作流中的默认值支持[日期表达式](WORKFLOWS.md#名称和日期参数)。

## 版本历史

回溯只切换当前版本指针，不生成新版本。预览窗口明确显示查看版本，窗口内和历史列表中对应版本的删除按钮禁用；当前版本始终不可删除。可以从历史快照选择“编辑并保存为新版本”，保存时从最大已分配版本号继续递增，即使更高的历史版本被删除也不复用编号。

删除历史版本会实际清除 PostgreSQL 中对应 YAML 和参数；已执行工作流的独立冻结快照不受影响。

## 诊断指纹规则

在告警中心新建规则，选择“诊断指纹”。支持 `APPLICATION_FAILED`、`VOLUME_MOUNT`、`IMAGE_PULL`、`SCHEDULING`、`OOM`、`EXECUTOR_LOST`、`RBAC`，以及集群中已捕获的代码。可直接输入未来新增的代码，无需升级规则编辑器。指纹卡片可快速创建对应代码和命名空间的规则。

- 多个代码采用任一匹配；留空匹配现有及未来所有诊断。
- 命名空间范围、严重度、启用状态和 Webhook 配置继续生效。
- 同一作业的不同诊断分别生成告警，互不覆盖。
- 评估器采集 Kubernetes 事件，包括仅通过事件可见的挂载和镜像拉取故障，保留证据与建议。

原有失败、长时间等待、资源压力和重复重试规则仍然有效。诊断规则保存为 `type: diagnostic`，代码存入 `diagnosticCodes`；启动迁移自动补充非空字段，规则快照保留所选代码。Webhook 保持唯一指纹，并在证据中包含如 `diagnostic=VOLUME_MOUNT` 的标识。
