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
lenny.monitoring.operatorPresent reports "true" when the Prometheus
Operator CRD API group (monitoring.coreos.com/v1) is registered in the
target cluster, else "". Render-time detection via
.Capabilities.APIVersions, which reflects the live cluster during
`helm install`/`helm upgrade` and the built-in set during `helm template`
(where `--api-versions monitoring.coreos.com/v1` declares it explicitly).
This is the §16.9 R8 CRD-presence preflight: a chart render targeting the
operator CRDs first confirms they exist. F-16.9.4.
*/}}
{{- define "lenny.monitoring.operatorPresent" -}}
{{- if .Capabilities.APIVersions.Has "monitoring.coreos.com/v1" -}}true{{- end -}}
{{- end -}}

{{/*
lenny.monitoring.effectiveFormat resolves monitoring.format to the value
actually rendered. When the configured format selects the Prometheus
Operator PrometheusRule CRD (prometheusrule or both) but the operator is
absent, it degrades to "configmap" so `kubectl apply` does not fail on a
missing PrometheusRule CRD. This is the §16.9 R8 automatic fallback. The
format is validated against the allow-list first. F-16.9.4.
*/}}
{{- define "lenny.monitoring.effectiveFormat" -}}
{{- include "lenny.monitoring.validateFormat" . -}}
{{- $format := .Values.monitoring.format -}}
{{- $operator := include "lenny.monitoring.operatorPresent" . -}}
{{- if and (or (eq $format "prometheusrule") (eq $format "both")) (not $operator) -}}
configmap
{{- else -}}
{{- $format -}}
{{- end -}}
{{- end -}}

{{/*
lenny.monitoring.renderMonitors reports "true" when the chart should
render the §16.9 ServiceMonitor and PodMonitor. The Prometheus Operator
CRDs MUST be present, and either monitoring.format selects the operator
CRDs (prometheusrule/both) or monitoring.serviceMonitor.enabled forces
them on. Collapsing the scrape-CRD gate onto monitoring.format (F-16.9.6)
is safe because the operator-presence check (F-16.9.4) fails closed when
the CRDs are absent, so a `format: prometheusrule` install on a cluster
without the operator renders neither a PrometheusRule nor scrape monitors.
F-16.9.4, F-16.9.6.
*/}}
{{- define "lenny.monitoring.renderMonitors" -}}
{{- $operator := include "lenny.monitoring.operatorPresent" . -}}
{{- $format := .Values.monitoring.format -}}
{{- $formatSelects := or (eq $format "prometheusrule") (eq $format "both") -}}
{{- if and $operator (or $formatSelects .Values.monitoring.serviceMonitor.enabled) -}}true{{- end -}}
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

{{/*
lenny.redisProvider resolves and validates the §17.9.3 Redis topology
selector, returning the effective provider ("external", "sentinel", or
"cluster"). An explicit redis.provider is validated against the
allow-list and against the fields the chosen topology requires (sentinel
needs redis.sentinels + redis.sentinelMaster; cluster needs
redis.cluster.addrs). An empty redis.provider resolves from the
configured fields — cluster.addrs selects "cluster", a non-empty
sentinels list selects "sentinel", otherwise "external" — so existing
url-only and cluster-only values files resolve exactly as before. The
external topology imposes no requirement: an empty url is the §17.4
in-memory dev posture. Templates use this helper as the single source of
truth for which connection env block to render.
spec: §17.9.3 lines 1402-1409.
*/}}
{{- define "lenny.redisProvider" -}}
{{- $redis := .Values.redis | default dict -}}
{{- $cluster := $redis.cluster | default dict -}}
{{- $hasClusterAddrs := ne ($cluster.addrs | default "" | toString) "" -}}
{{- $hasSentinels := and (hasKey $redis "sentinels") $redis.sentinels -}}
{{- $provider := $redis.provider | default "" -}}
{{- if eq $provider "" -}}
{{- if $hasClusterAddrs -}}{{- $provider = "cluster" -}}
{{- else if $hasSentinels -}}{{- $provider = "sentinel" -}}
{{- else -}}{{- $provider = "external" -}}{{- end -}}
{{- else if not (or (eq $provider "external") (eq $provider "sentinel") (eq $provider "cluster")) -}}
{{- fail (printf "§17.9.3: redis.provider must be \"external\", \"sentinel\", or \"cluster\", got %q" $provider) -}}
{{- end -}}
{{- if eq $provider "sentinel" -}}
{{- if not $hasSentinels -}}
{{- fail "§17.9.3: redis.provider=sentinel requires a non-empty redis.sentinels list" -}}
{{- end -}}
{{- if eq ($redis.sentinelMaster | default "") "" -}}
{{- fail "§17.9.3: redis.provider=sentinel requires redis.sentinelMaster" -}}
{{- end -}}
{{- end -}}
{{- if and (eq $provider "cluster") (not $hasClusterAddrs) -}}
{{- fail "§17.9.3: redis.provider=cluster requires redis.cluster.addrs" -}}
{{- end -}}
{{- $provider -}}
{{- end -}}

{{/*
lenny.gatewayLogLevel maps the §17.9.1 environment dimension to the
gateway's LENNY_LOG_LEVEL. The local and dev environments render
"debug" verbosity; staging and prod render "info". An empty environment
yields "info", matching the gateway's own LENNY_LOG_LEVEL default, so a
stock render is unchanged.
spec: §17.9.1 line 1350; §16.4 line 372.
*/}}
{{- define "lenny.gatewayLogLevel" -}}
{{- $env := .Values.environment | default "" -}}
{{- if or (eq $env "local") (eq $env "dev") -}}
debug
{{- else -}}
info
{{- end -}}
{{- end -}}
