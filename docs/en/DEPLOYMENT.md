# Container and Helm deployment

## Build and publish the UI image

```bash
docker build -t spark-control-center:1.5.5 .
```

For an air-gapped build with a locally cached Nginx base image:

```bash
npm ci
npm run build
docker build -f Dockerfile.prebuilt -t spark-control-center:1.5.5 .
```

The image listens on port `8080`, serves `/healthz`, runs as UID 101, and supports a read-only root filesystem. Helm replaces `/usr/share/nginx/html/config/config.js` at runtime, so one immutable image can be promoted through environments.

## Build and publish the backend image

Use the repository root as Docker build context:

```bash
export DOCKER_BUILDKIT=1
docker build -f backend/Dockerfile \
  -t spark-control-center-backend:1.5.5 .
```

The backend runs as UID 65532 on port `8081`. Startup migrations maintain identity/sessions, tenant-scoped templates/alerts, audit/history, workflow/schedule/run/backfill state and alert coordination. The optional storage process maintains grants, operations and bootstrap records in the same identity database. The database user needs schema/table/index permissions; back up PostgreSQL before upgrading. See the complete [table inventory and API/environment reference](REFERENCE.md).

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

Packaged defaults reference an existing PostgreSQL Secret with `username` and `password` keys and use `runtimeConfig.auth.mode=local`. Configure your own database Secret reference and initial administrator password Secret. For OIDC-only deployment, change the mode to `oidc` and enable/configure the provider. OIDC user records and server-side sessions are stored in PostgreSQL. Secrets and identity-provider tokens are never rendered into browser configuration or stored in the browser.

For OIDC, prepare the Keycloak client Secret in addition to the database Secret:

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

The production template enables the backend, same-origin `/api` proxy and PostgreSQL local login. To use OIDC-only login, set `runtimeConfig.auth.mode=oidc`, enable/configure `oidc`, and supply an explicit `oidc.redirectURL`; no initial local administrator is required then. Members of `oidc.adminGroups` are synchronized as administrators on login.

For an intentionally local-account deployment, set `runtimeConfig.auth.mode=local`, keep `oidc.enabled=false`, and configure `backend.auth.initialAdmin.existingSecret` or `backend.auth.initialAdmin.createSecret=true`. Only local mode requires an initial administrator password.

## Backend HTTP contract

The `api.baseUrl` prefix is omitted below. These are common endpoints; the source-checked [complete reference](REFERENCE.md) also covers templates, favorites, alerts, workflows, schedules, backfills, storage and Driver log windows. JSON uses the models in `src/types.ts`, `src/workflows.ts` and `src/objectStorage.ts`.

| Method | Path | Response/purpose |
| --- | --- | --- |
| `GET` | `/v1/dashboard/summary?from={RFC3339}&to={RFC3339}` | Live `DashboardSummary` plus PostgreSQL-backed historical submissions/failures |
| `GET` | `/v1/applications` | Paged `ApplicationList`; accepts keyword/state/owner/namespace, page/pageSize and sorting |
| `GET` | `/v1/stream` | Server-Sent Events stream backed by Kubernetes Watch; automatically reconnectable |
| `GET` | `/v1/namespaces/{namespace}/applications/{name}` | Complete `SparkApplication` detail |
| `POST` | `/v1/namespaces/{namespace}/applications/dry-run` | Requires submit permission; Kubernetes dry-run and normalized YAML preview from `{ yaml }` |
| `GET` | `/v1/namespaces/{namespace}/applications/{name}/prepare?mode=clone\|retry` | Requires submit permission; sanitized manifest with a derived name |
| `POST` | `/v1/namespaces/{namespace}/applications` | Requires submit permission; create from `{ yaml }` |
| `POST` | `/v1/namespaces/{namespace}/applications/{name}/kill` | Requires kill permission; retain the CR/failed Driver; body `{ reason }` |
| `GET` | `/v1/namespaces/{namespace}/applications/{name}/executors/{pod}/logs` | Loki-backed Executor logs; accepts RFC3339 `from`/`to`, `direction=forward|backward`, and `limit` |
| `GET` | `/v1/namespaces/{namespace}/applications/{name}/spark-ui/{path...}` | Same-origin reverse proxy to the in-cluster Driver UI Service |
| `DELETE` | `/v1/namespaces/{namespace}/applications/{name}` | Requires delete permission; delete a terminal application; body `{ reason }` |
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
| `GET` | `/readyz` | Backend dependency readiness |
| `GET` | `/metrics` | Optional Prometheus metrics |

All `/v1` data endpoints require a valid database session. Viewer accounts start with minimum permissions; tenant policies may explicitly grant operational capabilities. User/tenant management and full audit require an administrator. Namespace, tenant and storage-prefix permissions are independent checks, not UI-only controls. An empty namespace list means all Helm-configured namespaces. Audit actors come from the session, never a request body. Passwords use bcrypt and session tokens are stored as SHA-256 hashes. See [hardening and upgrade requirements](HARDENING.md) for CSRF, login limits and tenant migration.

Terminate TLS at the Ingress. Configure `backend.publicURL` to the canonical external origin, or let the Chart derive it from the explicit OIDC callback / Ingress host and TLS. Backend code ignores client-supplied Forwarded headers. HTTPS public origins produce Secure cookies. OIDC requires an explicit callback URL matching the identity provider exactly.

Kill/delete state guards are enforced by the backend as well as the UI. Kubernetes has no generic SparkApplication kill subresource. Both `RUNNING` and `SUBMITTED` applications can be terminated, including a Driver stuck in `ImagePullBackOff`. Kill first patches `spec.restartPolicy.type` to `Never`, then patches the Driver Pod with an expired `activeDeadlineSeconds`. Kubelet terminates the Driver container and retains the failed Pod so its status, events, and Kubernetes logs remain available. Correlated Executor Pods are force-deleted to stop compute. Delete later removes the terminal SparkApplication CR using foreground propagation, allowing owner-referenced Driver resources to be deleted with it. Exact reconciliation timing remains controlled by Spark Operator.

The submission API accepts at most 1 MiB, requires the configured SparkApplication API version and kind, removes server-owned metadata/status, and rejects a manifest namespace that differs from the selected, allow-listed namespace. Before submission, the UI calls Kubernetes with `dryRun=All` and displays the original and API-normalized YAML side by side. Clone/retry uses the same sanitizer and derives a new DNS-safe name. It records the authenticated username in the `spark-control-center.io/submitted-by` annotation; applications created elsewhere display owner `unknown`. The backend ServiceAccount gets namespace-scoped `sparkapplications.create` when `runtimeConfig.features.submit=true`.

For a running application, the UI embeds Driver Spark UI through the backend proxy using `status.driverInfo.webUIServiceName` and `webUIPort`; no `pods/exec` permission or local port-forward is used. The proxy rewrites redirects, absolute in-cluster Service URLs, and root-relative HTML links so the browser never has to resolve `*.svc` DNS. For terminal applications, the button opens `${runtimeConfig.historyServer.baseUrl}/<Spark ID>/jobs/` only when the history server is enabled and both `spark.eventLog.enabled=true` and `spark.eventLog.dir` are present in `spec.sparkConf`.

## Backend environment contract

When `backend.enabled=true`, the chart injects:

The list below is a quick overview, not an exhaustive contract. See the [complete environment reference](REFERENCE.md) for Kubernetes external access, database URL/pool settings, alerts, workflows and the storage process.

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

### Driver log windows

The Driver logs tab queries independently of the detail snapshot's 5000-line diagnostic tail. Select **Latest logs** or a **Line range**, with next/previous windows and up to 10000 lines / 4 MiB per request. Kubernetes offsets are implemented by streaming past earlier lines, not loading the full log into memory; very late ranges can take longer and remain subject to request timeouts. Numbers refer to the container log currently retained by Kubernetes, not an immutable lifetime log. Search/level filters apply to the loaded window and preserve its line numbers.

For rotated/deleted Driver logs, choose **Loki retained logs**, a time range and sort direction. Alloy must ingest `namespace`, `pod` and `spark_role="driver"` labels. Loki's configured entry limit still applies per query; narrow the time range if the limit warning appears. Logs neither retained by Kubernetes nor ingested by Loki cannot be recovered. Read-only log queries keep namespace/tenant authorization and do not generate operation audits.

- SparkApplications are read directly from Kubernetes in `rbac.namespaces`; Pods, Driver logs, and core/v1 Events are correlated to each application.
- A Kubernetes Watch per configured namespace feeds a same-origin SSE stream. The UI refreshes affected views immediately and retains periodic polling for watch reconnection or proxy timeout recovery.
- PostgreSQL snapshots preserve deleted application details, generated lifecycle events, and rule-based diagnosis for common image-pull, scheduling, mount, OOM, lost-executor, and RBAC failures.
- CPU usage comes from `container_cpu_usage_seconds_total`. Memory usage comes from `container_memory_working_set_bytes`.
- Executor logs use Loki `query_range`, remain available after Executor Pod deletion, and can be ordered oldest-first or newest-first for a selected time range.
- A Prometheus failure leaves Kubernetes application data available with live metrics omitted. Kubernetes and PostgreSQL are readiness dependencies.
- `/healthz` checks process liveness; `/readyz` verifies Kubernetes access and PostgreSQL connectivity.

### Overview resource pools

The fixed pool shows requests and usage from scheduled Spark Pods on fixed nodes. Its capacity is the sum of those nodes' Kubernetes `status.allocatable`. The progress bar shows the share of that capacity requested by Spark Pods in the fixed pool. The elastic pool shows requests and usage from its scheduled Spark Pods. Both values are zero when the pool has no Spark Pods. Unscheduled Pods appear in the scheduling panel.

The backend recognizes TKE supernodes by `node.kubernetes.io/instance-type=eklet` and ACK virtual nodes by `type=virtual-kubelet` or known virtual-node taints. For GKE, use `cloud.google.com/gke-nodepool` to match an autoscaling pool by name. Kubernetes Node labels can classify pools across all three platforms.

```yaml
overview:
  resourcePools:
    enabled: true
    autoDetectVirtualNodes: false
    fixedMatchLabels:
      capacity.example.com/pool: fixed
    elasticMatchLabels:
      - capacity.example.com/pool: elastic
```

Apply the labels in each node pool's Kubernetes node configuration. Each rule requires all its labels to match. Any rule in `elasticMatchLabels` can match. Elastic rules take precedence. An empty `fixedMatchLabels: {}` places the remaining nodes in the fixed pool. Enable `autoDetectVirtualNodes` to combine the built-in detection with custom elastic rules.

The backend refreshes the node inventory every 30 seconds. The chart grants its ServiceAccount cluster-scoped `nodes/list` access through a dedicated role and binding. Grant that permission in your managed RBAC when using `rbac.create: false`. The overview displays aggregate application usage when the node inventory is unavailable. Set `overview.resourcePools.enabled: false` to disable pool reporting.

## Tenant permissions in 1.5.3

Production chart defaults use the real backend and PostgreSQL login, not Mock. Prepare the initial administrator password Secret and database Secret referenced in your single values file. To use OIDC-only login, set runtimeConfig.auth.mode=oidc, enable oidc, configure the provider/client Secret and callback; no initial local administrator Secret is then required. Storage remains disabled unless explicitly enabled.

New admin/viewer accounts default to their corresponding built-in tenants. Existing user/project assignments are preserved. Configure tenant access in User management; the API and scheduler read policies from PostgreSQL. See [tenant permissions](WORKFLOWS.md#tenant-and-permission-boundaries) and [optional storage](OBJECT-STORAGE.md). All release assets use unprefixed image names: frontend/backend/storage TAR plus Helm TGZ, without a checksum file.

## Application instances and termination in 1.5.3

Application details use `/applications/<namespace>/<Kubernetes UID>`. Spark IDs may be absent before startup and can change on an Operator retry, so they are displayed as run information rather than used as the resource identity. Old name links resolve to the live or latest visible instance. Deleting a CR retains its observed PostgreSQL snapshot and same-name replacements appear separately. Only the paginated application browser includes archived instances; resource totals and scheduler reconciliation use live Kubernetes state.

An application removed from Kubernetes while active appears as `DELETED` in history. Archived applications with an observed terminal state retain that state. Administrators can clear the snapshot for a specific UID from the list or details. Clearing the record removes its entry from historical statistics and creates an audit entry.

Historical Loki and Prometheus queries are restricted to the instance lifetime. An archived UID cannot stop/delete or proxy the Spark UI of a replacement. Kubernetes log requests for archived instances return an explicit unavailable response; use retained Loki logs or the saved diagnostic tail. Snapshot collection cannot recover applications it never observed, deleted log segments or expired metrics.

Termination writes `spark-control-center.io/stop-requested-at` and `restartPolicy.type: Never` using UID/revision preconditions, then sets a verified Driver's active deadline and cleans its executors. Backend replicas recover outstanding stop requests and stop resubmitted Drivers for that UID. The console retains the CR and does not delete Drivers to terminate them. Older Spark Operators may independently remove the original Driver when a spec update invalidates the run; use persistent Loki logs. The [Operator controller](https://github.com/kubeflow/spark-operator/blob/v1beta2-1.3.8-3.1.1/pkg/controller/sparkapplication/controller.go) documents that spec-change behavior. While all backends are unavailable, a resubmission already in flight can run until recovery. Clone/retry removes the stop marker and creates a new resource; deleting/recreating a name does not carry a previous UID's stop request.
