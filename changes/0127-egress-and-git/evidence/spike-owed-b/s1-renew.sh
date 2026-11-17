#!/bin/bash
R=spike-owed-b; cd $R; . ./kc.sh pki
$K -n agents create configmap ob-prop --from-literal=v=start
$K apply -f k8s/clients.yaml
$K -n agents wait pod -l app=client --for=condition=Ready --timeout=60s; $K -n agents wait pod -l app=prop --for=condition=Ready --timeout=60s
echo "started $(date -u +%T)"
sleep 500
OUT=out/s1-renew; mkdir -p $OUT
$K -n agents logs soak > $OUT/soak.log 2>&1
$K -n lenny-system logs envoy -c wleaf > $OUT/envoy-wleaf.log
$K -n lenny-system logs watcher -c secrets > $OUT/secrets.log
$K -n lenny-system logs authz > $OUT/authz.log 2>&1
$K -n lenny-system logs writer > $OUT/writer.log
$K -n lenny-system logs ctrl > $OUT/ctrl.log
for p in c1 c2 c3; do $K -n agents logs $p -c probe > $OUT/$p.log; $K -n agents logs $p -c wca > $OUT/$p-wca.log; done
for p in p1 p2 p3 p4 p5 psub; do $K -n agents logs $p > $OUT/$p.log; done
$K -n lenny-system exec envoy -c wleaf -- curl -s localhost:9901/stats > $OUT/envoy-stats.txt
$K -n lenny-system exec envoy -c wleaf -- curl -s localhost:9901/certs > $OUT/envoy-certs.json
$K -n lenny-system logs envoy -c envoy | grep -v -E "server.cc:4|^\s*$" | grep -E "warning|error" | cut -c1-240 > $OUT/envoy-warn.log
$K -n lenny-system get pod envoy -o jsonpath='{.status.containerStatuses[*].restartCount}{"\n"}' > $OUT/envoy-restarts.txt
