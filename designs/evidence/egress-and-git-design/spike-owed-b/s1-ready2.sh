#!/bin/bash
# Which part of the config makes invalid material fatal at start: only the downstream listener through SDS, or also the I3 client certificate through SDS?
R=spike-owed-b; cd $R; . ./kc.sh pki
N="$K -n lenny-system"
python3 - <<'EOF'
import json
b=json.load(open("k8s/envoy-nomcd.json"))
# variant A: downstream SDS only; the I3 cluster has a client certificate from static files and no SDS
for c in b["static_resources"]["clusters"]:
    if c["name"]=="authz": c.pop("transport_socket")
json.dump(b,open("k8s/envoy-downonly.json","w"),indent=1)
b=json.load(open("k8s/envoy-nomcd.json"))
# variant B: only the I3 client certificate through SDS; the listeners use a second, valid Secret path
json.dump(b,open("k8s/envoy-both.json","w"),indent=1)
EOF
$N create configmap envoy-boot2 --from-file=downonly.json=k8s/envoy-downonly.json --from-file=both.json=k8s/envoy-both.json --dry-run=client -o yaml | $N apply -f - >/dev/null
GB=$(printf x | base64)
sec(){ n=$1; shift; d=""; for kv in "$@"; do d="$d\"${kv%%=*}\":\"${kv#*=}\","; done; d=${d%,}; echo "{\"apiVersion\":\"v1\",\"kind\":\"Secret\",\"metadata\":{\"name\":\"$n\",\"namespace\":\"lenny-system\"},\"type\":\"Opaque\",\"data\":{$d}}" | $N apply -f - >/dev/null; }
sec g-down "tls.crt=$GB" "tls.key=$GB"; sec g-both "tls.crt=$GB" "tls.key=$GB"
for v in "g-down downonly.json" "g-both both.json"; do set -- $v; sed -e "s/NAME/$1/;s/APP/r/;s/BOOT/$2/;s/SECRET/$1/;s/OPTIONAL/false/;s/name: envoy-boot/name: envoy-boot2/" k8s/envoy.tmpl | $K apply -f - >/dev/null; done
sleep 40
for p in g-down g-both; do
 echo "$p ready=$($N get pod $p -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}') restarts=$($N get pod $p -o jsonpath='{.status.containerStatuses[?(@.name=="envoy")].restartCount}') admin_ready=[$($N exec $p -c wleaf -- curl -s -m 2 localhost:9901/ready 2>&1 | head -1 | cut -c1-30)]"
 $N logs $p -c envoy | grep -E "rejected|critical" | cut -c1-190 | tail -2
 $K -n lenny-system get events --field-selector involvedObject.name=$p --sort-by=.lastTimestamp -o custom-columns=T:.type,R:.reason,C:.count,M:.message --no-headers | grep Warning | tail -1 | cut -c1-170
done
$N delete pod g-down g-both --wait=false >/dev/null
