{{- define "spark-control-center.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "spark-control-center.backendServiceAccountName" -}}
{{- if .Values.backend.serviceAccount.create }}
{{- default (printf "%s-backend" (include "spark-control-center.fullname" .)) .Values.backend.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.backend.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "spark-control-center.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name (include "spark-control-center.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "spark-control-center.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "spark-control-center.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "spark-control-center.selectorLabels" -}}
app.kubernetes.io/name: {{ include "spark-control-center.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: frontend
{{- end }}

{{- define "spark-control-center.backendSelectorLabels" -}}
app.kubernetes.io/name: {{ include "spark-control-center.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: backend
{{- end }}

{{- define "spark-control-center.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "spark-control-center.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "spark-control-center.backendSecretName" -}}
{{- if .Values.database.credentials.existingSecret }}{{ .Values.database.credentials.existingSecret }}{{ else }}{{ include "spark-control-center.fullname" . }}-backend{{ end }}
{{- end }}

{{- define "spark-control-center.oidcSecretName" -}}
{{- if .Values.oidcSecret.existingSecret }}{{ .Values.oidcSecret.existingSecret }}{{ else }}{{ include "spark-control-center.fullname" . }}-backend{{ end }}
{{- end }}

{{- define "spark-control-center.localAuthSecretName" -}}
{{- if .Values.backend.auth.initialAdmin.existingSecret }}{{ .Values.backend.auth.initialAdmin.existingSecret }}{{ else }}{{ include "spark-control-center.fullname" . }}-local-auth{{ end }}
{{- end }}

{{/* Annotations are string maps. Do not silently turn YAML off/false into "false". */}}
{{- define "spark-control-center.annotations" -}}
{{- range $key, $value := . }}
{{- if not (kindIs "string" $value) }}{{ fail (printf "metadata annotation %q must be a string; quote YAML values such as off, on, true, false and numbers" $key) }}{{ end }}
{{ $key | quote }}: {{ $value | quote }}
{{- end }}
{{- end }}
{{/* Validate the reserved destination before rendering any SQL deployment. */}}
{{- define "spark-control-center.sqlResultLocation" -}}
{{- $results := .Values.sql.results -}}
{{- if or .Values.sql.enabled $results.connection $results.bucket $results.prefix -}}
  {{- if not .Values.objectStorage.enabled -}}{{ fail "sql requires objectStorage.enabled=true" }}{{- end -}}
  {{- if not (and $results.connection $results.bucket (regexMatch "^([A-Za-z0-9_][A-Za-z0-9._-]*/)+$" $results.prefix) (le (len $results.prefix) 900)) -}}
    {{- fail "sql.results requires a connection, bucket and non-empty slash-terminated prefix" -}}
  {{- end -}}
  {{- $valid := false -}}
  {{- range .Values.objectStorage.connections -}}
    {{- if and (eq .id $results.connection) (has $results.bucket .buckets) -}}
      {{- if and (eq .provider "s3") (not .s3.conditionalWrites) -}}
        {{- fail "SQL results require conditional writes on the selected S3 connection" -}}
      {{- end -}}
      {{- $valid = true -}}
    {{- end -}}
  {{- end -}}
  {{- if not $valid -}}{{ fail "sql.results.bucket must be listed in the selected objectStorage connection" }}{{- end -}}
  {{- dict "connection" $results.connection "bucket" $results.bucket "prefix" $results.prefix | toJson -}}
{{- else -}}
  {}
{{- end -}}
{{- end -}}
