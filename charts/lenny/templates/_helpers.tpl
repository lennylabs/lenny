{{/*
lenny.labels renders the common label set applied to every resource
the chart emits. Callers invoke it with the root context, for example
`{{- include "lenny.labels" $ | nindent 4 }}`.
*/}}
{{- define "lenny.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
{{- end -}}
