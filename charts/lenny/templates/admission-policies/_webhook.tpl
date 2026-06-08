{{- /*
lenny.admissionWebhookWorkload renders the Deployment, Service,
PodDisruptionBudget, and cert-manager Certificate shared by every
admission webhook (§17.2). Each webhook is its own Deployment running
the lenny-webhook image with replicas: 2 and a minAvailable: 1 PDB.
The per-webhook ValidatingWebhookConfiguration carries the resource
rules and is written by the calling template.

Invoke with a dict carrying the root context and the webhook name:

	{{- include "lenny.admissionWebhookWorkload" (dict "root" $ "name" "lenny-label-immutability") }}

Pass an optional egressLabel (the short, kebab-case webhook name) to
additionally stamp the NET-068 additive pod label
lenny.dev/webhook-name: <egressLabel> on this Deployment's pods only, so
a NetworkPolicy egress sub-rule can narrow to this single webhook
(§13.2 NET-068, §17.2 line 56). The label is additive to the canonical
lenny.dev/component: admission-webhook selector and is omitted when
egressLabel is empty — chart authors MUST NOT add it to webhooks that do
not have a documented egress-narrowing sub-rule.
*/ -}}
{{- define "lenny.admissionWebhookWorkload" -}}
{{- $ := .root -}}
{{- $name := .name -}}
{{- $egressLabel := .egressLabel | default "" -}}
{{- $cfg := $.Values.admissionWebhooks -}}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ $name }}
  namespace: {{ $.Release.Namespace }}
  labels:
    {{- include "lenny.labels" $ | nindent 4 }}
    lenny.dev/component: admission-webhook
spec:
  replicas: {{ $cfg.replicas }}
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ $.Chart.Name }}
      lenny.dev/webhook: {{ $name }}
  template:
    metadata:
      labels:
        {{- include "lenny.labels" $ | nindent 8 }}
        lenny.dev/component: admission-webhook
        lenny.dev/webhook: {{ $name }}
        {{- if $egressLabel }}
        lenny.dev/webhook-name: {{ $egressLabel }}
        {{- end }}
    spec:
      serviceAccountName: lenny-webhook
      {{- include "lenny.imagePullSecrets" $ | nindent 6 }}
      securityContext:
        runAsNonRoot: true
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: webhook
          image: {{ include "lenny.componentImage" (dict "root" $ "name" "lenny-webhook" "image" $cfg.image) | quote }}
          imagePullPolicy: {{ $cfg.image.pullPolicy }}
          args:
            - --addr=:8443
            - --tls-cert-file=/etc/lenny/webhook-tls/tls.crt
            - --tls-key-file=/etc/lenny/webhook-tls/tls.key
            - --tenancy-mode={{ $.Values.tenancy.mode }}
            - --dev-mode={{ $.Values.global.devMode }}
            - --gateway-drain-readiness-url=http://lenny-gateway.{{ $.Release.Namespace }}.svc:{{ $.Values.gateway.internalPort }}/internal/drain-readiness
            - --gateway-drain-audit-url=http://lenny-gateway.{{ $.Release.Namespace }}.svc:{{ $.Values.gateway.internalPort }}/internal/audit/node-drain-forced
            - --gateway-runtime-upgrade-active-url=http://lenny-gateway.{{ $.Release.Namespace }}.svc:{{ $.Values.gateway.internalPort }}/internal/runtime-upgrade/active
            - --registry-require-digest={{ $.Values.platform.registry.requireDigest }}
            - --gvisor-runtime-class={{ $.Values.runtimeClasses.profiles.sandboxed.name }}
            - --kata-runtime-class={{ $.Values.runtimeClasses.profiles.microvm.name }}
          ports:
            - name: https
              containerPort: 8443
          livenessProbe:
            httpGet:
              path: /healthz
              port: https
              scheme: HTTPS
            initialDelaySeconds: 10
            periodSeconds: 20
          readinessProbe:
            httpGet:
              path: /readyz
              port: https
              scheme: HTTPS
            initialDelaySeconds: 5
            periodSeconds: 10
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop:
                - ALL
          volumeMounts:
            - name: webhook-tls
              mountPath: /etc/lenny/webhook-tls
              readOnly: true
          resources:
            {{- toYaml $cfg.resources | nindent 12 }}
      volumes:
        - name: webhook-tls
          secret:
            secretName: {{ $name }}-tls
---
apiVersion: v1
kind: Service
metadata:
  name: {{ $name }}
  namespace: {{ $.Release.Namespace }}
  labels:
    {{- include "lenny.labels" $ | nindent 4 }}
spec:
  selector:
    app.kubernetes.io/name: {{ $.Chart.Name }}
    lenny.dev/webhook: {{ $name }}
  ports:
    - name: https
      port: 443
      targetPort: https
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{ $name }}
  namespace: {{ $.Release.Namespace }}
  labels:
    {{- include "lenny.labels" $ | nindent 4 }}
spec:
  minAvailable: 1
  selector:
    matchLabels:
      lenny.dev/webhook: {{ $name }}
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: {{ $name }}
  namespace: {{ $.Release.Namespace }}
  labels:
    {{- include "lenny.labels" $ | nindent 4 }}
spec:
  secretName: {{ $name }}-tls
  dnsNames:
    - {{ $name }}.{{ $.Release.Namespace }}.svc
    - {{ $name }}.{{ $.Release.Namespace }}.svc.cluster.local
  issuerRef:
    name: lenny-webhook-selfsign
    kind: Issuer
{{- end -}}
