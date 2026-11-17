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
echo "=== the kubelet's identity through pods/status with a raw PATCH, under the chart's rules (resources pods only)"
$K delete validatingwebhookconfiguration ob-egress-group --ignore-not-found >/dev/null
sleep 3
podj(){ echo "{\"apiVersion\":\"v1\",\"kind\":\"Pod\",\"metadata\":{\"name\":\"$1\",\"namespace\":\"agents\",\"labels\":{\"app\":\"x\"}},\"spec\":{\"nodeName\":\"ob-pki-control-plane\",\"containers\":[{\"name\":\"c\",\"image\":\"ob-tools:1\",\"imagePullPolicy\":\"Never\",\"command\":[\"ob\",\"sleep\"]}]}}"; }
podj st2 | $A apply -f - >/dev/null; sleep 3
T=$(mktemp -d); $K config view --raw -o jsonpath='{.users[0].user.client-certificate-data}' | base64 -d > $T/c; $K config view --raw -o jsonpath='{.users[0].user.client-key-data}' | base64 -d > $T/k; S=$($K config view --raw -o jsonpath='{.clusters[0].cluster.server}')
curl -sk --cert $T/c --key $T/k -H "Impersonate-User: system:node:ob-pki-control-plane" -H "Impersonate-Group: system:nodes" -H "Content-Type: application/merge-patch+json" -X PATCH "$S/api/v1/namespaces/agents/pods/st2/status" -d '{"metadata":{"labels":{"lenny.dev/egress-group":"group-a","lenny.dev/tenant-id":"evil"}}}' -o $T/o -w 'http=%{http_code}\n'; python3 -c "
import json;d=json.load(open('$T/o'));print(d.get('kind'),d.get('message','')[:200]);print('labels in the response:',d.get('metadata',{}).get('labels'))"
echo "labels now: $($A get pod st2 -o jsonpath='{.metadata.labels}')"
rm -f $T/c $T/k $T/o; rmdir $T
$A delete pod st2 --wait=false >/dev/null
