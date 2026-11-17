#!/bin/bash
# soak.sh NAME MINUTES [gen4 args...] : start r4-envoy-soak (198.19.0.40, admin 19921) with the given config args,
# open 200 CONNECT tunnels to the ticking target (1 chunk/s) plus 2 idle-control tunnels (silent target :7001),
# sample Envoy memory every minute, and collect everything under logs/NAME-*.
cd "$(dirname "$0")"; N=$1; M=$2; shift 2
./gen4.py envoy/$N.json "$@" || exit 1
ENVOY_DOCKER="--cpus 1" ./envoy.sh soak 40 19921 envoy/$N.json --concurrency 1 || exit 1
echo "config args: $*" > logs/$N-mem.txt
docker rm -f r4-soak r4-soak-idle >/dev/null 2>&1
docker run -d --name r4-soak --network sr4-pub --ulimit nofile=65536:65536 -v $PWD/bin:/b:ro alpine:3.22 \
  /b/soak -proxy 198.19.0.40:3128 -n 200 -rate 50 -max ${M}m -report 60s >/dev/null
docker run -d --name r4-soak-idle --network sr4-pub -v $PWD/bin:/b:ro alpine:3.22 \
  /b/soak -proxy 198.19.0.40:3128 -target target.spike:7001 -n 2 -rate 2 -max ${M}m -report 60s >/dev/null
S0=$(date +%s)
for i in $(seq 0 $M); do
  MEM=$(docker stats --no-stream --format '{{.MemUsage}} cpu={{.CPUPerc}}' r4-envoy-soak)
  AL=$(curl -s 'http://127.0.0.1:19921/memory' | tr -d ' \n' | sed 's/.*"allocated":"\([0-9]*\)".*"heap_size":"\([0-9]*\)".*/alloc=\1 heap=\2/')
  ST=$(curl -s 'http://127.0.0.1:19921/stats?filter=^(http.egress.downstream_cx_active|http.egress.downstream_rq_active|cluster.dfp.upstream_cx_active|http.egress.downstream_rq_max_duration_reached|http.egress.downstream_rq_idle_timeout|server.memory_allocated)$' | tr '\n' ' ')
  LOGN=$(docker logs r4-envoy-soak 2>/dev/null | grep -c '"type":"DownstreamPeriodic"')
  echo "min=$i t=$(( $(date +%s) - S0 ))s docker=$MEM $AL periodic_log_lines=$LOGN | $ST" | tee -a logs/$N-mem.txt
  [ $i -lt $M ] && sleep 60
done
sleep 20
docker logs r4-soak > logs/$N-soak.txt 2>&1
docker logs r4-soak-idle > logs/$N-idle.txt 2>&1
docker logs r4-envoy-soak 2>&1 | grep '^{' > logs/$N-accesslog.jsonl
curl -s 'http://127.0.0.1:19921/stats' | grep -E 'egress.downstream_(rq_(total|active|max_duration_reached|idle_timeout|tx_reset|rx_reset)|cx_(total|active|destroy.*|idle_timeout|max_duration))|dfp.upstream_cx_(total|active|destroy.*)|http1.*' | grep -v ': 0$' > logs/$N-stats.txt
echo "soak $N done"; tail -3 logs/$N-soak.txt; tail -3 logs/$N-idle.txt
