# 安全加固（1.5.3）

1.5.3 包含以下安全与性能改进。上线前请备份 PostgreSQL 并验证升级。

## 升级要求

- 将 `backend.publicURL` 设置为浏览器访问的 Origin，不含路径。Chart 可从固定 OIDC 回调或 Ingress host/TLS 派生；外部 TLS 终止不在 Chart 中时必须显式填写。不再依赖客户端 Forwarded 头决定信任或 Secure Cookie。
- OIDC 必须显式设置完整 `oidc.redirectURL`，与身份提供商登记值一致，不再根据请求头动态拼接。
- 无 Origin/Referer 的写请求须使用 JSON 或 `X-Requested-With: SparkControlCenter`，二进制分片用后者；即使有这些头，外部或 `null` 源也不允许写入。
- 同步升级 RBAC：后端增加 Services 的 `get` 权限验证 Driver UI 归属。ExternalName、无关服务或不能选中实际 Driver 的 selector 将被拒绝。
- Spark UI 使用保存在 PostgreSQL 的只读票据，仅限一个作业并绑定父会话，最长十五分钟。登出、停用/迁移用户或撤销命名空间权限会使访问失效。父页面每十分钟刷新框架并保留子页面报告的视图；后端/Nginx 日志脱敏票据路径，采用 no-referrer，不要分享票据 URL。
- 历史模板/规则按创建者**当前**租户迁移，找不到创建者时使用不可访问标记 `_legacy_unassigned`；告警沿用所属规则的租户，规则归属不明时也隔离。不能重建历史租户变更。开放访问前核对归属；隔离记录须在 PostgreSQL 中指定核实后的租户。历史版本沿用父模板租户，本次安全升级不要混跑旧、新后端。
- 作业历史按应用 UID 固定归属，不跟随后来复用同名作业的租户变化。旧记录无法确定归属时保留 `tenant_id IS NULL`，暂不出现在租户查询中，重新观测或运维核实后恢复；没有删除历史。迁移后检查 `SELECT application_id,namespace,application_name FROM spark_application_history WHERE tenant_id IS NULL;`，核实后设置真实租户 ID；仅确认是命名空间共享外部作业时设为 `''`，不得批量把未知工作流历史设为共享。
- 存储容器需要可写 `/tmp`。Chart 提供磁盘型 `emptyDir`（256 MiB）与 GID 65532。自行部署只读容器时需提供同类挂载。分片临时文件权限 0600，通常每次请求完成后删除，替换 Pod 清空临时卷；同时规划节点临时磁盘和内存。
- Go 镜像构建要求 BuildKit（`DOCKER_BUILDKIT=1`），模块与编译缓存不会进入运行镜像。回滚需停止写入并成套恢复兼容的应用和数据库；恢复旧备份前另行备份升级后的数据。

## 已落实的安全边界

- 两个 Go 组件要求 Go 1.26.11，Docker 使用对应的受支持补丁版本。前端最低修复版本包括 DOMPurify 3.4.17、Vite 6.4.4 和 Vitest 5.0.3，CI 遇到 High/Critical npm 漏洞将失败。依赖检查是当前漏洞库的快照，不保证未来没有新披露问题。
- 活跃 Pod 使用所有者 UID、精确应用标签或从已验证 Driver 获取的精确 `spark-app-selector` 关联，无标签时仅允许精确 Driver 名称回退；冲突归属优先，作业名重叠不再关联他人 Executor。Kill 同样只选择已验证 Driver，不直接信任状态字段；无法核实时返回 409，不终止 Pod 或修改重启策略。
- Spark UI 视为不可信作业内容，iframe 与响应 CSP 均移除同源权限，直接打开代理也受沙箱限制。转发前移除控制台 Cookie/Authorization，不接受上游 Cookie/CSP/CORS 放宽限制。路径前缀来自校验后的配置。仅只读代理允许隔离页面通过 `Origin: null` AJAX 使用范围受限的路径票据，不依赖控制台 Cookie；控制台 API 不开放 null 源读取或写入。
- 模板/规则/告警增加租户列并在 SQL 过滤。汇总、历史、指纹排除其他租户成功绑定的工作流资源；失败/未派发任务不再抢占资源名。外部非工作流作业仍按授权命名空间共享；严格资源隔离应划分 Kubernetes 命名空间，工作流建议启用名称后缀。
- 登录账户/来源速率在 PostgreSQL 中统一协调，每进程最多四并发密码哈希；配额数据库不可用时拒绝新尝试，不绕过预算。
- 告警评估保留数据库租约及单作业错误隔离；Webhook 保留重定向/DNS/元数据保护，并采用下述持久化投递队列。
- `networkPolicy.enabled` 同时保护前后端，后端默认仅接受本 release 前端；监控等可信调用方需额外授权。不设置 `backend.networkPolicy.egress` 时不限制后端出站。存储示例限制的是**端口**而非目标地址，按 CNI 自行限制目标；不要误封 GKE KSA WIF 所需元数据路径。

## 持久化协调与采集

- 调度规划将提交/终止意图持久化到 PostgreSQL，Kubernetes 观测、Dry Run、创建和终止均在事务外执行。执行租约为 90 秒，RPC 上限 20 秒，通过随机租约令牌隔离过期确认；重试身份包含任务 attempt。恢复会核认已创建作业，执行前后重新检查权限/取消状态，已接受的创建通过持久化补偿处理。数据库与 Kubernetes 并非原子事务，仍存在有限的在途副作用窗口。
- Webhook 使用事务型 outbox、不可变投递快照和稳定的 `X-Spark-Delivery-ID`/`Idempotency-Key`。所有副本最多四个有效投递租约，失败指数退避，上限十五分钟。确认丢失可能重复投递，接收端须幂等去重。成功记录保留七天/最多十万条；未完成积压达到一万条时显式背压，不静默丢弃。更换端点不会把旧记录发往新接收端。
- 同一数据库 schema 的存储副本共享传输/请求并发及用户+租户分钟窗口。租约十五秒、每五秒续租；续租失败取消流式/SDK 请求。滚动部署取活跃副本最严格预算，分片会话锁也改为持久租约，不再跨传输占用数据库连接。SDK/网络取消是协作式的，租约不能使底层写入成为原子事务。
- 登录账户/来源分钟预算统一放在 PostgreSQL，bcrypt 并发仍按进程限制。来源使用实际 socket 对端，不信任客户端转发头；代理可能合并来源，入口侧仍可增加真实客户端限流。
- 每个后端按命名空间共享完整分页 LIST/WATCH 快照，从列表 resourceVersion 开始 watch，断连或 410 后失效重建。此实现为专用反射器，并非 client-go SharedInformer。生命周期操作继续直接读取 Kubernetes。
- 作业页改用 SQL 完成筛选、租户校验、排序、收藏、总数和 LIMIT/OFFSET 分页。数据库时钟租约协调所有副本每五秒采集，过期采集者不能覆盖新快照；索引未就绪或超过九十秒返回 503 提示重试。计数与分页使用同一可重复读快照。有界延迟仅用于展示，不用于授权变更操作。
- 第一轮的历史租户隔离与 Bootstrap 一次性撤销语义继续生效。目标地址级 CNI 出站规则、真实云 IAM/Kubernetes 验收仍需部署环境验证。

## 验证与参考

执行 `npm audit`、`npm test`、`npm run build`、`npm run docs:check`、`node scripts/security-browser-test.mjs`、`node scripts/object-storage-local-test.mjs` 和 `node scripts/object-storage-helm-test.mjs`。安全浏览器 fixture 在 Edge/Chromium 中验证生产沙箱策略，HTTP/SQL 测试覆盖票据鉴权与撤销。Docker 集成使用隔离 PostgreSQL/MinIO，覆盖租户读写/版本、失败同名任务、调度/配置锁分离、告警租约竞争/恢复、代理归属、CSRF 与凭证剥离。GCS SDK/协议模拟不代替真实 WIF/IAM 验收。

完整路由/变量/表清单见[源码核对参考](REFERENCE.md)。私有审计输入、values、凭证和本地工件不纳入 Git。

CI 还分别在 `backend` 和 `object-storage` 执行 `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`。门禁检查可达符号，仍应查看 verbose 中的包/模块提示。未使用、已停止维护的 `x/crypto/openpgp` 与受支持的 bcrypt 依赖位于同一模块，因此保留模块级提示；项目没有导入 OpenPGP。本轮不宣称完成生产渗透测试或镜像操作系统全量 CVE 扫描。
