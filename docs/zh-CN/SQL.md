# SQL 工作台

Spark Control Center 1.6.0 提供选配的只读 SQL 工作台，适配 Kyuubi 1.11.0、Spark 3.5 和 Apache Polaris 1.6.0。查询使用一条受支持的 SELECT 语句，表名采用 `polaris.namespace.table`。支持常见 CTE、JOIN、聚合、窗口函数和部分 Spark/Hive 内置函数。侧栏展示已授权的表并按需读取当前字段。命名空间与表的创建、删除由 Polaris 管理工具处理。

网关向 Kyuubi 提交一条用户查询，将有界结果分批写入 S3 后关闭操作和会话。分页和 CSV 下载复用保存的结果。若查询标记为截断，CSV 只包含已保存的行。表授权、查询状态和操作审计持久化到 PostgreSQL。审计记录操作人、SQL 指纹、输入表、访问决定与操作 ID。查询结束后清除库中的 SQL 正文，Kyuubi 和 Spark 的日志按各自保留策略管理。

## 准备专用 Kyuubi 服务

SQL 网关独占一个 Kyuubi 服务入口。通过网络策略限制访问，仅允许网关连接。启用 LDAP 或 Kyuubi JDBC 认证的 Basic REST，关闭匿名方式。使用 JDBC 认证时应由独立只读数据库账号查询，通过数据库密码哈希扩展验证凭证，将认证配置挂载自 Secret。部署单副本 Kyuubi，或确保 REST 会话始终由持有相应会话与操作的实例处理。[Kyuubi JDBC 认证](https://kyuubi.readthedocs.io/en/v1.11.0/security/jdbc.html) · [Kyuubi REST 接口](https://kyuubi.readthedocs.io/en/v1.11.0/client/rest/rest_api.html)。

```properties
kyuubi.authentication=JDBC
kyuubi.frontend.protocols=REST
kyuubi.frontend.rest.bind.port=10099
kyuubi.frontend.rest.ui.enabled=false
kyuubi.engine.type=SPARK_SQL
kyuubi.engine.share.level=USER
kyuubi.engine.doAs.enabled=false
kyuubi.engine.single.spark.session=false
kyuubi.operation.interrupt.on.cancel=true
kyuubi.operation.query.timeout=PT10M
kyuubi.session.engine.idle.timeout=PT5M
kyuubi.session.idle.timeout=PT10M
kyuubi.session.engine.spark.max.lifetime=PT2H
kyuubi.engine.submit.timeout=PT180S
kyuubi.engine.kubernetes.submit.timeout=PT180S
```

为 SQL Driver 配置独立命名空间、ServiceAccount、Pod 配额和网络策略，同时限制挂载凭证、工作负载、日志和 Spark UI 的访问。通用 SparkApplication 提交与旧的匿名/root 链路必须与受保护的命名空间和业务表隔离。Spark staging、event log、业务表、查询结果使用各自的存储位置与访问权限。

从 `spark-control-center-sql:1.6.0` 提取 `/app/sql-guard.jar`，放入 Spark Driver 镜像的 `/opt/spark/jars`。Driver 需使用 JDK 11 或更高版本、Spark 3.5/Scala 2.12 对应的 Iceberg runtime 和匹配的 Iceberg AWS bundle。Kyuubi 提交端与 Spark 镜像的 Hadoop 版本可以不同，应分别核对实际 classpath，让 `hadoop-aws` 与各自的 Hadoop 版本一致。Hadoop 3.3.x 的 S3A 使用 AWS SDK v1，Hadoop 3.4.x 使用 AWS SDK v2，Iceberg S3FileIO 有独立的运行时依赖。[Hadoop AWS 配置](https://hadoop.apache.org/docs/r3.4.3/hadoop-aws/tools/hadoop-aws/index.html) · [Polaris Spark 客户端](https://polaris.apache.org/releases/1.6.0/polaris-spark-client/)。

```bash
container="$(docker create spark-control-center-sql:1.6.0)"
docker cp "$container:/app/sql-guard.jar" ./sql-guard.jar
docker rm "$container"
```

在专用 Kyuubi 服务上设置以下 Spark 配置。使用 `catalog-impl` 时省略 `type`。为每个 Spark 用户创建独立 Secret，其中 `credential` 键的内容为该用户的 `clientId:clientSecret`，只挂载到对应的 Driver。

```properties
spark.sql.extensions=org.apache.iceberg.spark.extensions.IcebergSparkSessionExtensions
spark.sql.catalog.polaris=org.apache.iceberg.spark.SparkCatalog
spark.sql.catalog.polaris.catalog-impl=org.sparkconsole.MountedRESTCatalog
spark.sql.catalog.polaris.uri=https://polaris.example.com/api/catalog
spark.sql.catalog.polaris.warehouse=example_catalog
spark.sql.catalog.polaris.oauth2-server-uri=https://polaris.example.com/api/catalog/v1/oauth/tokens
spark.sql.catalog.polaris.header.Polaris-Realm=POLARIS
spark.sql.catalog.polaris.cache-enabled=false
spark.sql.defaultCatalog=polaris
spark.sql.variable.substitute=false
spark.driver.maxResultSize=128m
```

在 Kyuubi 中配置按用户生效的默认值。用户名、client ID 和角色必须与 SQL 网关中的身份匹配。更新后重建对应的 SQL 引擎。保留挂载文件作为唯一的目录凭证来源，不设置内联 `credential` 或 `token`。

```properties
___user_alice___.spark.kubernetes.driver.secrets.polaris-user-alice=/var/run/spark-console/polaris/user_alice
___user_alice___.spark.sql.catalog.polaris.console-credential-file=/var/run/spark-console/polaris/user_alice/credential
___user_alice___.spark.sql.catalog.polaris.console-principal-id=ALICE_CLIENT_ID
___user_alice___.spark.sql.catalog.polaris.scope=PRINCIPAL_ROLE:alice_reader
```

Polaris 须能够为 Spark 签发业务桶访问凭证。内网 MinIO 测试可在创建目录时配置 MinIO endpoint 和 path-style，并用非 root principal 验证 Spark 能读取 Iceberg。COS 部署需用实际目录角色验证 STS 凭证签发和 S3FileIO。创建独立的结果桶，给 `spark-console-sql/` 配置生命周期，网关只需该前缀的 `s3:PutObject` 和 `s3:GetObject` 权限。避免在对象存储浏览器中开放结果桶。

## Helm 与表授权

在同一份生产 values 文件的 `sql` 段设置 `enabled: true`、两个 REST 地址、warehouse、结果存储 endpoint 和 bucket。生产环境使用 TLS，私有 CA 通过 `sql.tls.caConfigMap` 挂载。隔离的内网测试可参考 [MinIO values 示例](../../charts/spark-control-center/examples/values-sql-minio.yaml)。SQL 网关无需 Kubernetes API 权限。

在控制台的 release 命名空间预先创建以下 Secret。

| 设置 | Secret 数据 |
| --- | --- |
| `sql.internalAuth.existingSecret` | 至少 32 个随机字节的 `signing-key` |
| `sql.database.existingSecret` | 指向核心 PostgreSQL 同一数据库和 schema 的 `dsn` |
| `sql.identities.existingSecret` | 以控制台不可变用户 ID 为键的 `profiles.json` |
| `sql.results.credentialsSecret` | 结果桶的 `access-key` 和 `secret-key` |

可从授权的用户 API 或 PostgreSQL 中获取用户 ID 和租户 ID。为每个用户单独配置 Kyuubi 与 Polaris 身份。更新 Secret 后重启 SQL 网关。身份配置格式如下。

```json
{
  "CONSOLE_USER_ID": {
    "tenant": "TENANT_ID",
    "kyuubiUser": "user_alice",
    "kyuubiPassword": "从密钥系统读取 Kyuubi 密码",
    "polarisClientId": "ALICE_CLIENT_ID",
    "polarisClientSecret": "从密钥系统读取 Polaris 密钥",
    "polarisPrincipalRole": "alice_reader"
  }
}
```

在租户管理页启用 SQL 工作台。拥有 SQL 表授权管理权限的管理员先配置目标用户的最小 Polaris 角色，再在工作台为该租户内的用户授予具体表的 SELECT 权限。授权绑定用户、租户和当前表身份。控制台撤权会立即拦截已有结果，取消受影响的执行。若还要收回控制台之外的访问，需要同步调整 Polaris 权限。

## 查询上限与资源回收

默认保存最多 10,000 行或 16 MiB，以先到达的上限为准。网关每次从 Kyuubi 读取最多 100 行，每个用户仅有一条查询在执行，全局默认两个工作器。可在 `sql.limits` 中调整。结果过期后 API 拒绝翻页和下载，桶生命周期负责清理过期和未完成对象。配置 Kyuubi 侧的查询超时和会话空闲超时，确保网关中断时 Kyuubi 仍能回收。网关重启后不会自动重跑已中断的语句。

同一用户后续查询可复用未空闲超时的 USER 引擎。在 Kubernetes 上可从初始一个 Executor、最少零个 Executor、空闲 60–120 秒开始压测，并按实测决定是否保留预热资源。冷启动仍取决于 Driver 拉镜像、Executor 调度和容量。保留 Spark Driver 结果大小限制，调高结果上限前验证实际行宽、收集方式和内存余量。异步全量文件导出暂未开放。[Kyuubi 动态资源分配](https://kyuubi.readthedocs.io/en/v1.11.0/deployment/spark/dynamic_allocation.html) · [Kyuubi 大结果收集](https://kyuubi.readthedocs.io/en/v1.11.0/deployment/spark/large_query_results.html)。
