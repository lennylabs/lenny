#!/bin/bash
# t3.sh NAME DELAY_MS [RATE] : set the decision-service delay on both endpoints, run RATE CONNECT/s for 20 s (hold 1 s),
# and report CONNECT latency plus the in-flight check histogram seen by each decision service and by Envoy.
cd "$(dirname "$0")"; N=$1; D=$2; RATE=${3:-1000}
for ip in 198.19.0.11 198.19.0.12; do docker exec r4-client curl -s "$ip:9002/delay?ms=$D" >/dev/null; docker exec r4-client curl -s "$ip:9002/reset" >/dev/null; done
curl -s -X POST 'http://127.0.0.1:19912/reset_counters' >/dev/null
./ld.sh $N -rate $RATE -n 0 -dur 20s -hold 1s -bucket 1s 2>&1 | grep -v "Terminated\|^    \|^done" | tail -8
for ip in 198.19.0.11 198.19.0.12; do docker exec r4-client curl -s "$ip:9002/hist" | head -1; done
./st.sh 19912 'cluster.authz.upstream_rq_(total|active|pending_overflow|timeout|2xx|5xx|retry)$|cluster.authz.upstream_cx_(total|active)$|http.egress.ext_authz'
echo "## envoy authz upstream_rq_active samples (0.5 s): $(grep -o 'upstream_rq_active: [0-9]*' logs/$N.gauges | awk '{print $2}' | sort -n | uniq -c | sort -k2 -n | awk '{printf "%s x%s  ", $2, $1}')"
for ip in 198.19.0.11 198.19.0.12; do docker exec r4-client curl -s "$ip:9002/delay?ms=0" >/dev/null; done
