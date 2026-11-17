#!/bin/bash
R=spike-owed-b; cd $R; . ./kc.sh pki
A="$K -n agents"
$A delete pod --all --wait=true >/dev/null; $A delete sandbox --all >/dev/null
cat <<'EOF' | $K apply -f - >/dev/null
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: ob-wpc-extra}
rules:
- {apiGroups: [""], resources: [nodes], verbs: [get, list, watch]}
- {apiGroups: [lenny.dev], resources: ["*"], verbs: [get, list, watch, patch, update]}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: ob-wpc-extra}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: ob-wpc-extra}
subjects: [{kind: ServiceAccount, name: lenny-controller, namespace: lenny-system}]
EOF
echo "=== drain on mismatch with the controller holding node read"
bin/drainh-host $R/kc-pki 2>&1 | cut -c1-230
$A get sandbox -o 'custom-columns=N:.metadata.name,P:.status.phase'
$A get pod -o 'custom-columns=N:.metadata.name,G:.metadata.labels.lenny\.dev/egress-group,D:.metadata.annotations.lenny\.dev/drain-request'
