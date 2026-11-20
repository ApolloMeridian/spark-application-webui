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
guard_container=$(docker create spark-control-center-sql:1.6.1-patch-v3)
docker cp "${guard_container}:/app/sql-guard.jar" ./spark-console-sql-guard-1.6.1-patch-v3.jar
docker rm "${guard_container}"

mc cp ./spark-console-sql-guard-1.6.1-patch-v3.jar \
  minio/example-bucket/spark-console-sql/artifacts/
```

将同一份 JAR 放入 Kyuubi 镜像的 `/opt/kyuubi/jars/`，供 `ConsoleKyuubiAuth` 加载。将 `spark.jars` 改成上传位置对应的 S3A URI。Driver 需要读取此 JAR，并通过 Secret 获得 Polaris 凭证和引擎签名密钥。使用 GCS 时，staging 需要对应的 Hadoop GCS Connector 和 Workload Identity。

```bash
helm upgrade --install kyuubi-scc ./kyuubi-0.1.0-scc.1.6.1-patch-v3.tgz \
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

## Iceberg 运维 SQL 与权限

目标组合为 Spark 3.5.9 和 Iceberg 1.10.2（Spark 3.5、Scala 2.12 Runtime）。将 SQL 网关与 Driver Guard JAR 一起升级，并在专用 Kyuubi 上重建 SQL 引擎。每次提交最多 20 条语句，SQL 文本最多 32 KiB，JSON 请求体最多 64 KiB。单条语句最多引用 32 个输入关系，展开视图和临时视图后最多校验 64 项访问权限。持久对象名仅支持小写 ASCII 字母、数字、下划线和逐段反引号引用的连字符，且首个字符须为小写字母或下划线。CTE 和会话临时视图的别名按 Spark 默认大小写不敏感规则处理。

以下操作映射到控制台授权。Polaris 还会独立校验网关和 Driver 共用的服务身份。运行 SQL 的用户只通过控制台申请权限。

| SQL 操作 | 控制台权限 | 约束 |
| --- | --- | --- |
| `SELECT`、`EXPLAIN SELECT`、`DESCRIBE QUERY SELECT` | 输入表或视图 `read` | 视图还校验实际读取对象。`SHOW SYSTEM FUNCTIONS` 仅列系统函数。 |
| `SHOW CREATE TABLE`、`DESCRIBE TABLE`、`SHOW TBLPROPERTIES` | 对象 `metadataRead` | Iceberg 元数据表还需要基表 `read`。 |
| `CREATE TABLE`、CTAS、创建视图和命名空间 | 父级 `create`，输入关系 `read` | 指定受管 Iceberg 表。创建时支持格式 v1、v2。 |
| `INSERT INTO`、`INSERT OVERWRITE` | 表 `write` 或 `overwrite`，输入关系 `read` | 使用 Iceberg 隐藏分区。 |
| `UPDATE`、`DELETE`、`MERGE`、`TRUNCATE TABLE` | 表 `update`、`deleteRows`、`merge`、`deleteRows` | 子查询和 MERGE 源关系需要 `read`。TRUNCATE 清空当前快照，保留表结构和历史。 |
| 改列、修改表属性、添加／删除／替换分区字段、写入排序、设置／删除标识字段 | 表 `metadataWrite` | 修改快照保留属性还需 `expireSnapshots`。 |
| `ALTER VIEW ... AS` 和 `SET/UNSET TBLPROPERTIES` | 视图 `metadataWrite`，定义引用关系 `read` | 可编辑的视图属性为 `comment`。 |
| 重命名表或视图 | 源对象 `metadataWrite`，目标命名空间 `create` | 目标名称必须空闲。 |
| `DROP TABLE`、`DROP TABLE ... PURGE`、删除视图或空命名空间 | 对象 `delete` 或表 `purge` | PURGE 需要 Polaris 提供对应能力。 |
| `rewrite_data_files`、`rewrite_position_delete_files` | 表 `rewriteDataFiles` | 可通过字面量 `options` 设置 `rewrite-all` 或 `min-input-files`。 |
| `CALL polaris.system.rewrite_manifests('db.tbl')` | 表 `rewriteManifests` | 可用命名参数 `spec_id` 指定非负分区规范 ID。 |
| `CALL polaris.system.expire_snapshots(table => 'db.tbl', stream_results => true)` | 表 `expireSnapshots` | 会物理清理只被过期快照引用的文件。可用 `retain_last` 保留额外快照。 |
| `remove_orphan_files(table => 'db.tbl', dry_run => true, prefix_listing => true)` | 表 `previewOrphanFiles` | 通过 Iceberg FileIO 预览候选文件。 |
| `remove_orphan_files(table => 'db.tbl', dry_run => false, prefix_listing => true)` | 表 `deleteOrphanFiles` | 使用 Iceberg 默认安全年龄与受管表自身的位置。 |
| `VERSION AS OF`、`TIMESTAMP AS OF`、查询 `table.branch_name` | 表 `read` + `snapshotRead` | 接受快照 ID、分支或标签名、字面量时间。 |
| `ancestors_of` | 表 `snapshotRead` | 查看指定或当前快照的祖先。 |
| `CREATE/REPLACE/DROP BRANCH` 或 `TAG` | 表 `referenceManage` | 更改 `main` 还需 `snapshotManage`。 |
| 向 `table.branch_name` 写入 | 表 `branchWrite` + 该写入动作 | Driver 验证分支已存在且属于绑定的表。 |
| `fast_forward`、`rollback_to_snapshot`、`rollback_to_timestamp`、`set_current_snapshot`、`cherrypick_snapshot`、`publish_changes` | 表 `snapshotManage` | 继续遵循 Iceberg 的快照祖先、操作类型和 WAP 约束。 |

过程需要带命名空间的表名和经校验的字面量参数。孤儿文件清理通过 Iceberg FileIO 列举受管表路径，可使用 Polaris 下发的存储凭证。清理范围受表 UUID、表位置及平台存储权限约束。委派方式与现有表授权相同，支持在目录、命名空间或具体表授予这些动作。升级时，仅为仍保留完整初始授权的 Helm 管理员一次性添加新权限。

审计记录 Iceberg 操作、请求的分支／标签或快照、受管表 UUID、操作者、执行结果与耗时。`engineSucceeded` 可以区分操作已经完成但结果页保存失败的情况。快照切换过程在返回前后快照 ID 时，服务端将这些 ID 一并保存到 `sqlDetails.outcome`，结果页到期后仍可查询审计。

错误按语法错误（400）、平台限制（422）、缺少授权（403）和解析器不可用或内部失败（503）区分。已进入队列的脚本在历史与审计中记录失败阶段。不回显 SQL 原文或上游堆栈。

### 仍由平台管理的操作

- 关闭隐式 Schema Evolution。普通 MERGE 保持 `merge` 权限。加列先用有 `metadataWrite` 授权的 `ALTER TABLE`，再执行 MERGE。
- `CREATE OR REPLACE TABLE` 和 `REPLACE TABLE` 保持拒绝。原子替换会同时修改数据和结构，需要重新验证同名对象绑定并独立授权。可按意图使用 `CREATE TABLE IF NOT EXISTS` 或 `INSERT OVERWRITE`。
- `CREATE TABLE LIKE` 采用经过检查的 `SHOW CREATE TABLE` 输出并修改目标表名。Iceberg V2 表的 `ANALYZE TABLE` 保持关闭，可查询 `.files`、`.partitions` 做运维统计。二者不会写入 Spark 优化器统计。
- 显式 `INSERT ... PARTITION`、`REPLACE WHERE`、格式升级、任意 Location、外部 UDF 和非白名单配置保持拒绝。外部数据导入、注册或迁移、共享底层文件的表克隆，以及 CDC 视图由平台单独管理。

上游参考 [Iceberg 1.10.2 DDL](https://iceberg.apache.org/docs/1.10.2/spark-ddl/)、[Spark 写入](https://iceberg.apache.org/docs/1.10.2/spark-writes/)、[Spark 运维过程](https://iceberg.apache.org/docs/1.10.2/spark-procedures/)和 [Polaris 1.6.0](https://polaris.apache.org/releases/1.6.0/)。
