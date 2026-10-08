# Reusable YAML templates and diagnostic alerts

## Templates

Clone, retry, and template-save operations retain `apiVersion`, `kind`, `spec`, and the necessary metadata (`name`, `namespace`, custom labels and annotations). They discard runtime `status`, Kubernetes managed fields, ownership/finalizer metadata, the previous console submitter, and `kubectl.kubernetes.io/last-applied-configuration`.

Saving a template from an application opens a cleaned YAML manifest. Existing JSON templates are converted when opened for editing and when rendered or copied. Saving creates a new version; old version snapshots remain unchanged.

Use the parameter form to add a name, type, required flag, default value, allowed values, optional regex, and description. There is no JSON parameter editor. For example:

```yaml
apiVersion: sparkoperator.k8s.io/v1beta2
kind: SparkApplication
metadata:
  name: "{{name}}"
  namespace: spark
spec:
  type: Scala
  mode: cluster
  image: "{{image}}"
  mainClass: org.apache.spark.examples.SparkPi
  mainApplicationFile: local:///opt/spark/examples/jars/spark-examples.jar
  sparkVersion: "3.5.3"
  driver:
    cores: 1
    memory: 1g
    serviceAccount: spark
  executor:
    instances: "{{executorCount}}"
    cores: 1
    memory: 2g
```

Define `name` and `image` as strings and `executorCount` as an integer. Entire-scalar integer/number/boolean placeholders retain their types after rendering; placeholders embedded in longer strings stay strings. Values are escaped as YAML data rather than inserted as YAML syntax. Define Spark configuration map values as strings when Spark requires string values. Rendered submissions still go through the existing Kubernetes dry-run and permission checks. Reference existing Kubernetes Secrets rather than embedding credentials.

## Diagnostic rules

In Alert center → New rule, select **Diagnostic fingerprint**. Codes include `APPLICATION_FAILED`, `VOLUME_MOUNT`, `IMAGE_PULL`, `SCHEDULING`, `OOM`, `EXECUTOR_LOST`, and `RBAC`, along with codes already observed in your cluster. You can type future codes without upgrading the rule editor. A fingerprint card also has a shortcut to create a rule for its code and namespace.

- Select one or more codes to match any of them.
- Leave codes empty to match all current and future diagnoses.
- Namespace scope, severity, enabled flag, and Webhook settings continue to apply.
- Each application/code produces an independent alert so concurrent diagnoses do not overwrite one another.
- The evaluator collects Kubernetes events, including event-only `FailedMount`/image-pull problems, and preserves diagnosis evidence and recommendations.

The original failure, pending, resource-pressure, and retry rules remain supported. A diagnostic rule is stored as `type: diagnostic` with `diagnosticCodes: [VOLUME_MOUNT]` (or an empty list for every code). PostgreSQL adds a non-null `diagnostic_codes` column automatically at startup; immutable rule snapshots include the selected codes. Webhooks retain the existing unique alert fingerprint and include `diagnostic=VOLUME_MOUNT` in the evidence.

## Version history

Restore switches the current version pointer without adding a snapshot. The current version and the version open in the preview cannot be deleted. The preview shows its version number and offers editing as a new version. Saving allocates a number beyond the highest previously allocated number, even when newer historical versions were deleted. Deleting a historical version removes its YAML and parameters from PostgreSQL; existing execution snapshots remain independent.
