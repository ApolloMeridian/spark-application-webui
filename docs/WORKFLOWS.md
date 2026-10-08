# Projects, workflows and scheduled Spark jobs

PostgreSQL-backed orchestration was introduced in 1.4.0; 1.4.1 improves the editor, audit filters and second-level scheduling. It does not depend on an external DolphinScheduler installation and runs only SparkApplication nodes, not arbitrary scripts.

## Tenant and permission boundaries

All existing and newly auto-created OIDC users start in `default`. Administrators can create/rename tenants in **User management → Manage tenants** and assign local or OIDC users to a tenant. Tenant assignment survives subsequent OIDC logins. Administrators are also restricted to their assigned tenant for projects, workflows, schedules and execution history. To operate another tenant, an administrator must explicitly change their own assignment or use an administrator assigned there; this change is audited.

Viewers can browse authorized projects, workflow versions, task/run details, logs and Spark UI, but cannot mutate configuration or execute jobs. Namespace authorization is checked for every node. A mixed-namespace workflow is hidden as a whole when any node is unauthorized. Templates remain a namespace-authorized shared library; existing external/kubectl SparkApplications retain namespace-based visibility. Workflow-created application detail, list, stream and batch-operation access also check tenant ownership.

## Organizing and running jobs

1. Create a project, then create a workflow inside it.
2. Add SparkApplication YAML nodes or select a specific template version. Set node values using YAML `key: value`; parameter definitions use the existing typed form.
3. Drag a border port onto another task to connect, or select upstream tasks in the form. Multiple incoming/outgoing dependencies are supported and reflected in both views. Links automatically choose facing ports based on node positions. Select a link to remove it; keyboard users can press Enter on a port, then select a downstream task. Drag nodes to arrange the DAG. Cycles, unknown dependencies and duplicate IDs are rejected by both UI and backend.
4. Save. Each save creates an immutable version containing the entire DAG, node YAML, selected template version, parameter definitions and values.
5. Use **Run → Dry run & preview** to inspect generated manifests. Each actual dispatch also performs Kubernetes server-side dry-run before CREATE. Manual execution defaults to the current time as its parameter baseline.

Dependencies require **all upstream tasks to succeed**. Independent branches run concurrently within workflow and global limits. An upstream terminal failure skips its dependants. Node retries are bounded (0–10) with exponential backoff from 10 seconds, capped at five minutes. Configure Spark `restartPolicy: Never` when the workflow should own retries; otherwise the Spark operator's own retries occur inside an orchestration attempt.

Workflow and node policies control **overlapping execution instances**, not the DAG dependency order:

- `parallel`: admit overlapping instances; nodes still honor dependencies and concurrency limits.
- `serial_wait`: queue behind the preceding active workflow, or wait while the same node in another instance is active.
- `serial_discard`: discard the new workflow, or skip the busy node; skipped upstream nodes prevent dependent tasks from running.

Pause stops further dispatch but continues observing already-running Spark jobs. Resume continues pending tasks. Cancel requests the existing Driver deadline-based termination, retains the SparkApplication/Driver for diagnosis, and waits for Kubernetes to report a terminal state. Cluster/API failures or a Driver that cannot be terminated keep the instance `cancelling` with an error message rather than falsely reporting success. **Rerun failed** requeues failed/skipped nodes with a new attempt name and the original frozen configuration/logical time. Successful nodes are not rerun.

The project execution-history page has separate workflow and task tables, filters, and links to application details. Instance names contain a 17-digit creation timestamp (`yyyyMMddHHmmssSSS`) plus an identifier. Task names and manifests are persisted before dispatch.

## Collision-safe SparkApplication names

**Unique application name suffix** defaults on. The suffix contains the UTC logical timestamp, a 64-bit portion of the randomly generated task ID, and the attempt number. The base name is truncated to fit the 63-character DNS-label limit. Retries create a new SparkApplication, preserving the previous failed object for diagnostics.

If disabled, the supplied name is submitted with Kubernetes CREATE, never apply/update. An existing foreign resource causes an explicit conflict, not an overwrite. A resource bearing the same persisted task-instance annotation is recovered after a crash, not submitted again. Disabling suffixes also makes retries conflict with the retained failed object; enable suffixes for retryable jobs.

## Time parameters and Cron

Supported date tokens are `yyyy`, `MM`, `dd`, `HH`, `mm`, `ss`, `SSS`. The date-expression baseline is the persisted scheduled time in the workflow's IANA timezone:

```yaml
businessDate: $[yyyy-MM-dd-1]
previousHour: $[yyyy-MM-dd HH:mm:ss-1/24]
nextDay: $[yyyyMMdd+1]
runTimestamp: $[yyyyMMddHHmmssSSS]
```

Whole offsets use calendar days, preserving local wall time across DST. Fractional offsets use elapsed durations: `-1/24` is one hour, `-1/1440` one minute. Backfill uses each selected occurrence, not the time at which the worker eventually dispatches it. Parameters also support typed `{{name}}` placeholders. Use Kubernetes Secret references; do not place credentials in YAML or parameters.

Schedules support six-field Cron (**second minute hour day-of-month month day-of-week**); existing five-field expressions remain valid and run at second 0. Dropdowns configure second/minute intervals, hourly, daily, weekly, monthly and yearly schedules, with a custom expression option. Cron steps are field-based (`*/7` seconds resets at each minute), not elapsed-duration timers. Monthly dates absent from a month are skipped.

The next-execution preview returns at most ten future occurrences strictly inside the optional validity window (inclusive boundaries), using the configured IANA timezone. If only two executions fit, only two are shown; if none fit, a time range / Cron error is displayed. Changing the Cron, timezone or validity window clears stale preview results. When day-of-month and day-of-week are both restricted, either matching field triggers, following standard Cron OR semantics. Nonexistent DST wall times are skipped; repeated wall times can yield distinct UTC occurrences. Impossible expressions and invalid timezones are rejected. Editing a schedule recalculates its next occurrence from now; already-created runs retain their snapshots.

Second-level occurrences do not guarantee real-time dispatch. The backend polls at `workflows.scheduler.scanInterval` (default `5s`, minimum `1s`); use `1s` for finer scanning and review overlap, misfire and concurrency limits before enabling frequent schedules. The `fire_once` policy locates the latest missed occurrence without iterating through every missed second after prolonged downtime.

Misfire policies: `skip` skips occurrences older than two scan intervals plus one second; `fire_once` creates only the latest missed occurrence; `catch_up` creates up to the configured limit, then skips the remaining backlog and records an audit. Disabled workflows do not accumulate missed runs. The workflow overlap strategy applies to both automatic and manual runs. Backfill preview/confirmation supports up to 200 occurrences across at most 366 days; a repeated explicit confirmation intentionally creates new instances.

## Persistence, HA and version deletion

Configuration and state live in PostgreSQL: tenants, user assignments, projects, workflow/version snapshots, schedules/next occurrences, workflow/task instances, ownership bindings and audits. Existing alert rules and template versions already use the same database. No backend Pod volume or process-local queue is required. PostgreSQL itself must use durable storage and backups.

Workers coordinate through a transaction-scoped PostgreSQL advisory lock. Only one replica makes dispatch decisions at a time; other replicas remain HTTP-ready. A lost transaction cannot commit stale progress. Kubernetes CREATE and PostgreSQL are not atomic: deterministic attempt names and the task-instance annotation reconcile accepted external creates after a Pod crash. Schedule ID + UTC occurrence has a unique constraint. Startup migrations also acquire the dispatch lock to avoid rolling-upgrade DDL/worker deadlocks.

Template/workflow history offers **View**, **Restore**, and **Delete**. Restore creates a new monotonically increasing current version. The current version cannot be deleted. Deleting a historical version physically removes its PostgreSQL row and YAML/parameter/snapshot content. Execution snapshots remain independent so existing runs and their evidence do not change. A workflow/project can be deleted only after its execution history is explicitly removed. History deletion cascades task history but retains Kubernetes jobs and ownership isolation. Schedule ownership is revalidated against the current user role, account status, namespace permissions and tenant before each trigger/dispatch; revoked owners block scheduling and stop active runs.

Human configuration mutations, restores/deletions, run actions and backfills are audited. Automatic triggers, dispatches, blocked schedules and workflow completion are also audited. Read-only polling is not audited. Passwords and request bodies are never included in configuration-action audit messages.

Operation Audit offers combined operator, operation, result and keyword filters. Operation tags use semantic colors (deletion red, termination orange, submission blue, configuration cyan, restore purple) and wrap long names without overlapping the result column. Workflow pages display the current tenant name and safely clear project data/selection when the last project is removed.

## Helm and operations

```yaml
workflows:
  enabled: true
  timeZone: Asia/Shanghai
  maxNodes: 50
  scheduler:
    enabled: true
    scanInterval: 5s
    maxActiveRuns: 20
    maxActiveTasks: 50
    maxCatchupRuns: 20
```

The frontend feature switch derives from backend availability and `workflows.enabled`. Workflow operations are not simulated in the standalone frontend mock demo. Multiple backend replicas must share the same PostgreSQL database and Kubernetes context. `/v1/workflows/scheduler` exposes the persisted scheduler heartbeat; `/metrics` adds workflow queue, active runs/tasks, failed tasks and schedule-lag gauges. History retention is manual in this release; there is no automatic data purging.

Before upgrading, back up PostgreSQL. Migrations are additive and all older users are assigned `default`. To stop reconciliation without losing state, set `workflows.scheduler.enabled=false`; jobs already submitted to Kubernetes continue independently. Re-enable to recover persisted runs. Disabling the scheduler also pauses application of queued cancel actions; leave it enabled until requested cancellations finish. Do not run an older backend concurrently with the new scheduler during rollback.

```bash
docker build -t spark-control-center:1.4.1 .
docker build -f backend/Dockerfile -t spark-control-center-backend:1.4.1 .
helm upgrade --install spark-console ./charts/spark-control-center \
  --namespace spark-console --create-namespace \
  -f charts/spark-control-center/examples/values-production.yaml \
  -f values.local.yaml
```

Validation includes PostgreSQL integration tests (DAG/HA/crash recovery/permissions/version deletion), production-container smoke tests using a Kubernetes HTTP fixture, and English browser screenshots at 1440/1024px. These tests do **not** prove compatibility with your specific Spark operator/admission policies; run a small non-production workflow against your cluster before enabling production schedules.
