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

{{/*
lenny.autoscaling.metric.* templates are the §4.1 SCL-026 canonical
HPA metric-role mapping. The gateway HPA and KEDA ScaledObject
(autoscaling-gateway.yaml) reference every scale metric by its named
role through these templates, so a metric rename is a single-line edit
here rather than a sweep across the chart.

  primaryScaleOut — §4.1 primary HPA scale-out trigger (queue depth).
  secondaryMetric — §4.1 secondary HPA metric (active streams).

lenny_gateway_active_sessions is intentionally absent: §4.1 SCL-026
classifies it as an alert-only capacity-ceiling signal, never an HPA
trigger.
*/}}
{{- define "lenny.autoscaling.metric.primaryScaleOut" -}}
lenny_gateway_request_queue_depth
{{- end -}}
{{- define "lenny.autoscaling.metric.secondaryMetric" -}}
lenny_gateway_active_streams
{{- end -}}

{{/*
lenny.mtlsLeafCertificate renders one §10.3 internal-control-plane
cert-manager Certificate. The certificate has a DNS SAN of
<name>.<namespace>.svc (and the cluster-FQDN form), the §10.3 24h leaf
TTL, and an issuerRef pointing at the lenny-mtls-ca CA Issuer so the
leaf chains to the cluster-internal CA.

Invoke with a dict carrying the root context, the component's Service
name, and a short component label:

    {{- include "lenny.mtlsLeafCertificate" (dict "root" $ "name" "lenny-gateway" "component" "gateway") }}

The resulting Secret is named <name>-tls.
*/}}
{{- define "lenny.mtlsLeafCertificate" -}}
{{- $ := .root -}}
{{- $name := .name -}}
{{- $component := .component -}}
{{- $ns := $.Release.Namespace -}}
{{- $m := $.Values.mtls -}}
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: {{ $name }}-tls
  namespace: {{ $ns }}
  labels:
    {{- include "lenny.labels" $ | nindent 4 }}
    lenny.dev/component: mtls-pki
spec:
  secretName: {{ $name }}-tls
  duration: {{ printf "%dh" (int $m.leafDurationHours) }}
  renewBefore: {{ printf "%dh" (int $m.leafRenewBeforeHours) }}
  commonName: {{ $name }}.{{ $ns }}.svc
  dnsNames:
    - {{ $name }}.{{ $ns }}.svc
    - {{ $name }}.{{ $ns }}.svc.cluster.local
  usages:
    - server auth
    - client auth
  privateKey:
    algorithm: ECDSA
    size: 256
  issuerRef:
    name: lenny-mtls-ca
    kind: Issuer
{{- end -}}
