# Container and Helm deployment

## Build and publish the UI image

```bash
docker build -t spark-control-center:1.1.0 .
```

For an air-gapped build with a locally cached Nginx base image:

```bash
npm ci
npm run build
docker build -f Dockerfile.prebuilt -t spark-control-center:1.1.0 .
```

The image listens on port `8080`, serves `/healthz`, runs as UID 101, and supports a read-only root filesystem. Helm replaces `/usr/share/nginx/html/config/config.js` at runtime, so one immutable image can be promoted through environments.

## Build and publish the backend image

Use the repository root as Docker build context:

```bash
docker build -f backend/Dockerfile \
  -t spark-control-center-backend:1.1.0 .
```

The backend runs as UID 65532 on port `8081`. On startup it connects to PostgreSQL and creates the audit/history tables plus `local_users`, `local_user_sessions`, and `application_settings`. The configured database user therefore needs table/index creation permission in `spark_console_db`.

## Prometheus resolution for short Spark jobs

The console keeps a one-hour history but evaluates it at a configurable 15-second step and uses a one-minute CPU rate window. Prometheus must also scrape kubelet/cAdvisor at 15 seconds; reducing only the console query step cannot create samples that Prometheus never ingested.

For a `prometheus-community/prometheus` installation, keep the installed chart version while applying the provided override:

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

The production example uses the existing `spark-postgres-secret` keys `POSTGRES_USER` and `POSTGRES_PASSWORD`, and runs with `runtimeConfig.auth.mode=oidc`. OIDC user records and server-side sessions are stored in PostgreSQL. Secrets and identity-provider tokens are never rendered into browser configuration or stored in the browser.

Create only the Keycloak client Secret before installing the production profile:

```bash
kubectl -n spark-console create secret generic spark-console-oidc \
  --from-literal=client-secret='REPLACE_WITH_KEYCLOAK_CLIENT_SECRET' \
  --dry-run=client -o yaml | kubectl apply -f -
```

If Keycloak uses an internal CA, copy its ConfigMap into the release namespace because Kubernetes ConfigMaps cannot be mounted across namespaces:

```bash
oidc_ca_file="$(mktemp)"
trap 'rm -f "${oidc_ca_file}"' EXIT

kubectl -n identity get configmap oidc-ca -o json | \
  jq -r '.data | to_entries[0].value' > "${oidc_ca_file}"

kubectl -n spark-console create configmap oidc-ca \
  --from-file=ca.crt="${oidc_ca_file}" \
  --dry-run=client -o yaml | kubectl apply -f -
```

Set `oidc.ca.existingConfigMap` to that ConfigMap name and `oidc.ca.key` to `ca.crt`. The chart mounts the selected PEM read-only and automatically sets `OIDC_CA_FILE`; no custom `extraVolumes` configuration is required.

```bash
helm upgrade --install spark-console ./charts/spark-control-center \
  --namespace spark-console --create-namespace \
  -f ./charts/spark-control-center/examples/values-production.yaml
```

The production profile enables the backend, same-origin `/api` proxy, real Kubernetes/Prometheus data, PostgreSQL audit storage, and OIDC authentication. It does not require or bootstrap a local administrator. The first Keycloak user in `oidc.adminGroups` is automatically persisted as an administrator on login.

For an intentionally local-account deployment, set `runtimeConfig.auth.mode=local`, keep `oidc.enabled=false`, and configure `backend.auth.initialAdmin.existingSecret` or `backend.auth.initialAdmin.createSecret=true`. Only local mode requires an initial administrator password.

## Backend HTTP contract

The `api.baseUrl` prefix is omitted below. JSON uses the TypeScript models in `src/types.ts`.

| Method | Path | Response/purpose |
| --- | --- | --- |
| `GET` | `/v1/dashboard/summary?from={RFC3339}&to={RFC3339}` | Live `DashboardSummary` plus PostgreSQL-backed historical submissions/failures |
| `GET` | `/v1/applications` | `SparkApplication[]`; accepts keyword/state/owner/namespace |
| `GET` | `/v1/stream` | Server-Sent Events stream backed by Kubernetes Watch; automatically reconnectable |
| `GET` | `/v1/namespaces/{namespace}/applications/{name}` | Complete `SparkApplication` detail |
| `POST` | `/v1/namespaces/{namespace}/applications/dry-run` | Admin only; Kubernetes server-side dry-run and normalized YAML preview from body `{ yaml }` |
| `GET` | `/v1/namespaces/{namespace}/applications/{name}/prepare?mode=clone\|retry` | Admin only; return a sanitized manifest with a derived name |
| `POST` | `/v1/namespaces/{namespace}/applications` | Admin only; validate and create a SparkApplication from body `{ yaml }` |
| `POST` | `/v1/namespaces/{namespace}/applications/{name}/kill` | Admin only; terminate a `RUNNING` or `SUBMITTED` application while retaining its CR and failed Driver Pod; body `{ reason }` |
| `GET` | `/v1/namespaces/{namespace}/applications/{name}/executors/{pod}/logs` | Loki-backed Executor logs; accepts RFC3339 `from`/`to`, `direction=forward|backward`, and `limit` |
| `GET` | `/v1/namespaces/{namespace}/applications/{name}/spark-ui/{path...}` | Same-origin reverse proxy to the in-cluster Driver UI Service |
| `DELETE` | `/v1/namespaces/{namespace}/applications/{name}` | Admin only; delete a terminal application; body `{ reason }` |
| `GET` | `/v1/audit` | Admin only; `OperationAudit[]` |
| `POST` | `/v1/auth/login` | Local mode only: authenticate `{ username, password }` and set an HttpOnly session cookie |
| `GET` | `/v1/auth/oidc/login` | OIDC mode: start Authorization Code + PKCE login |
| `GET` | `/v1/auth/oidc/callback` | Validate the OIDC callback, synchronize the PostgreSQL user, and create a session |
| `GET` | `/v1/auth/me` | Return the authenticated user |
| `POST` | `/v1/auth/logout` | Revoke the database session and clear the cookie |
| `GET/PATCH` | `/v1/profile` | Read/update the current user's display name, email, or password |
| `GET/POST` | `/v1/users` | Admin only; list or create users |
| `PATCH/DELETE` | `/v1/users/{id}` | Admin only; update, reset password, disable, or delete a user |
| `GET` | `/healthz` | Backend health check |
| `GET` | `/metrics` | Optional Prometheus metrics |

All `/v1` data endpoints require a valid database session. Viewer accounts are read-only; the backend independently enforces administrator access for submission, termination, deletion, audit, and user-management APIs. Every data and mutation route also enforces the authenticated user's namespace assignment. An empty namespace list means all Helm-configured namespaces for backward compatibility. Client-supplied operator names are ignored, and operation audit records use the authenticated username. Passwords are bcrypt-hashed, only SHA-256 hashes of random session tokens are stored, session cookies are HttpOnly/SameSite, and cross-origin state-changing requests are rejected. The last active administrator cannot be disabled, demoted, or deleted, and users cannot delete their own account.

Terminate TLS at the Ingress before using password login outside a trusted test network. The frontend proxy preserves the original `X-Forwarded-Proto`/host so the backend marks the session cookie `Secure` on HTTPS deployments.

Kill/delete state guards are enforced by the backend as well as the UI. Kubernetes has no generic SparkApplication kill subresource. Both `RUNNING` and `SUBMITTED` applications can be terminated, including a Driver stuck in `ImagePullBackOff`. Kill first patches `spec.restartPolicy.type` to `Never`, then patches the Driver Pod with an expired `activeDeadlineSeconds`. Kubelet terminates the Driver container and retains the failed Pod so its status, events, and Kubernetes logs remain available. Correlated Executor Pods are force-deleted to stop compute. Delete later removes the terminal SparkApplication CR using foreground propagation, allowing owner-referenced Driver resources to be deleted with it. Exact reconciliation timing remains controlled by Spark Operator.

The submission API accepts at most 1 MiB, requires the configured SparkApplication API version and kind, removes server-owned metadata/status, and rejects a manifest namespace that differs from the selected, allow-listed namespace. Before submission, the UI calls Kubernetes with `dryRun=All` and displays the original and API-normalized YAML side by side. Clone/retry uses the same sanitizer and derives a new DNS-safe name. It records the authenticated username in the `spark-control-center.io/submitted-by` annotation; applications created elsewhere display owner `unknown`. The backend ServiceAccount gets namespace-scoped `sparkapplications.create` when `runtimeConfig.features.submit=true`.

For a running application, the UI embeds Driver Spark UI through the backend proxy using `status.driverInfo.webUIServiceName` and `webUIPort`; no `pods/exec` permission or local port-forward is used. The proxy rewrites redirects, absolute in-cluster Service URLs, and root-relative HTML links so the browser never has to resolve `*.svc` DNS. For terminal applications, the button opens `${runtimeConfig.historyServer.baseUrl}/<Spark ID>/jobs/` only when the history server is enabled and both `spark.eventLog.enabled=true` and `spark.eventLog.dir` are present in `spec.sparkConf`.

## Backend environment contract

When `backend.enabled=true`, the chart injects:

- Kubernetes: `KUBERNETES_CLUSTER_NAME`, `WATCH_NAMESPACES` and a scoped ServiceAccount token.
- Spark Operator: `SPARKAPPLICATION_API_VERSION`.
- Prometheus: `PROMETHEUS_URL`, `PROMETHEUS_QUERY_TIMEOUT`, `PROMETHEUS_LOOKBACK`, `PROMETHEUS_QUERY_STEP`, `PROMETHEUS_CPU_RATE_WINDOW`.
- Loki, when enabled: `LOKI_URL`, `LOKI_QUERY_TIMEOUT`, `LOKI_MAX_ENTRIES`. Executor queries use the Alloy labels `namespace`, `pod`, and `spark_role=executor`.
- Dashboard history: `HISTORY_DEFAULT_DAYS`; frontend polling uses `runtimeConfig.dashboard.refreshIntervalSeconds`.
- PostgreSQL: `DATABASE_HOST`, `DATABASE_PORT`, `DATABASE_NAME`, `DATABASE_SSLMODE`, `DATABASE_USERNAME`, `DATABASE_PASSWORD`.
- Authentication: `AUTH_MODE`, `AUTH_SESSION_TTL`, and `AUTH_BCRYPT_COST`; local mode additionally uses `AUTH_INITIAL_ADMIN_USERNAME`, `AUTH_INITIAL_ADMIN_DISPLAY_NAME`, and Secret-backed `AUTH_INITIAL_ADMIN_PASSWORD`.
- Optional OIDC: `OIDC_ENABLED`, `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, Secret-backed `OIDC_CLIENT_SECRET`, `OIDC_SCOPES`, `OIDC_USERNAME_CLAIM`, `OIDC_GROUPS_CLAIM`, `OIDC_ADMIN_GROUPS`, `OIDC_AUTO_CREATE`, `OIDC_REDIRECT_URL`, `OIDC_STATE_TTL`, and optional `OIDC_CA_FILE`.

Use `oidc.ca.existingConfigMap` for the Keycloak trust bundle. `backend.extraEnv`, `extraVolumes`, and `extraVolumeMounts` remain available for workload identity or additional provider-specific settings.

## Configuration boundaries

- `runtimeConfig.*` is public and appears in the browser. Client IDs and issuer URLs are safe here; secrets are not.
- `runtimeConfig.timeZone` accepts an IANA name such as `Asia/Shanghai`; leave it empty to display all timestamps in the browser's timezone. The same conversion is used on list, detail, audit, user, overview, and Loki log screens.
- Application resource history can query an explicit time range. The backend queries Prometheus with the selected RFC3339 range and `prometheus.queryStep`; historical Driver/Executor names are taken from SparkApplication status even after Pods have been removed.
- `database.credentials.*`, `backend.auth.initialAdmin.*`, and `oidcSecret.*` are backend-only Secret settings. Prefer existing Secrets outside test environments.
- `rbac.clusterWide=false` creates namespace-scoped Roles. Use cluster-wide RBAC only for a genuine multi-namespace/multi-tenant requirement.
- `runtimeConfig.historyServer.enabled/baseUrl` controls terminal-job links. The production example uses `https://spark-history.example.com/history` as a placeholder.
- `runtimeConfig.auth.mode=oidc` selects OIDC-only login and does not require an initial local administrator. OIDC identities are keyed by `(issuer, subject)`, auto-created when enabled, and receive `admin` only when a configured groups claim matches `oidc.adminGroups`; all other OIDC users are `viewer`.
- The chart does not install PostgreSQL, Prometheus, Keycloak, or Spark Operator.

## Keycloak client for optional OIDC login

Create a confidential OpenID Connect client named `spark-control-center` with **Client authentication** and **Standard flow** enabled. Configure:

- Valid redirect URI: `https://spark-console.example.com/api/v1/auth/oidc/callback`
- Web origin: `https://spark-console.example.com`
- A Group Membership mapper that adds the `groups` claim to the ID token. The production values map `spark-console-admins` to the Spark Console `admin` role.

Store the generated Keycloak client secret in Kubernetes; `spark-console-oidc` is the Kubernetes Secret name, while its `client-secret` value is the actual Keycloak client secret:

```bash
kubectl -n spark-console create secret generic spark-console-oidc \
  --from-literal=client-secret='REPLACE_WITH_KEYCLOAK_CLIENT_SECRET' \
  --dry-run=client -o yaml | kubectl apply -f -
```

OIDC uses Authorization Code Flow with PKCE, validates issuer/audience/signature/expiry/nonce, persists only one-time login state and the resulting Spark Console session, and does not store Keycloak tokens.

## Real data behavior

- SparkApplications are read directly from Kubernetes in `rbac.namespaces`; Pods, Driver logs, and core/v1 Events are correlated to each application.
- A Kubernetes Watch per configured namespace feeds a same-origin SSE stream. The UI refreshes affected views immediately and retains periodic polling for watch reconnection or proxy timeout recovery.
- PostgreSQL snapshots preserve deleted application details, generated lifecycle events, and rule-based diagnosis for common image-pull, scheduling, mount, OOM, lost-executor, and RBAC failures.
- CPU uses `container_cpu_usage_seconds_total`; memory uses `container_memory_working_set_bytes`; cluster capacity uses `kube_node_status_allocatable`.
- Executor logs use Loki `query_range`, remain available after Executor Pod deletion, and can be ordered oldest-first or newest-first for a selected time range.
- A Prometheus failure leaves Kubernetes application data available with live metrics omitted. Kubernetes and PostgreSQL are readiness dependencies.
- `/healthz` checks process liveness; `/readyz` verifies Kubernetes access and PostgreSQL connectivity.
