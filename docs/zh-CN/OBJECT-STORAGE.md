# 对象存储插件（1.5.0）

这是可选的独立无状态服务，不是嵌入 MinIO Console。默认关闭，不创建插件资源，不要求存储凭证，不改变已有 Spark 作业/工作流能力。

## 构建与安装

```bash
docker build -t spark-control-center:1.5.0 .
docker build -f backend/Dockerfile -t spark-control-center-backend:1.5.0 .
docker build -f object-storage/Dockerfile -t spark-control-center-object-storage:1.5.0 .
helm package charts/spark-control-center

# 首次部署时创建单文件配置；已配置过的文件不要覆盖。
helm show values ./spark-control-center-1.5.0.tgz > values-production.local.yaml
# 修改此文件中的镜像仓库、Endpoint、bucket、认证、数据库和初始授权。
# 根据内网实际仓库分别 docker tag / docker push 三个镜像。
helm upgrade --install spark-console ./spark-control-center-1.5.0.tgz \
  --namespace spark-console --create-namespace \
  -f values-production.local.yaml
```

Chart 打包的 values 即生产模板，包含前后端、本地登录、可选 OIDC、PostgreSQL 引用、工作流、Ingress 与默认关闭的对象存储；启用存储时设 objectStorage.enabled=true 并修改连接及授权。安装时只需要一个 `-f`，Chart 自身默认 values 会自动加载。`values-production.local.yaml` 由 Git/Docker 忽略。部署前核对三处 image.repository/tag、S3 endpoint/bucket、bootstrapGrants.username/tenantId/prefix 及认证/数据库配置，升级前备份 PostgreSQL。

Secret 和 CA ConfigMap 仍是集群资源，不是额外的 Helm values 文件。使用已有的数据库与 OIDC Secret/CA，并在同一发布命名空间准备下面的存储 Secret；不要把真实凭证、生产 values 或 WIF 私钥提交到代码库。

`metadata.annotations` 的值必须是字符串。values 中的注解应加引号，尤其是 `"off"`、`"on"`、`"true"`、`"false"` 和 `"600"`；裸 `off` 会被 Helm 解析为布尔值。不要把关闭缓冲写成 `false` 或 `"false"`，应使用 `"off"`。Chart 会提前拒绝非字符串注解。

## S3 / MinIO

先在发布命名空间创建签名及 S3 凭证 Secret：

```bash
kubectl -n spark-console create secret generic object-storage-signing \
  --from-literal=signing-key="$(openssl rand -hex 32)"
kubectl -n spark-console create secret generic object-storage-s3-credentials \
  --from-literal=access-key="$S3_ACCESS_KEY" \
  --from-literal=secret-key="$S3_SECRET_KEY"
```

可在安全管理终端执行；避免把凭证放在可共享的 shell 历史中。`objectStorage.internalAuth.existingSecret` 和 S3 credentialsSecret 只引用 Secret 名，不在前端 config.js 中暴露。

显式配置 endpoint、region、forcePathStyle、bucket 白名单。内部 HTTPS 使用 `caConfigMap` 与 `caKey` 挂载 PEM CA，不提供跳过 TLS 校验选项。`conditionalWrites: true` 仅应在服务确实支持 PutObject/CompleteMultipartUpload 条件写时开启；不支持时设为 false，上传、编辑及复制禁用，不会偷偷进行无条件覆盖。

首次管理员数据权限通过 bootstrapGrants 配置。`bucket` 指定单个已配置桶；`allBuckets: true` 将相同目录与操作权限显式授予连接白名单中的每个桶，MinIO/S3 和 GCS 行为一致。新增白名单桶不会扩大普通用户授权。username 必须等于实际登录用户名，tenantId 为持久化租户 ID；OIDC 首次登录建用户后，插件一分钟内会重新检查初始化授权。初始化记录只应用一次，页面撤销后重建 Pod 不会重新授予；重新授予请使用页面。

## GCS 与 WIF

将 GKE 或通用 WIF 示例中的连接及 WIF 字段合并到自己的单个生产 values 文件，无需叠加多个 `-f`。一个插件 Pod 共享专属 KSA；各 GCS 连接的外部 IAM 必须授权该身份。provider 明确为 `gcs`，不使用 S3 AK/SK。

- **GKE**：先启用集群和节点池的 WIF，授予 KSA principal 对指定 bucket 的 IAM。也可通过 `serviceAccount.annotations` 配置 IAM service account impersonation。应用使用 metadata server/ADC，不挂载服务账号 JSON 私钥。仅添加 annotation 并不完成 WIF 前置配置。
- **自建 Kubernetes**：先配置 Google Workload Identity Pool/Provider、Kubernetes OIDC/JWKS 及 bucket IAM，再创建 external_account 配置 ConfigMap。Helm 按每个 connection ID 投射 KSA JWT；gcs 连接的 token 路径为 `/var/run/secrets/storage-wif/gcs/token`。`credential_source.file` 必须准确匹配该路径。

external_account 示例（将项目数字、Pool、Provider 与 audience 换成自己的配置）：

```json
{
  "type": "external_account",
  "audience": "//iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/example-pool/providers/kubernetes",
  "subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
  "token_url": "https://sts.googleapis.com/v1/token",
  "credential_source": {
    "file": "/var/run/secrets/storage-wif/gcs/token",
    "format": { "type": "text" }
  }
}
```

```bash
kubectl -n spark-console create configmap spark-console-storage-wif \
  --from-file=config.json=external-account.json
```

需要 impersonation 时添加官方生成的 service_account_impersonation_url，并授予对应 IAM。Projected JWT 的 audience 必须在外部 Provider allowed audiences 内，不能将 Keycloak 登录 token 用作 KSA WIF。外部 IAM 是第一道边界，页面 Prefix 权限只能进一步收紧，不能替代 IAM。

## 目录、权限与编辑

管理员在“对象存储 → 目录权限”选择用户、连接、bucket、prefix 和操作。管理员也必须有数据授权；只能把自己已有的范围/动作授予本租户用户，不能跨租户或扩大范围。普通用户的文件操作由具体授权决定，可授权上传、编辑、删除、复制；权限配置仍仅管理员可管理，Spark 作业权限不变。空 prefix 表示整个 bucket，页面必须再次确认；`team/a/` 不匹配 `team/ab/`。

页面只展示授权入口，目录来自 object key 的 `/` 前缀，不等同于本地目录。每页 100 条，分页游标绑定身份、目录与当前授权。点路径段、反斜杠、控制字符和超过 1024 字节的键不支持；中文、空格与百分号按原始 key 处理。撤权作用于后续请求；已经开始的传输不承诺瞬间中断。

`objectStorage.limits.textMaxBytes` 控制可编辑大小和有界预览，默认 1048576 字节，可配置为 1024～8388608 字节。最多读取上限+1 字节，截断时保留完整 UTF-8 字符。超过上限的文本以截断只读方式打开，提示完整文件大小，剩余内容下载到本地查看。后端同样拒绝保存超限源文件；调高上限需同步评估内存和并发，大量 JSON 转义的保存请求可能需要接近文本上限六倍加额外头部的 Nginx/Ingress body 配额。拒绝二进制、NUL、压缩自动展开；HTML/SVG/Markdown 不渲染、不执行脚本。

新建和编辑共用本地打包的 Monaco 编辑器：语法着色、行号、折叠、注释、撤销、Ctrl+F 搜索、Ctrl+H 替换、大小写/全字/正则选项、单个/全部替换、Ctrl+D 多选、Alt+点击与 Ctrl+Alt+上下箭头复制光标。JSON/YAML/XML 提供结构错误提示，JavaScript/TypeScript 提供语法提示；其他格式提供高亮与注释，不提供完整编译器或远程语言服务。支持 md、txt、log、sh、bat、conf、cfg、py、java、xml、sql、hql、json、yml、yaml、ini、js、ts、csv、properties、toml、html、svg。新建需要 upload 权限且不覆盖同名对象，编辑需要 edit 权限及原 ETag/generation。文件大小按十进制 B/KB/MB/GB/TB 自适应显示（1 KB = 1000 B）。

上传默认禁止覆盖同名对象；SDK/API 保留显式版本匹配覆盖的能力，页面第一版不提供强制覆盖。上传分片默认 8 MiB、每副本最多 4 路。可重新选择相同名称、大小、lastModified 的文件并勾选恢复，上传票据在浏览器本地保存，24 小时到期，内部 GCS session URI 加密封装。浏览器不访问内部 endpoint，不持有 AK/SK。取消仅取消未完成上传；文件可能已在最后分片完成时创建，须刷新确认。

复制是同连接的服务端单对象复制，目标必须不存在；不支持递归目录或跨连接复制。删除是单对象删除。旧 S3 兼容服务可能忽略 DeleteObject If-Match：插件有删除前版本检查，但无法保证外部系统在检查后并发改写时的原子删除。GCS 使用 generation 条件删除。需要更强恢复保障时应启用底层版本管理/备份，第一版不管理这些版本。

## 审计、网络和无状态

- 记录 UPLOAD、DOWNLOAD、CREATE、EDIT、DELETE、COPY、POLICY_CHANGE；每次下载始终记录一行并在流传输结束后更新结果（表示服务端传输结果，不保证客户端已落盘），旧 audit.downloads 配置不再关闭审计。不记录刷新、预览或每个分片。上传仅一个审计行，通过 pending/succeeded/failed/cancelled/unknown 更新结果。
- 文件内容不入 PostgreSQL。权限表和小型操作表使用现有核心数据库；可配置独立数据库凭证，但该账号仍必须能访问核心 local_users 表。第一版不支持完全隔离、没有身份表的独立数据库。
- 审计默认保留 30 天，每分钟最多清理 1000 条插件旧记录，不触碰已有 Spark/工作流审计。权限表不按保留期删除。数据库不可用时拒绝新变更；中断后可能保留 pending/unknown，不宣称数据库与对象存储存在跨系统原子事务。
- 不完整 S3 multipart 应在 bucket 配置生命周期清理。插件会主动取消正常取消的上传，但丢失票据、异常进程及过期会话仍需底层清理。
- 默认独立 ClusterIP、KSA 无 Kubernetes RBAC、只读根文件系统。签名 Secret 在主后端和插件间共享，至少 32 字节；轮换后旧上传/分页票据失效，应先完成或取消上传。
- 插件 NetworkPolicy 是选配项，默认关闭；同一内网部署并不要求本组件额外创建策略，但已有命名空间/集群策略仍然生效。启用后入口仅允许本次 release 后端，出口须放行 DNS、PostgreSQL、存储、Google STS/metadata。仅按端口放行不是目的地白名单，且需 CNI 支持。参见 [Kubernetes 网络策略](https://kubernetes.io/docs/concepts/services-networking/network-policies/) 和 [GKE WIF 网络要求](https://docs.cloud.google.com/kubernetes-engine/docs/how-to/network-policy)。
- Nginx 对存储接口关闭上传/下载缓冲，单个分片请求上限 17 MiB。外部 Ingress 也须配置至少 17 MiB body 和足够的传输 timeout；长传输不沿用核心 JSON 的 15 秒超时。
- 三个存储 overlay 已包含 ingress-nginx 的 17m body、关闭缓冲和 600 秒超时注解。其他 Ingress controller 应换用对应配置；调整插件 timeout 后也要同步这些注解。
- GOMEMLIMIT 默认 180MiB，容器 memory limit 256Mi；资源修改应与分片大小和并发同步。插件故障不参与主后端 readiness，旧页面和调度仍可继续运行。

## 本机验证及内网验收

```bash
npm install
npm test
npm run build
node scripts/object-storage-local-test.mjs
QA_VERSION=1.5.0 node scripts/container-object-storage-smoke.mjs
node scripts/object-storage-helm-test.mjs
```

脚本创建带独立测试标签的 PostgreSQL/MinIO 容器并自动清理，不连接生产集群。启用浏览器 Mock 可在本地 config.js 中设置 features.objectStorage=true；Mock 上传仅模拟不超过 1 MiB 的文本，真实大文件需测试插件。浏览器回归脚本为 `node scripts/visual-qa-object-storage.mjs`，先启动 `npm run dev -- --host 127.0.0.1`。

本机通过不等于真实 GCS WIF 或内网 Spark 集群验收。部署时检查：登录身份/租户、CA、网络策略、bucket IAM、只读用户及越权目录、上传/下载大文件内存、插件重建后恢复、编辑冲突、审计保留、关闭插件后核心功能。先使用隔离测试 bucket/Prefix，不在生产关键对象上测试删除。

### GKE KSA 直接授权

当 serviceAccount.create=true 且 serviceAccount.name 留空时，Chart 自动创建专属 KSA。release 与 namespace 均为 spark-console 时，名称是 spark-console-spark-control-center-object-storage。下面直接绑定 KSA principal，不需要 Google 服务账号模拟注解；集群和节点池须已启用 WIF，再授予桶访问权限：

```bash
gcloud storage buckets add-iam-policy-binding \
  gs://<GCS_BUCKET> \
  --role=roles/storage.objectUser \
  --member="principal://iam.googleapis.com/projects/<PROJECT_NUMBER>/locations/global/workloadIdentityPools/<PROJECT_ID>.svc.id.goog/subject/ns/spark-console/sa/spark-console-spark-control-center-object-storage" \
  --condition=None
```

release 名称不同请改用实际 namespace/KSA，每个配置桶均需绑定。IAM 与 UI 目录授权必须同时满足。若启用严格网络策略，GKE Dataplane V2 使用 169.254.169.254:80，其他受支持的 GKE 配置使用 169.254.169.252:988（部分配置也需 987）；以当前 GKE 版本官方说明为准，不能假定普通内网端口规则已经涵盖 WIF。
