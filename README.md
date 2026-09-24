# Spark Control Center

[简体中文](README.zh-CN.md) · English

Spark Control Center is a web console for operating Apache Spark applications on Kubernetes. It combines application lifecycle operations, live and historical resource metrics, Driver UI access, persisted Executor logs, audit records, and role-based access in one deployable package.

## Highlights

- Discover SparkApplications across configured namespaces, with status, owner, lifecycle timestamps, and resource usage.
- Inspect Driver/Executor requests, live usage, peaks, scheduling, Kubernetes events, logs, YAML, and selectable Prometheus history ranges.
- Proxy a running Driver Spark UI through the backend; link terminal jobs to Spark History Server when event logging is enabled.
- Query retained Executor logs from Loki even after Kubernetes Pods are removed.
- Submit SparkApplication YAML, terminate `RUNNING` or stuck `SUBMITTED` jobs while retaining the failed Driver Pod, and delete terminal records.
- Validate submissions through Kubernetes server-side dry-run and compare the original YAML with the normalized manifest before creating it.
- Clone or retry an application from a sanitized manifest, with server-owned metadata and runtime status removed.
- Stream Kubernetes Watch changes to the browser over SSE, while retaining periodic refresh as a recovery path.
- Authenticate with PostgreSQL local accounts or optional OIDC/Keycloak; enforce `viewer` and `admin` permissions plus per-user namespace access in both UI and API.
- Persist users, sessions, lifecycle snapshots, failure diagnostics, and operation audit records in PostgreSQL.
- Deploy with the included Helm chart and namespace-scoped Kubernetes RBAC.

## Screenshots

| Overview | Applications |
| --- | --- |
| ![Operations overview](docs/images/overview-1440.png) | ![Spark application list](docs/images/applications-1440.png) |

| Application resources | Executor logs |
| --- | --- |
| ![Application detail and historical resource usage](docs/images/detail-1440.png) | ![Persisted Executor logs](docs/images/executor-logs-1440.png) |

![SparkApplication YAML submission](docs/images/submit-1440.png)

All screenshots use deterministic mock data. They contain no production cluster details or credentials.

## Architecture

- **Frontend:** React, TypeScript, Vite, Ant Design, ECharts
- **Backend:** Go HTTP service
- **Integrations:** Kubernetes Spark Operator API, Prometheus, Loki, PostgreSQL, optional OIDC
- **Deployment:** non-root container images, Nginx same-origin API proxy, Helm and scoped RBAC

The browser talks only to the same-origin `/api` endpoint. Kubernetes, Prometheus, Loki, PostgreSQL, and OIDC credentials remain server-side.

## Local development

Requirements: Node.js 22+ and npm.

```bash
npm ci
npm run dev
```

The default configuration uses browser-local mock data and mock authentication.

Run validation:

```bash
npm test
npm run build
```

Backend validation requires Go 1.24+:

```bash
cd backend
go test ./...
go vet ./...
```

## Container images

Build images without a registry prefix:

```bash
docker build -t spark-control-center:1.1.0 .
docker build -f backend/Dockerfile -t spark-control-center-backend:1.1.0 .
```

GitHub Release `v1.1.0` contains Docker-loadable image tar files and the packaged Helm chart.

```bash
docker load -i spark-control-center-1.1.0.tar
docker load -i spark-control-center-backend-1.1.0.tar
```

## Helm deployment

The default values run the mock frontend. For a real deployment, copy and customize the placeholder-only production example:

```bash
cp charts/spark-control-center/examples/values-production.yaml values-production.yaml

helm upgrade --install spark-console ./charts/spark-control-center \
  --namespace spark-console \
  --create-namespace \
  -f values-production.yaml
```

Keep environment-specific deployment settings in an untracked values file. These settings include image locations, watched namespaces, database credential references, observability endpoints, Ingress, and identity-provider metadata.

Never commit actual credentials, access tokens, certificates, kubeconfigs, or production values.

See [Deployment and configuration](docs/DEPLOYMENT.md) for the complete interface, security model, RBAC behavior, Keycloak setup, and high-resolution Prometheus configuration.

## Permissions

- `viewer`: read permitted namespaces, applications, metrics, events, logs, YAML, and Spark UI; manage their own profile.
- `admin`: all viewer permissions plus submit, terminate, delete, audit, and user management within permitted namespaces. Administrators can assign namespace access; an empty assignment retains backward-compatible access to every configured namespace.

The backend independently checks every privileged operation; hiding a UI button is not treated as authorization.

## Security

- No production credentials are included in this repository or container images.
- Passwords are bcrypt-hashed; random session tokens are stored only as SHA-256 hashes.
- Sessions use HttpOnly/SameSite cookies and state-changing API calls enforce same-origin checks.
- OIDC uses Authorization Code Flow with PKCE and validates issuer, signature, audience, expiry, state, and nonce.
- Kubernetes access is limited to explicitly configured namespaces by default.

## License

Licensed under the [Apache License 2.0](LICENSE).
