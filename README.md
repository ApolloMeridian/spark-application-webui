# Spark Control Center UI

Spark on Kubernetes 运维控制台。既可使用确定性的浏览器 Mock 数据，也可通过仓库内的 Go 后端连接真实 Kubernetes、Prometheus 和 PostgreSQL。

## 运行

```bash
npm install
npm run dev
```

打开 Vite 输出的本地地址。登录页可选择模拟角色：

- `viewer`：只读
- `operator`：可终止运行中的作业
- `admin`：可终止运行中的作业、删除终态作业并查看审计页

## 验证

```bash
npm run test
npm run build
```

Mock 数据、登录角色、筛选条件和语言选择保存在 `localStorage`。

API 模式由 Helm 运行时配置启用，前端与后端共用 `src/types.ts` 中定义的 JSON 契约。

## 镜像与 Helm

项目包含多阶段、非 root Nginx 镜像和可配置 Helm Chart：

```bash
docker build -t spark-control-center:0.1.3 .
docker build -f backend/Dockerfile -t spark-control-center-backend:0.1.3 .
helm upgrade --install spark-console ./charts/spark-control-center \
  --namespace spark-console --create-namespace
```

完整的 API、PostgreSQL、Prometheus、OIDC、RBAC、Ingress 与 Secret 配置说明见 [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md)。
