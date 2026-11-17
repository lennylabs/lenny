#!/bin/bash
cd "$(dirname "$0")"; D=$PWD; P=198.19.0.41:3128
st(){ docker exec ev-client wget -qO- http://198.19.0.11:9002/stats; }
ends(){ st | sed -E 's/.*(tunnelEnds=[0-9]+).*/\1/'; }
echo "### F2. Drain as the chart does it: --drain-time-s 20, drain, wait, then SIGTERM"
./env.sh reset; ./env.sh dec -e FLUSH=2s; ENVOY_ARGS="--drain-time-s 20" ./env.sh envoy; /bin/sleep 1
docker exec ev-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:t0 -tenants 5 -rate 200 -n 1000 -hold 120s -bucket 600s > /tmp/x 2>&1 &
/bin/sleep 8; echo "   open: $(ends)"; curl -s -XPOST 'http://127.0.0.1:19951/drain_listeners?graceful' >/dev/null; /bin/sleep 25; docker stop -t 10 ev-envoy >/dev/null; /bin/sleep 3; echo "   after drain and stop: $(ends)"; docker exec ev-client pkill tcl; wait
echo "### F3. Plain SIGTERM with no drain"
./env.sh reset; ./env.sh dec -e FLUSH=2s; ./env.sh envoy; /bin/sleep 1
docker exec ev-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:t0 -tenants 5 -rate 200 -n 1000 -hold 120s -bucket 600s > /tmp/x 2>&1 &
/bin/sleep 8; docker stop -t 10 ev-envoy >/dev/null; /bin/sleep 3; echo "   after SIGTERM: $(ends)"; docker exec ev-client pkill tcl; wait
echo "### H. Query cost, with no index: find one tunnel by source port across a tenant-hour of objects"
./env.sh reset; ./env.sh dec -e FLUSH=30s -e ALS_SAMPLE=1; ./env.sh envoy; /bin/sleep 1
docker exec ev-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:t0 -tenants 200 -rate 2000 -n 24000 -hold 300ms -bucket 600s >/dev/null 2>&1; /bin/sleep 33
PORT=$(docker exec ev-client wget -qO- http://198.19.0.11:9002/last | python3 -c 'import json,sys; print(json.loads(sys.stdin.read())["sourcePort"])'); echo "   source port $PORT, $(st | cut -d' ' -f1-3)"
docker exec ev-client /b/verify -mode query -port $PORT | cut -c1-150 | sed 's/^/   /'
docker rm -f ev-dec ev-envoy ev-minio ev-pg ev-target ev-client ev-mc >/dev/null 2>&1; docker network rm sev-pub >/dev/null 2>&1
echo "### I. Egress group by label: how long after the stamp is the path open"
cat > /tmp/np.yaml <<'Y'
apiVersion: v1
kind: Namespace
metadata: {name: lenny-system}
---
apiVersion: v1
kind: Namespace
metadata: {name: agents}
---
apiVersion: v1
kind: Pod
metadata: {name: group-a, namespace: lenny-system, labels: {app: group-a}}
spec:
  containers: [{name: s, image: alpine:3.22, imagePullPolicy: IfNotPresent, command: ["sh","-c","while :; do echo ok | nc -l -p 8080 >/dev/null; done"]}]
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: group-a-ingress, namespace: lenny-system}
spec:
  podSelector: {matchLabels: {app: group-a}}
  policyTypes: [Ingress]
  ingress:
  - from:
    - namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: agents}}
      podSelector: {matchLabels: {lenny.dev/egress-group: group-a}}
    ports: [{port: 8080}]
Y
kind create cluster --name lenny-grp --wait 90s 2>&1 | tail -1 | cut -c1-40; K="kubectl --context kind-lenny-grp"; kind load docker-image alpine:3.22 --name lenny-grp >/dev/null 2>&1
$K apply -f /tmp/np.yaml >/dev/null; $K -n lenny-system wait --for=condition=Ready pod/group-a --timeout=60s >/dev/null; IP=$($K -n lenny-system get pod group-a -o jsonpath='{.status.podIP}')
for i in 1 2 3 4 5 6 7 8; do
  $K -n agents run c$i --image=alpine:3.22 --image-pull-policy=IfNotPresent --command -- sleep 600 >/dev/null; $K -n agents wait --for=condition=Ready pod/c$i --timeout=60s >/dev/null
  /bin/sleep 2
  if $K -n agents exec c$i -- sh -c "nc -w 1 $IP 8080 </dev/null >/dev/null 2>&1"; then B=open; else B=dropped; fi
  $K -n agents exec c$i -- sh -c "t0=\$(date +%s%N); : > /tmp/go; i=0; while [ ! -e /tmp/stop ] && [ \$i -lt 400 ]; do if nc -w 1 $IP 8080 </dev/null >/dev/null 2>&1; then echo \$(( (\$(date +%s%N)-t0)/1000000 )); break; fi; i=\$((i+1)); done" > /tmp/lat$i &
  /bin/sleep 0.2; $K -n agents label pod c$i lenny.dev/egress-group=group-a >/dev/null; wait; echo "   sample $i: before stamp=$B, open $(cat /tmp/lat$i) ms after the probe began"
done
kind delete cluster --name lenny-grp 2>&1 | tail -1 | cut -c1-40
