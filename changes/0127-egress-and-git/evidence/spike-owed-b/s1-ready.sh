#!/bin/bash
# Kubelet httpGet scheme HTTPS against the TLS readiness listener in each state, then recovery when the Secret heals.
R=spike-owed-b; cd $R; . ./kc.sh pki
N="$K -n lenny-system"
CRT=$($N get secret lenny-egress-tls -o jsonpath='{.data.tls\.crt}')
KEY=$($N get secret lenny-egress-tls -o jsonpath='{.data.tls\.key}')
OKEY=$($N get secret lenny-controller-tls -o jsonpath='{.data.tls\.key}')
SPT=$R/../spike-tls/certs/expired
XC=$(base64 < $SPT/tls.crt | tr -d '\n'); XK=$(base64 < $SPT/tls.key | tr -d '\n')
GB=$(printf x | base64)
sec(){ # sec NAME k=v...
  n=$1; shift; d=""; for kv in "$@"; do d="$d\"${kv%%=*}\":\"${kv#*=}\","; done; d=${d%,}
  echo "{\"apiVersion\":\"v1\",\"kind\":\"Secret\",\"metadata\":{\"name\":\"$n\",\"namespace\":\"lenny-system\"},\"type\":\"Opaque\",\"data\":{$d}}" | $N apply -f - >/dev/null
}
sec r-missing "foo=$GB"
sec r-garbage "tls.crt=$GB" "tls.key=$GB"
sec r-mismatch "tls.crt=$CRT" "tls.key=$OKEY"
sec r-expired "tls.crt=$XC" "tls.key=$XK"
sec r-valid "tls.crt=$CRT" "tls.key=$KEY"
pod(){ sed -e "s/NAME/$1/;s/APP/r/;s/BOOT/nomcd.json/;s/SECRET/$2/;s/OPTIONAL/$3/" k8s/envoy.tmpl | $K apply -f - >/dev/null; }
pod r-valid r-valid false
pod r-absent r-absent false
pod r-absentopt r-absentopt true
pod r-missing r-missing false
pod r-garbage r-garbage false
pod r-mismatch r-mismatch false
pod r-expired r-expired false
sleep 45
show(){ for p in r-valid r-absent r-absentopt r-missing r-garbage r-mismatch r-expired; do
  ph=$($N get pod $p -o jsonpath='{.status.phase}'); rd=$($N get pod $p -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')
  st=$($N get pod $p -o jsonpath='{.status.containerStatuses[?(@.name=="envoy")].state}' | cut -c1-70)
  rc=$($N get pod $p -o jsonpath='{.status.containerStatuses[?(@.name=="envoy")].restartCount}')
  live=$($N exec $p -c wleaf -- curl -s -m 2 localhost:9901/ready 2>&1 | head -1 | cut -c1-40)
  hs=$($N exec $p -c wleaf -- sh -c 'curl -sk -m 2 -o /dev/null -w "%{http_code}" https://localhost:3443/healthz; echo " curlrc=$?"' 2>&1 | head -1 | cut -c1-60)
  echo "$p phase=$ph ready=$rd restarts=$rc envoy_state=$st admin_ready=[$live] curl_k=[$hs]"
  $K -n lenny-system get events --field-selector involvedObject.name=$p --sort-by=.lastTimestamp -o custom-columns=T:.type,R:.reason,C:.count,M:.message --no-headers 2>/dev/null | grep -E "Warning" | sed 's/^/    /' | cut -c1-210 | tail -2
done; }
echo "### states after 45 s"; show
echo "### Envoy logs for the broken states"
for p in r-absentopt r-missing r-garbage r-mismatch; do echo "-- $p"; $N logs $p -c envoy | grep -E "warning|error|critical" | grep -v -E "global_downstream|no configured limit" | cut -c1-210 | tail -3; done
echo "### heal: Secrets replaced with valid material, measured from the write to Ready"
readytime(){ p=$1; t0=$(python3 -c 'import time;print(time.time())'); for i in $(seq 1 240); do rd=$($N get pod $p -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'); if [ "$rd" = True ]; then python3 -c "import time;print('$p Ready %.1f s after the Secret write' % (time.time()-$t0))"; return; fi; sleep 1; done; echo "$p NOT Ready after 240 s"; }
sec r-absent "tls.crt=$CRT" "tls.key=$KEY"; readytime r-absent &
sec r-absentopt "tls.crt=$CRT" "tls.key=$KEY"; readytime r-absentopt &
sec r-missing "tls.crt=$CRT" "tls.key=$KEY"; readytime r-missing &
sec r-garbage "tls.crt=$CRT" "tls.key=$KEY"; readytime r-garbage &
sec r-mismatch "tls.crt=$CRT" "tls.key=$KEY"; readytime r-mismatch &
wait
echo "### states after the heal"; show
echo "### a running, ready pod whose Secret turns invalid keeps its last good certificate and stays ready"
serial(){ $N exec r-valid -c wleaf -- sh -c 'echo | openssl s_client -connect localhost:3443 2>/dev/null | openssl x509 -noout -serial -enddate | tr "\n" " "'; echo; }
echo "before: $(serial)"
sec r-valid "tls.crt=$GB" "tls.key=$GB"
for i in $(seq 1 120); do c=$($N exec r-valid -c wleaf -- head -c 1 /certs/live/tls.crt); [ "$c" = x ] && { echo "delivered to the pod after ${i} polls"; break; }; sleep 1; done; sleep 5
echo "after:  $(serial) ready=$($N get pod r-valid -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')"
$N exec r-valid -c wleaf -- sh -c 'curl -s localhost:9901/stats | grep -E "sds\.server\.|sds.egress-ca"; curl -s localhost:9901/certs | head -c 0' | cut -c1-100
$N logs r-valid -c envoy | grep -E "warning|error" | grep -v -E "global_downstream|no configured limit" | tail -3 | cut -c1-210
$N delete pod r-valid r-absent r-absentopt r-missing r-garbage r-mismatch r-expired --wait=false >/dev/null
