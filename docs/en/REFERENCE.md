# API and environment reference

This inventory is generated from route registrations, configuration sources and migrations. Run `npm run docs:check` to detect drift. Helm values remain the deployment interface; backend defaults are defined in `backend/internal/config/config.go`.

## HTTP contract

The public `/api` prefix is omitted. Except health/metrics, login/callback and sandbox CORS preflight, routes require the PostgreSQL session cookie; sandboxed Spark UI subresources instead accept an application-scoped, session-bound read-only ticket. Namespace and tenant policy apply independently of the UI. Mutations require a valid same-origin Origin/Referer; non-browser clients without these headers must use JSON or `X-Requested-With: SparkControlCenter`. Explicit cross-origin requests are always denied. Upload parts use the custom header. Spark UI is GET-only and sandboxed.

JSON models: `src/types.ts`, `src/workflows.ts`, `src/objectStorage.ts` and `backend/internal/domain`. Run actions are `pause`, `resume`, `cancel`, `rerun_failed` and `delete`; template/workflow versions use their separate restore/delete routes. Limits, error behavior and storage tickets are described in the [deployment](DEPLOYMENT.md), [workflow](WORKFLOWS.md) and [storage](OBJECT-STORAGE.md) guides.

| Method | Registered path |
| --- | --- |
| GET | `/healthz` |
| GET | `/metrics` |
| GET | `/readyz` |
| GET | `/v1/alert-rules` |
| POST | `/v1/alert-rules` |
| DELETE | `/v1/alert-rules/{id}` |
| PATCH | `/v1/alert-rules/{id}` |
| GET | `/v1/alerts` |
| PATCH | `/v1/alerts/{id}` |
| GET | `/v1/applications` |
| POST | `/v1/applications/batch/execute` |
| POST | `/v1/applications/batch/preview` |
| GET | `/v1/audit` |
| POST | `/v1/auth/login` |
| POST | `/v1/auth/logout` |
| GET | `/v1/auth/me` |
| GET | `/v1/auth/oidc/callback` |
| GET | `/v1/auth/oidc/login` |
| GET | `/v1/dashboard/summary` |
| GET | `/v1/diagnostics/fingerprints` |
| GET | `/v1/favorites` |
| POST | `/v1/namespaces/{namespace}/applications` |
| DELETE | `/v1/namespaces/{namespace}/applications/{name}` |
| GET | `/v1/namespaces/{namespace}/applications/{name}` |
| GET | `/v1/namespaces/{namespace}/applications/{name}/driver/logs` |
| GET | `/v1/namespaces/{namespace}/applications/{name}/executors/{pod}/logs` |
| PUT | `/v1/namespaces/{namespace}/applications/{name}/favorite` |
| POST | `/v1/namespaces/{namespace}/applications/{name}/kill` |
| GET | `/v1/namespaces/{namespace}/applications/{name}/metrics` |
| GET | `/v1/namespaces/{namespace}/applications/{name}/prepare` |
| GET | `/v1/namespaces/{namespace}/applications/{name}/spark-ui` |
| GET | `/v1/namespaces/{namespace}/applications/{name}/spark-ui/{path...}` |
| OPTIONS | `/v1/namespaces/{namespace}/applications/{name}/spark-ui/{path...}` |
| POST | `/v1/namespaces/{namespace}/applications/dry-run` |
| GET | `/v1/object-storage/connections` |
| POST | `/v1/object-storage/copy` |
| GET | `/v1/object-storage/download` |
| DELETE | `/v1/object-storage/objects` |
| GET | `/v1/object-storage/objects` |
| GET | `/v1/object-storage/operations` |
| GET | `/v1/object-storage/policies` |
| POST | `/v1/object-storage/policies` |
| DELETE | `/v1/object-storage/policies/{id}` |
| GET | `/v1/object-storage/text` |
| POST | `/v1/object-storage/text` |
| PUT | `/v1/object-storage/text` |
| DELETE | `/v1/object-storage/uploads` |
| GET | `/v1/object-storage/uploads` |
| POST | `/v1/object-storage/uploads` |
| POST | `/v1/object-storage/uploads/complete` |
| PUT | `/v1/object-storage/uploads/part` |
| GET | `/v1/profile` |
| PATCH | `/v1/profile` |
| GET | `/v1/projects` |
| POST | `/v1/projects` |
| DELETE | `/v1/projects/{project}` |
| PATCH | `/v1/projects/{project}` |
| GET | `/v1/projects/{project}/backfills` |
| POST | `/v1/projects/{project}/backfills/{id}/cancel` |
| GET | `/v1/projects/{project}/runs` |
| POST | `/v1/projects/{project}/runs/{id}/{action}` |
| GET | `/v1/projects/{project}/schedules` |
| POST | `/v1/projects/{project}/schedules` |
| DELETE | `/v1/projects/{project}/schedules/{id}` |
| PATCH | `/v1/projects/{project}/schedules/{id}` |
| GET | `/v1/projects/{project}/task-runs` |
| GET | `/v1/projects/{project}/workflows` |
| POST | `/v1/projects/{project}/workflows` |
| DELETE | `/v1/projects/{project}/workflows/{id}` |
| PATCH | `/v1/projects/{project}/workflows/{id}` |
| POST | `/v1/projects/{project}/workflows/{id}/backfill` |
| POST | `/v1/projects/{project}/workflows/{id}/dry-run` |
| POST | `/v1/projects/{project}/workflows/{id}/trigger` |
| GET | `/v1/projects/{project}/workflows/{id}/versions` |
| DELETE | `/v1/projects/{project}/workflows/{id}/versions/{version}` |
| POST | `/v1/projects/{project}/workflows/{id}/versions/{version}/restore` |
| GET | `/v1/stream` |
| GET | `/v1/templates` |
| POST | `/v1/templates` |
| DELETE | `/v1/templates/{id}` |
| GET | `/v1/templates/{id}` |
| PATCH | `/v1/templates/{id}` |
| POST | `/v1/templates/{id}/copy` |
| POST | `/v1/templates/{id}/render` |
| DELETE | `/v1/templates/{id}/versions/{version}` |
| POST | `/v1/templates/{id}/versions/{version}/restore` |
| GET | `/v1/tenants` |
| POST | `/v1/tenants` |
| PATCH | `/v1/tenants/{id}` |
| GET | `/v1/users` |
| POST | `/v1/users` |
| DELETE | `/v1/users/{id}` |
| PATCH | `/v1/users/{id}` |
| POST | `/v1/workflows/cron-preview` |
| GET | `/v1/workflows/scheduler` |

## Backend environment

Set `PUBLIC_URL` (Helm `backend.publicURL`) to the canonical origin behind TLS proxies; Helm can derive it from OIDC callback or Ingress host/TLS. Untrusted Forwarded headers are ignored. `EXTERNAL_API_PREFIX` defaults to `/api`. `OIDC_REDIRECT_URL` must be explicitly configured when OIDC is enabled.

| Variable | Purpose |
| --- | --- |
| `ALERT_EVALUATION_INTERVAL` | Evaluation interval or operator-managed webhook |
| `ALERT_WEBHOOK_TIMEOUT` | Evaluation interval or operator-managed webhook |
| `ALERT_WEBHOOK_URL` | Evaluation interval or operator-managed webhook |
| `AUTH_BCRYPT_COST` | Local login/session; initial admin only in local mode |
| `AUTH_INITIAL_ADMIN_DISPLAY_NAME` | Local login/session; initial admin only in local mode |
| `AUTH_INITIAL_ADMIN_PASSWORD` | Local login/session; initial admin only in local mode |
| `AUTH_INITIAL_ADMIN_USERNAME` | Local login/session; initial admin only in local mode |
| `AUTH_MODE` | Local login/session; initial admin only in local mode |
| `AUTH_SESSION_TTL` | Local login/session; initial admin only in local mode |
| `DATABASE_HOST` | PostgreSQL connection/pool; credentials from Secret |
| `DATABASE_MAX_OPEN_CONNECTIONS` | PostgreSQL connection/pool; credentials from Secret |
| `DATABASE_NAME` | PostgreSQL connection/pool; credentials from Secret |
| `DATABASE_PASSWORD` | PostgreSQL connection/pool; credentials from Secret |
| `DATABASE_PORT` | PostgreSQL connection/pool; credentials from Secret |
| `DATABASE_SSLMODE` | PostgreSQL connection/pool; credentials from Secret |
| `DATABASE_URL` | PostgreSQL connection/pool; credentials from Secret |
| `DATABASE_USERNAME` | PostgreSQL connection/pool; credentials from Secret |
| `EXTERNAL_API_PREFIX` | Runtime setting; see configuration source and Helm values |
| `HISTORY_DEFAULT_DAYS` | Runtime setting; see configuration source and Helm values |
| `HTTP_ADDR` | Runtime setting; see configuration source and Helm values |
| `KUBERNETES_API_URL` | Kubernetes connection/namespace scope; ServiceAccount in cluster |
| `KUBERNETES_BEARER_TOKEN` | Kubernetes connection/namespace scope; ServiceAccount in cluster |
| `KUBERNETES_CA_FILE` | Kubernetes connection/namespace scope; ServiceAccount in cluster |
| `KUBERNETES_CLUSTER_NAME` | Kubernetes connection/namespace scope; ServiceAccount in cluster |
| `KUBERNETES_REQUEST_TIMEOUT` | Kubernetes connection/namespace scope; ServiceAccount in cluster |
| `KUBERNETES_SERVICE_HOST` | Kubernetes connection/namespace scope; ServiceAccount in cluster |
| `KUBERNETES_SERVICE_PORT_HTTPS` | Kubernetes connection/namespace scope; ServiceAccount in cluster |
| `LOKI_MAX_ENTRIES` | Observability endpoint/query settings |
| `LOKI_QUERY_TIMEOUT` | Observability endpoint/query settings |
| `LOKI_URL` | Observability endpoint/query settings |
| `OBJECT_STORAGE_ENABLED` | Optional authenticated storage gateway |
| `OBJECT_STORAGE_SIGNING_KEY` | Optional authenticated storage gateway |
| `OBJECT_STORAGE_URL` | Optional authenticated storage gateway |
| `OIDC_ADMIN_GROUPS` | OIDC; explicit callback required, secrets server-side |
| `OIDC_AUTO_CREATE` | OIDC; explicit callback required, secrets server-side |
| `OIDC_CA_FILE` | OIDC; explicit callback required, secrets server-side |
| `OIDC_CLIENT_ID` | OIDC; explicit callback required, secrets server-side |
| `OIDC_CLIENT_SECRET` | OIDC; explicit callback required, secrets server-side |
| `OIDC_ENABLED` | OIDC; explicit callback required, secrets server-side |
| `OIDC_GROUPS_CLAIM` | OIDC; explicit callback required, secrets server-side |
| `OIDC_ISSUER_URL` | OIDC; explicit callback required, secrets server-side |
| `OIDC_REDIRECT_URL` | OIDC; explicit callback required, secrets server-side |
| `OIDC_SCOPES` | OIDC; explicit callback required, secrets server-side |
| `OIDC_STATE_TTL` | OIDC; explicit callback required, secrets server-side |
| `OIDC_USERNAME_CLAIM` | OIDC; explicit callback required, secrets server-side |
| `PROMETHEUS_CPU_RATE_WINDOW` | Observability endpoint/query settings |
| `PROMETHEUS_LOOKBACK` | Observability endpoint/query settings |
| `PROMETHEUS_QUERY_STEP` | Observability endpoint/query settings |
| `PROMETHEUS_QUERY_TIMEOUT` | Observability endpoint/query settings |
| `PROMETHEUS_URL` | Observability endpoint/query settings |
| `PUBLIC_URL` | Runtime setting; see configuration source and Helm values |
| `SPARKAPPLICATION_API_VERSION` | Kubernetes connection/namespace scope; ServiceAccount in cluster |
| `WATCH_NAMESPACES` | Kubernetes connection/namespace scope; ServiceAccount in cluster |
| `WORKFLOWS_ENABLED` | Workflow scheduler, timezone and concurrency |
| `WORKFLOW_MAX_ACTIVE_RUNS` | Workflow scheduler, timezone and concurrency |
| `WORKFLOW_MAX_ACTIVE_TASKS` | Workflow scheduler, timezone and concurrency |
| `WORKFLOW_MAX_CATCHUP_RUNS` | Workflow scheduler, timezone and concurrency |
| `WORKFLOW_MAX_NODES` | Workflow scheduler, timezone and concurrency |
| `WORKFLOW_SCHEDULER_ENABLED` | Workflow scheduler, timezone and concurrency |
| `WORKFLOW_SCHEDULER_INTERVAL` | Workflow scheduler, timezone and concurrency |
| `WORKFLOW_TIMEZONE` | Workflow scheduler, timezone and concurrency |

## Object-storage process environment

Connection types, endpoints, credential-file mounts, buckets, bootstrap grants and limits live in the JSON file selected by `STORAGE_CONFIG_FILE`, rendered from `objectStorage` Helm values. GCS uses the configured KSA WIF identity, not browser OIDC tokens. `GOMEMLIMIT` is a Go runtime tuning hint, not a container memory limit.

| Variable | Purpose |
| --- | --- |
| `DATABASE_HOST` | PostgreSQL connection/pool; credentials from Secret |
| `DATABASE_NAME` | PostgreSQL connection/pool; credentials from Secret |
| `DATABASE_PASSWORD` | PostgreSQL connection/pool; credentials from Secret |
| `DATABASE_PORT` | PostgreSQL connection/pool; credentials from Secret |
| `DATABASE_SSLMODE` | PostgreSQL connection/pool; credentials from Secret |
| `DATABASE_URL` | PostgreSQL connection/pool; credentials from Secret |
| `DATABASE_USERNAME` | PostgreSQL connection/pool; credentials from Secret |
| `GOMEMLIMIT` | Runtime setting; see configuration source and Helm values |
| `HTTP_ADDR` | Runtime setting; see configuration source and Helm values |
| `STORAGE_CONFIG_FILE` | Runtime setting; see configuration source and Helm values |
| `STORAGE_SIGNING_KEY` | Runtime setting; see configuration source and Helm values |

## Persistent tables

Startup migrations require schema/table/index privileges. Back up PostgreSQL before upgrading; the optional plugin migrates its own tables in the same identity database. Object bytes and temporary parts are not stored in PostgreSQL.

- `alert_evaluation_lease`
- `alert_rule_versions`
- `alert_rules`
- `alert_webhook_outbox`
- `application_alerts`
- `application_collections`
- `application_favorites`
- `application_settings`
- `application_template_versions`
- `application_templates`
- `authentication_rate_windows`
- `live_applications`
- `local_user_sessions`
- `local_users`
- `object_storage_bootstrap`
- `object_storage_budget_leases`
- `object_storage_grants`
- `object_storage_limit_nodes`
- `object_storage_operations`
- `object_storage_rate_windows`
- `oidc_login_states`
- `operation_audit`
- `projects`
- `spark_application_history`
- `spark_ui_tickets`
- `tenants`
- `workflow_backfills`
- `workflow_effects`
- `workflow_resources`
- `workflow_runs`
- `workflow_scheduler_state`
- `workflow_schedules`
- `workflow_task_runs`
- `workflow_versions`
- `workflows`
