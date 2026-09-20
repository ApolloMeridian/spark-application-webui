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
