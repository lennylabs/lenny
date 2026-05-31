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

{{/*
lenny.monitoring.validateFormat fails the render when monitoring.format
is not one of the §16.9 / §25.13 allow-list values (prometheusrule,
configmap, both). Without this guard a typo (e.g. "prometheus-rule")
silently renders no alerting manifest and no scrape configuration, with
no warning. Invoked from prometheusrule.yaml, which Helm always
processes, so the check runs on every render. F-16.9.8.
*/}}
{{- define "lenny.monitoring.validateFormat" -}}
{{- $format := .Values.monitoring.format -}}
{{- $allowed := list "prometheusrule" "configmap" "both" -}}
{{- if not (has $format $allowed) -}}
{{- fail (printf "monitoring.format must be one of [prometheusrule configmap both]; got %q" $format) -}}
{{- end -}}
{{- end -}}

{{/*
lenny.connectionPooler resolves the effective Postgres connection-pooler
posture. An explicit postgres.connectionPooler wins; otherwise it
defaults to "external" when backends is "cloud-managed" (so a
cloud-managed install never silently runs without the lenny_tenant_guard
trigger) and "pgbouncer" otherwise. The result is validated against the
§17.9.3 allow-list, so a typo aborts the render rather than silently
disabling the cloud-pooler defense.
spec: §12.3 line 55 / §17.9.3.
*/}}
{{- define "lenny.connectionPooler" -}}
{{- $explicit := (.Values.postgres | default dict).connectionPooler | default "" -}}
{{- $effective := $explicit | default (ternary "external" "pgbouncer" (eq (.Values.backends | default "") "cloud-managed")) -}}
{{- if not (or (eq $effective "pgbouncer") (eq $effective "external")) -}}
{{- fail (printf "§17.9.3: postgres.connectionPooler must be \"pgbouncer\" or \"external\", got %q" $effective) -}}
{{- end -}}
{{- $effective -}}
{{- end -}}

{{/*
lenny.poolerMode maps the effective connection-pooler posture to the
gateway's LENNY_POOLER_MODE env. "external" (a managed out-of-process
pooler that cannot run the connect_query __unset__ sentinel) stays
"external"; the in-cluster pooler is "transactional". The gateway reads
this to decide whether to enforce the §12.3 line 56 lenny_tenant_guard
startup refusal, so deriving it from the same connectionPooler value the
preflight check uses keeps the install-time and runtime defenses
consistent.
spec: §12.3 line 56 / §4.2 line 165.
*/}}
{{- define "lenny.poolerMode" -}}
{{- if eq (include "lenny.connectionPooler" .) "external" -}}
external
{{- else -}}
transactional
{{- end -}}
{{- end -}}
