#!/bin/bash
# ld.sh NAME [loadgen flags] : run loadgen on sr3-pub while sampling docker stats of r3-envoy and r3-authz every second.
cd "$(dirname "$0")"; N=$1; shift
( while true; do docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}}' r3-envoy r3-authz | tr '\n' ' '; echo " $(date +%T)"; sleep 1; done ) > logs/t4-$N.stats 2>&1 &
SP=$!
docker run --rm --name r3-loadgen --network sr3-pub --ulimit nofile=65536:65536 -v $PWD/bin:/b:ro alpine:3.22 /b/loadgen "$@" 2>&1 | tee logs/t4-$N.txt
kill $SP 2>/dev/null
echo "## stats (envoy cpu, mem) min/max:"; awk '{print $2, $3}' logs/t4-$N.stats | sort -t% -k1 -n | sed -n '1p;$p'
