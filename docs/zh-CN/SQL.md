# SQL 工作台

Spark Control Center 通过认证的 Kyuubi REST 服务和加载 Driver Guard 的 Spark 3.5 执行 SQL，使用 Polaris 1.6 管理 Iceberg 目录。为控制台部署独立的 Kyuubi 服务。现有 JDBC 客户端可以继续使用原实例。

## 配置 Spark Control Center

启用 `objectStorage` 和 `sql`。在 `sql.results` 中填写现有对象存储连接、存储桶和目录前缀。Chart 检查存储桶是否属于该连接，并要求 S3 支持条件写入。S3 凭证和 GCS Workload Identity 由对象存储插件管理。

查询结果写入 `<prefix>pages/<结果 ID>/`。拥有相应对象存储权限的用户可以浏览、预览和下载结果页，也可以复制到有写权限的位置。结果页由服务写入和删除。`artifacts/`、`staging/` 及其他目录使用普通对象存储 ACL。只向负责发布的运维身份授予运行时 JAR 的写权限。

通过 `sql.catalog.credentialsSecret` 引用保存 Polaris 客户端 ID 和密钥的 Secret。网关和 Driver 使用同一服务账号、角色、Realm、仓库和 REST 地址。网关复用 PostgreSQL 配置以及对象存储插件的签名 Secret。

| Helm 配置 | 默认值 | 用途 |
| --- | --- | --- |
| `sql.limits.resultTTL` | `1h` | 按语句完成时间计算结果有效期，支持 `30s`、`10m`、`4h`、`1d`。 |
| `sql.history.retention` | `30d` | 保留执行历史和 SQL 原文。展开历史行可查看原文。 |
| `sql.audit.retention` | `90d` | 保留 SQL 操作审计。 |

每天按 24 小时计算。到期后结果页和 CSV 下载立即失效。网关通过对象存储服务在后台删除结果页，失败时重试。请让 SQL 网关和对象存储服务持续运行，直至清理完成。

## 安装独立的 Kyuubi 实例

发布包包含基于 Apache Kyuubi 1.11.1 的 Helm Chart，修复了容器 `args` 的模板。使用[独立部署示例](../../charts/kyuubi/examples/values-spark-console.yaml)，填写镜像、Polaris 和存储地址，在 `kyuubi-scc` 中准备所引用的 Secret。启用后，Chart 会创建 `kyuubi` ServiceAccount 和命名空间级 RBAC。Kyuubi REST 服务应只对控制台 SQL 网关开放。引擎命名空间应独立于普通 SparkApplication 的提交范围。

```bash
docker load -i spark-control-center-sql-1.6.1-patch.tar
guard_container=$(docker create spark-control-center-sql:1.6.1-patch)
docker cp "${guard_container}:/app/sql-guard.jar" ./spark-console-sql-guard-1.6.1-patch.jar
docker rm "${guard_container}"

mc cp ./spark-console-sql-guard-1.6.1-patch.jar \
  minio/example-bucket/spark-console-sql/artifacts/
```

将同一份 JAR 放入 Kyuubi 镜像的 `/opt/kyuubi/jars/`，供 `ConsoleKyuubiAuth` 加载。将 `spark.jars` 改成上传位置对应的 S3A URI。Driver 需要读取此 JAR，并通过 Secret 获得 Polaris 凭证和引擎签名密钥。使用 GCS 时，staging 需要对应的 Hadoop GCS Connector 和 Workload Identity。

```bash
helm upgrade --install kyuubi-scc ./kyuubi-0.1.0-scc.1.6.1-patch.tgz \
  --namespace kyuubi-scc --create-namespace \
  -f kyuubi-values.yaml
```

将 `sql.kyuubi.url` 指向专用 REST 服务。示例创建的服务地址为 `kyuubi-scc-rest.kyuubi-scc.svc.cluster.local:10099`。生产配置应使用 TLS。隔离的 HTTP 测试环境需要显式启用 `sql.tls.allowHTTP: true`。

## 执行 SQL 与管理授权

查询使用同一个 Polaris catalog。`analytics.orders` 和 `polaris.analytics.orders` 指向同一张表。对含连字符的标识符逐段引用，例如 `` polaris.`analytics-prod`.orders ``。一份脚本在同一个 Kyuubi 会话中顺序执行，`USE` 和会话临时视图对后续语句生效。执行失败后停止后续语句，已完成的变更保留。

编辑器有选区时，「运行查询」和 `Ctrl+Enter`（macOS 为 `⌘+Enter`）执行选区内容。选区为空时执行全文。Iceberg 的 `files`、`snapshots`、`history` 等元数据表可以通过 `polaris.namespace.table.files` 这样的名称读取，需要基表的 `read` 和 `metadataRead` 权限。

可在 catalog、命名空间、表和视图层级授权。命名空间授权适用于其直接包含的对象，嵌套命名空间单独授权。表和视图授权同时绑定对象名称与身份。同名对象重建后重新授权。查询视图需要具备视图和实际读取对象的权限。

旧的 `write` 授权继续允许追加。覆盖、行更新、行删除、合并、注销和清除底层数据分别授权。使用 `DROP TABLE ... PURGE` 前，需要在 Polaris 启用对应能力，并给服务账号授予所需权限。普通 `DROP TABLE` 注销目录记录并保留底层数据。

网关为用户和租户签发语句授权，Driver 验证签名并检查解析后的关联对象。操作审计包含提交、逐条语句结果、取消、授权、撤权以及结果下载。SQL 原文保存在查询历史中。

语句审计包含实际目标、权限作用域、语句指纹和 Kyuubi 操作 ID。执行前失败的语句显示失败阶段和原因。脚本结束时汇总总语句数、已尝试数、引擎已接受数和跳过数。可从审计 API 的 `sqlDetails` 获取结构化字段。

从 `1.6.0-patch` 升级时，保持 PostgreSQL 与 Polaris 配置一致可沿用授权和已有结果的过期时间。从正式版 `1.6.0` 升级时，需要在管理目录中重新授权。
