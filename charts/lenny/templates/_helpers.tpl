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
lenny.componentImage composes a Lenny component image reference from the
single-source platform.registry.* values (§17.8.6: "the gateway,
lenny-ops, controllers, lenny-backup, and the warm-pool controller all
honor the same registry configuration"). It mirrors the
pkg/common/registry ImageResolver precedence so a chart render and a
binary resolve the same way. Invoke with a dict carrying the root
context, the component short name, and the per-component image block:

  {{ include "lenny.componentImage" (dict "root" $ "name" "lenny-gateway" "image" .Values.gateway.image) }}

Precedence:
  1. platform.registry.overrides[<name>] — a complete reference wins.
  2. platform.registry.url + "/" + <name> + ":" + tag — the single source.
  3. The component image.repository + ":" + tag when registry.url is empty,
     so a registry-less custom values file still renders.
The tag defaults to .Chart.AppVersion when the component pins none.
*/}}
{{- define "lenny.componentImage" -}}
{{- $root := .root -}}
{{- $name := .name -}}
{{- $image := .image | default dict -}}
{{- $reg := $root.Values.platform.registry -}}
{{- $overrides := $reg.overrides | default dict -}}
{{- $tag := $image.tag | default $root.Chart.AppVersion -}}
{{- if hasKey $overrides $name -}}
{{- index $overrides $name -}}
{{- else -}}
{{- $url := $reg.url | default "" | trimSuffix "/" -}}
{{- if $url -}}
{{- printf "%s/%s:%s" $url $name $tag -}}
{{- else -}}
{{- printf "%s:%s" $image.repository $tag -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
lenny.imagePullSecrets renders the imagePullSecrets list for a Lenny
component pod spec from platform.registry.pullSecretName (§17.8.6). It
emits nothing when no pull secret is configured, so a public-registry
install renders an unchanged pod spec. Invoke with the root context at
the pod-spec indent, for example:

  {{- include "lenny.imagePullSecrets" $ | nindent 6 }}
*/}}
{{- define "lenny.imagePullSecrets" -}}
{{- with .Values.platform.registry.pullSecretName }}
imagePullSecrets:
  - name: {{ . }}
{{- end }}
{{- end -}}

{{/*
lenny.controllerAntiAffinity renders the §17.8.2 controller-tuning
"Controller pod anti-affinity" block so the WarmPoolController and
PoolScalingController leaders schedule onto different nodes. The shared
controller.antiAffinity value selects the mode:
  - "preferred" (Tier 1, advisory): preferredDuringScheduling, weight 100.
  - "required"  (Tier 2/3): requiredDuringScheduling — the two leaders
    must not co-locate, bounding the simultaneous-failover blast radius.
Invoke with a dict carrying the root context and the peer component the
pod schedules away from:

  {{- include "lenny.controllerAntiAffinity" (dict "root" . "peer" "pool-scaling-controller") | nindent 6 }}
*/}}
{{- define "lenny.controllerAntiAffinity" -}}
{{- $root := .root -}}
{{- $peer := .peer -}}
{{- $mode := $root.Values.controller.antiAffinity | default "preferred" -}}
affinity:
  podAntiAffinity:
{{- if eq $mode "required" }}
    requiredDuringSchedulingIgnoredDuringExecution:
      - labelSelector:
          matchLabels:
            lenny.dev/component: {{ $peer }}
        topologyKey: kubernetes.io/hostname
{{- else }}
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          labelSelector:
            matchLabels:
              lenny.dev/component: {{ $peer }}
          topologyKey: kubernetes.io/hostname
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

{{/*
lenny.clusterType validates and returns the §17.9.1 cluster-type
composition dimension. An empty value is the docker-compose answer
file's cluster=n/a case and passes through unchanged; a non-empty value
must be one of the §17.9.1 cluster types so a typo in a curated answer
file (e.g. cluster: eks-prod) fails the render rather than rendering an
install that silently ignores the dimension.
spec: §17.9.1 line 1351. F-17.9.1.
*/}}
{{- define "lenny.clusterType" -}}
{{- $c := .Values.cluster | default "" -}}
{{- if and (ne $c "") (not (has $c (list "laptop" "eks" "gke" "aks" "openshift" "vanilla"))) -}}
{{- fail (printf "§17.9.1: cluster must be one of laptop, eks, gke, aks, openshift, vanilla (or empty), got %q" $c) -}}
{{- end -}}
{{- $c -}}
{{- end -}}

{{/*
lenny.seededIsolationProfile maps the §17.9.1 isolationProfile
composition dimension (baseline | sandboxed | hypervisor) to the §5.3
runtime isolation profile the seeded reference runtimes carry: baseline
-> standard (runc), sandboxed -> sandboxed (gVisor), hypervisor ->
microvm (Kata). An empty dimension defaults to sandboxed, preserving the
historical seeded-runtime posture. An unrecognized value fails the
render so a typo cannot silently drop every seeded runtime onto the
wrong RuntimeClass.
spec: §17.9.1 line 1354; §5.3. F-17.9.10.
*/}}
{{- define "lenny.seededIsolationProfile" -}}
{{- $p := .Values.isolationProfile | default "sandboxed" -}}
{{- if eq $p "baseline" -}}standard
{{- else if eq $p "sandboxed" -}}sandboxed
{{- else if eq $p "hypervisor" -}}microvm
{{- else -}}
{{- fail (printf "§17.9.1: isolationProfile must be one of baseline, sandboxed, hypervisor, got %q" $p) -}}
{{- end -}}
{{- end -}}

{{/*
lenny.artifactReplicationConfigJSON renders the §25.11 minio.artifactBackup
Helm values into the JSON the gateway's --artifact-replication-config flag
(LENNY_ARTIFACT_REPLICATION_CONFIG) decodes into replication.Config. It
emits the empty string when replication is disabled so the gateway omits
the env and runs no replication controller.

Two topologies (§25.11 "Required Helm values"):
  - per-region: each minio.regions.<region>.artifactBackup becomes a
    region entry with dataResidencyRegion = <region> (cross-region
    replication is prohibited, so the destination jurisdiction tag MUST
    equal the region key).
  - single-region: minio.artifactBackup.enabled true becomes one "default"
    region with no residency constraint and sourceBucket = minio.bucket.
Per-region entries take precedence; when any exist the single-region block
is ignored. The gateway runs the §25.11 startup CONFIG_INVALID validation
on the decoded config, so an incomplete residency-region target is
rejected fail-closed at process start.
spec: §25.11 lines 4045-4071. F-25.11.9.
*/}}
{{- define "lenny.artifactReplicationConfigJSON" -}}
{{- $minio := .Values.minio | default dict -}}
{{- $ab := $minio.artifactBackup | default dict -}}
{{- $regionsIn := $minio.regions | default dict -}}
{{- $regionList := list -}}
{{- range $rk, $rv := $regionsIn -}}
{{- $rab := (default dict $rv).artifactBackup -}}
{{- if $rab -}}
{{- $rt := (default dict $rab).target | default dict -}}
{{- $target := dict "endpoint" ($rt.endpoint | default "") "bucket" ($rt.bucket | default "") "accessCredentialSecret" ($rt.accessCredentialSecret | default "") "kmsKeyId" ($rt.kmsKeyId | default "") -}}
{{- $entry := dict "region" $rk "sourceBucket" ((default dict $rv).sourceBucket | default $minio.bucket | default "") "dataResidencyRegion" $rk "target" $target "allowedDestinationCidrs" ((default dict $rab).allowedDestinationCidrs | default (list)) -}}
{{- $regionList = append $regionList $entry -}}
{{- end -}}
{{- end -}}
{{- $enabled := false -}}
{{- if gt (len $regionList) 0 -}}
{{- $enabled = true -}}
{{- else if $ab.enabled -}}
{{- $enabled = true -}}
{{- $t := $ab.target | default dict -}}
{{- $target := dict "endpoint" ($t.endpoint | default "") "bucket" ($t.bucket | default "") "accessCredentialSecret" ($t.accessCredentialSecret | default "") "kmsKeyId" ($t.kmsKeyId | default "") -}}
{{- $entry := dict "region" "default" "sourceBucket" ($minio.bucket | default "") "dataResidencyRegion" "" "target" $target "allowedDestinationCidrs" (list) -}}
{{- $regionList = append $regionList $entry -}}
{{- end -}}
{{- if $enabled -}}
{{- if eq ($minio.endpoint | default "") "" -}}
{{- fail "§25.11: minio.artifactBackup requires minio.endpoint (the source ArtifactStore cluster the gateway replicates from)." -}}
{{- end -}}
{{- $cfg := dict "enabled" true "versioning" (ternary $ab.versioning true (hasKey $ab "versioning")) "replicationLagRpoSeconds" (int ($ab.replicationLagRpoSeconds | default 900)) "residencyCheckIntervalSeconds" (int ($ab.residencyCheckIntervalSeconds | default 300)) "residencyAuditSamplingWindowSeconds" (int ($ab.residencyAuditSamplingWindowSeconds | default 3600)) "regions" $regionList -}}
{{- $cfg | toJson -}}
{{- end -}}
{{- end -}}
