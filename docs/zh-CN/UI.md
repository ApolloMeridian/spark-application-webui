# 界面导览

## 作业运维

从运行总览查看实时资源及历史提交、失败统计，再在作业列表按命名空间、操作人和状态筛选。

![运行总览](../images/overview-1440.png)

![Spark 作业列表](../images/applications-1440.png)

作业详情展示 Driver/Executor 的资源信息和可选时间范围的历史曲线。持久化 Executor 日志可按时间筛选，并选择正序或倒序。

Driver 日志支持最新/指定行范围、前后翻段及 Loki 时间筛选。

![资源详情](../images/detail-1440.png)

![Executor 日志](../images/executor-logs-1440.png)

![Driver 日志范围](../images/driver-log-range-1440.png)

管理员可提交 YAML，经过服务端预检并确认规范化清单后创建作业。

![YAML 提交](../images/submit-1440.png)

## 工作流与调度

项目组织工作流和任务，拖动节点连接点建立依赖。参数默认值通过表单配置，支持以逻辑调度时间为基准的日期表达式。

![项目组织](../images/workflow-projects-1440.png)

![工作流任务编辑器](../images/workflow-editor-1440.png)

通过下拉菜单或自定义表达式配置秒级 Cron，预览有效范围内的执行时间，在项目执行历史中查看工作流和任务实例。

![Cron 定时配置](../images/workflow-schedule-1440.png)

![执行历史](../images/workflow-history-1440.png)

补数预览显示前 50 次，可选择正倒序和失败策略，确认整个范围后持久化并串行执行。

![补数配置](../images/workflow-backfill-1440.png)

## 对象存储

新建文件夹先保留为临时目录，仅在目录下写入实际文件后形成持久化前缀。可递归搜索当前 Prefix 下的文件名和相对路径，通过下一页继续分批扫描。配置与权限说明见[对象存储文档](OBJECT-STORAGE.md)。

![临时文件夹](../images/object-storage-draft-folder-1440.png)

![目录搜索](../images/object-storage-search-1440.png)

## 模板、告警与治理

复用参数化 YAML 模板并管理版本；诊断规则可匹配已捕获或自定义的故障代码，告警证据关联作业诊断。

![作业模板](../images/templates-1440.png)

![模板参数编辑器](../images/template-editor-1440.png)

![告警中心](../images/alerts-1440.png)

![诊断规则](../images/diagnostic-rule-1440.png)

按操作人、操作类型、结果与关键词筛选审计记录。

![操作审计](../images/audit-1440.png)

在用户管理中维护账号、命名空间权限与租户绑定。

![用户管理](../images/users-1440.png)

返回[中文 README](../../README.zh-CN.md)。
