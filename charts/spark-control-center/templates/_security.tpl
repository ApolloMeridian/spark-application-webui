{{- define "spark-control-center.publicURL" -}}
{{- if .Values.backend.publicURL -}}
{{- .Values.backend.publicURL -}}
{{- else if .Values.oidc.redirectURL -}}
{{- $url := urlParse .Values.oidc.redirectURL -}}
{{- printf "%s://%s" $url.scheme $url.host -}}
{{- else if and .Values.ingress.enabled .Values.ingress.hosts -}}
{{- $scheme := "http" -}}
{{- if .Values.ingress.tls -}}{{- $scheme = "https" -}}{{- end -}}
{{- printf "%s://%s" $scheme (first .Values.ingress.hosts).host -}}
{{- end -}}
{{- end -}}
