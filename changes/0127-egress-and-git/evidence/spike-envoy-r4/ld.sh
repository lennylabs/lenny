#!/bin/bash
# ld.sh NAME [loadgen flags] : run loadgen on sr4-pub while sampling docker stats of r4-envoy-main every second
# and the authz cluster stats (active rq, ejections) every 0.5 s.
cd "$(dirname "$0")"; N=$1; shift
( while true; do docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}}' r4-envoy-main | tr '\n' ' '; echo " $(date +%T)"; sleep 1; done ) > logs/$N.stats 2>&1 &
SP=$!
( while true; do echo "$(date +%T) $(curl -s 'http://127.0.0.1:19912/stats?filter=cluster.authz.(upstream_rq_active|outlier_detection.ejections_active|upstream_cx_active|membership_healthy)$' | tr '\n' ' ')"; sleep 0.5; done ) > logs/$N.gauges 2>&1 &
GP=$!
docker run --rm --name r4-loadgen --network sr4-pub --ulimit nofile=65536:65536 -v $PWD/bin:/b:ro alpine:3.22 /b/loadgen "$@" 2>&1 | tee logs/$N.txt
kill $SP $GP 2>/dev/null
echo "## envoy cpu/mem min..max:"; awk '{print $2, $3}' logs/$N.stats | sort -t% -k1 -n | sed -n '1p;$p'
echo "## max authz upstream_rq_active sampled: $(grep -o 'upstream_rq_active: [0-9]*' logs/$N.gauges | awk '{print $2}' | sort -n | tail -1)"
