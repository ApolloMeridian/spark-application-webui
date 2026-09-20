# Container and Helm deployment

## Build and publish the UI image

```bash
docker build -t 10.0.32.115:5000/library/platform/spark-control-center:0.1.3 .
docker push 10.0.32.115:5000/library/platform/spark-control-center:0.1.3
```

For an air-gapped build with a locally cached Nginx base image:

```bash
npm ci
npm run build
docker build -f Dockerfile.prebuilt -t spark-control-center:0.1.3 .
```

The image listens on port `8080`, serves `/healthz`, runs as UID 101, and supports a read-only root filesystem. Helm replaces `/usr/share/nginx/html/config/config.js` at runtime, so one immutable image can be promoted through environments.

## Build and publish the backend image

Use the repository root as Docker build context:

```bash
docker build -f backend/Dockerfile \
  -t 10.0.32.115:5000/library/platform/spark-control-center-backend:0.1.3 .
docker push 10.0.32.115:5000/library/platform/spark-control-center-backend:0.1.3
```

The backend runs as UID 65532 on port `8081`. On startup it connects to PostgreSQL and creates the `operation_audit` and `spark_application_history` tables and indexes when absent. The configured database user therefore needs table/index creation permission in `spark_console_db`.

## Prometheus resolution for short Spark jobs

The console keeps a one-hour history but evaluates it at a configurable 15-second step and uses a one-minute CPU rate window. Prometheus must also scrape kubelet/cAdvisor at 15 seconds; reducing only the console query step cannot create samples that Prometheus never ingested.

For the `prometheus-community/prometheus` release shown in this environment, keep the installed chart version while applying the provided override:

```bash
PROMETHEUS_CHART_VERSION="$(helm list -n prometheus -o json | jq -r '.[] | select(.name == "prometheus") | .chart | sub("^prometheus-"; "")')"

helm upgrade prometheus prometheus-community/prometheus \
  --namespace prometheus \
  --version "${PROMETHEUS_CHART_VERSION}" \
  --reuse-values \
  -f ./charts/spark-control-center/examples/prometheus-values-high-resolution.yaml
```

Verify the rendered Prometheus configuration after the upgrade:

```bash
kubectl -n prometheus get configmap prometheus-server \
  -o jsonpath='{.data.prometheus\.yml}' | \
  grep -n -E 'scrape_interval|job_name|cadvisor'
```

## Mock deployment

```bash
helm upgrade --install spark-console ./charts/spark-control-center \
  --namespace spark-console --create-namespace \
  -f ./charts/spark-control-center/examples/values-mock.yaml
```

## API/OIDC deployment

The production example uses the existing `spark-postgres-secret` keys `POSTGRES_USER` and `POSTGRES_PASSWORD`. OIDC currently runs in explicit admin-bypass mode, so the Keycloak secret is not mounted yet. Secrets are never rendered into browser configuration.

```bash
helm upgrade --install spark-console ./charts/spark-control-center \
  --namespace spark-console --create-namespace \
  -f ./charts/spark-control-center/examples/values-production.yaml
```

The production profile enables the backend, same-origin `/api` proxy, real Kubernetes/Prometheus data, PostgreSQL audit storage, and the temporary admin session. The backend queries `sparkoperator.k8s.io/v1beta2` by default; override `backend.sparkApplicationAPIVersion` if the installed Spark Operator exposes another version.

## Backend HTTP contract

The `api.baseUrl` prefix is omitted below. JSON uses the TypeScript models in `src/types.ts`.

| Method | Path | Response/purpose |
| --- | --- | --- |
| `GET` | `/v1/dashboard/summary?from={RFC3339}&to={RFC3339}` | Live `DashboardSummary` plus PostgreSQL-backed historical submissions/failures |
| `GET` | `/v1/applications` | `SparkApplication[]`; accepts keyword/state/owner/namespace |
| `GET` | `/v1/namespaces/{namespace}/applications/{name}` | Complete `SparkApplication` detail |
| `POST` | `/v1/namespaces/{namespace}/applications/{name}/kill` | Kill a `RUNNING` application; `OperationAudit`; body `{ reason, requestedBy }` |
| `DELETE` | `/v1/namespaces/{namespace}/applications/{name}` | Delete a terminal application; `OperationAudit`; body `{ reason, requestedBy }` |
| `GET` | `/v1/audit` | `OperationAudit[]` |
| `GET` | `/v1/auth/login?returnUrl=...` | Temporary admin-bypass login redirect |
| `GET` | `/v1/auth/me` | Temporary `{ "username": "admin", "role": "admin" }` session |
| `GET` | `/v1/auth/logout?returnUrl=...` | Clear the placeholder session cookie |
| `GET` | `/healthz` | Backend health check |
| `GET` | `/metrics` | Optional Prometheus metrics |

With `backend.auth.adminBypass=true`, `/v1/auth/me` reports every caller as `admin`; the API is not protected and must remain on a trusted test network. Login/logout only create and clear a placeholder HttpOnly cookie and validate return URLs. If bypass is disabled before real OIDC is implemented, the backend deliberately rejects authentication instead of silently falling back.

Kill/delete state guards are enforced by the backend as well as the UI. Kubernetes has no generic SparkApplication kill subresource, so Kill uses foreground deletion of a `RUNNING` CR to terminate its Driver and Executors; Delete exposes the same Kubernetes deletion mechanism only for terminal CR cleanup. They remain distinct API and audit operations.

## Backend environment contract

When `backend.enabled=true`, the chart injects:

- Kubernetes: `KUBERNETES_CLUSTER_NAME`, `WATCH_NAMESPACES` and a scoped ServiceAccount token.
- Spark Operator: `SPARKAPPLICATION_API_VERSION`.
- Prometheus: `PROMETHEUS_URL`, `PROMETHEUS_QUERY_TIMEOUT`, `PROMETHEUS_LOOKBACK`, `PROMETHEUS_QUERY_STEP`, `PROMETHEUS_CPU_RATE_WINDOW`.
- Dashboard history: `HISTORY_DEFAULT_DAYS`; frontend polling uses `runtimeConfig.dashboard.refreshIntervalSeconds`.
- PostgreSQL: `DATABASE_HOST`, `DATABASE_PORT`, `DATABASE_NAME`, `DATABASE_SSLMODE`, `DATABASE_USERNAME`, `DATABASE_PASSWORD`.
- Authentication: `AUTH_MODE`, `AUTH_ADMIN_BYPASS`; OIDC values and secret are mounted only when bypass is disabled.

Use `backend.extraEnv`, `extraVolumes`, and `extraVolumeMounts` for provider-specific certificates, workload identity, or additional settings.

## Configuration boundaries

- `runtimeConfig.*` is public and appears in the browser. Client IDs and issuer URLs are safe here; secrets are not.
- `database.credentials.*` and `oidcSecret.*` are backend-only Secret references.
- `rbac.clusterWide=false` creates namespace-scoped Roles. Use cluster-wide RBAC only for a genuine multi-namespace/multi-tenant requirement.
- The chart does not install PostgreSQL, Prometheus, Keycloak, or Spark Operator.

## Real data behavior

- SparkApplications are read directly from Kubernetes in `rbac.namespaces`; Pods, Driver logs, and core/v1 Events are correlated to each application.
- CPU uses `container_cpu_usage_seconds_total`; memory uses `container_memory_working_set_bytes`; cluster capacity uses `kube_node_status_allocatable`.
- A Prometheus failure leaves Kubernetes application data available with live metrics omitted. Kubernetes and PostgreSQL are readiness dependencies.
- `/healthz` checks process liveness; `/readyz` verifies Kubernetes access and PostgreSQL connectivity.
