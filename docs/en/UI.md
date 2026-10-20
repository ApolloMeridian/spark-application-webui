# UI tour

## Application operations

Start with the overview to inspect live resources and historical submission/failure totals, then filter applications by namespace, owner and state.

![Operations overview](../images/overview-1440.png)

![SparkApplication list](../images/applications-1440.png)

Open application details for Driver/Executor resources and selectable historical ranges. Persisted Executor logs support time filtering and ascending/descending order.

Driver logs support latest/range queries, previous/next line windows and Loki time filtering.

![Resource details](../images/detail-1440.png)

![Executor logs](../images/executor-logs-1440.png)

![Driver log ranges](../images/driver-log-range-1440.png)

Administrators submit YAML after server-side validation and inspect the normalized manifest before creating an application.

![YAML submission](../images/submit-1440.png)

## Workflows and scheduling

Projects organize workflows and tasks. Drag task ports to build dependencies and configure typed parameter defaults, including logical-time expressions.

![Project organization](../images/workflow-projects-1440.png)

![Workflow task editor](../images/workflow-editor-1440.png)

Configure second-level Cron using dropdowns or a custom expression. Preview executions within the validity window; use project-scoped histories to inspect workflow and task instances.

![Cron schedule builder](../images/workflow-schedule-1440.png)

![Execution history](../images/workflow-history-1440.png)

Backfill previews show the first 50 occurrences. Choose forward/reverse order and a failure policy, then confirm the entire range for durable serial execution.

![Backfill configuration](../images/workflow-backfill-1440.png)

## Object storage

Create empty directories as drafts; they become persistent only when a file is written beneath them. Search file names and relative paths recursively within the current prefix; Next continues bounded scan batches. See [object-storage setup and permissions](OBJECT-STORAGE.md).

![Draft folder](../images/object-storage-draft-folder-1440.png)

![Prefix search](../images/object-storage-search-1440.png)

## Templates, alerts and governance

Reuse parameterized YAML templates and manage their versions. Diagnostic rules match observed or custom failure codes; alert evidence links back to application diagnostics.

![Template library](../images/templates-1440.png)

![Template parameter editor](../images/template-editor-1440.png)

![Alert center](../images/alerts-1440.png)

![Diagnostic rule](../images/diagnostic-rule-1440.png)

Filter audit records by operator, operation, result and keyword.

![Operation audit](../images/audit-1440.png)

Manage accounts, namespace permissions and tenant assignments from User management.

![User management](../images/users-1440.png)

Return to the [English README](../../README.md).
