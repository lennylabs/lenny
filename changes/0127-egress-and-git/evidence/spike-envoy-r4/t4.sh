#!/bin/bash
# t4.sh NAME MODE [DRAIN_S] : 200 live tunnels (ticking) + one fresh CONNECT probe per second through r4-envoy-main
# started with --drain-time-s DRAIN_S (default 20); at t=8 s apply MODE:
#   graceful       POST /drain_listeners?graceful
#   graceful-skip  POST /drain_listeners?graceful&skip_exit
#   immediate      POST /drain_listeners
#   sigterm        docker kill -s TERM
# Samples container state and listener/connection stats every second until the soak client ends.
cd "$(dirname "$0")"; N=$1; MODE=$2; DS=${3:-20}
ENVOY_DOCKER="--cpus 2" ./envoy.sh main 10 19912 ${CFG:-envoy/final.json} --concurrency 2 --drain-time-s $DS --drain-strategy ${STRATEGY:-gradual} || exit 1
docker rm -f r4-soak4 >/dev/null 2>&1
docker run -d --name r4-soak4 --network sr4-pub --ulimit nofile=65536:65536 -v $PWD/bin:/b:ro alpine:3.22 \
  /b/soak -proxy 198.19.0.10:3128 -n 200 -rate 100 -max ${MAX:-70s} -report 10s -probe 1s -probe-for ${PROBEFOR:-60s} >/dev/null
S0=$(date +%s)
( sleep ${DRAIN_AT:-8}
  case $MODE in
    graceful)      R=$(curl -s -X POST 'http://127.0.0.1:19912/drain_listeners?graceful') ;;
    graceful-skip) R=$(curl -s -X POST 'http://127.0.0.1:19912/drain_listeners?graceful&skip_exit') ;;
    immediate)     R=$(curl -s -X POST 'http://127.0.0.1:19912/drain_listeners') ;;
    sigterm)       R=$(docker kill -s TERM r4-envoy-main) ;;
  esac
  echo "   [t=$(( $(date +%s) - S0 ))s wallclock $(python3 -c 'import time;print(time.strftime("%H:%M:%S",time.gmtime())+".%03d"%(int(time.time()*1000)%1000))') applied $MODE: $R]" ) &
for i in $(seq 1 ${MAX_S:-72}); do
  ST=$(docker inspect -f '{{.State.Status}} exit={{.State.ExitCode}}' r4-envoy-main 2>/dev/null)
  S=$(curl -s --max-time 0.5 'http://127.0.0.1:19912/stats?filter=^(listener_manager.total_listeners_draining|listener_manager.total_listeners_active|server.state|server.live|http.egress.downstream_cx_active|listener.0.0.0.0_3128.downstream_cx_active|server.total_connections)$' | tr '\n' ' ' | sed 's/listener_manager.total_listeners_//g; s/http.egress.downstream_//; s/listener.0.0.0.0_3128.downstream_//; s/server\.//g')
  echo "t=$(( $(date +%s) - S0 ))s envoy=$ST $S"
  docker inspect -f '{{.State.Status}}' r4-soak4 2>/dev/null | grep -q running || break
  sleep 1
done
wait
sleep 1
echo "--- soak client:"; docker logs r4-soak4 2>&1 | grep -v '^probe' | tail -12
echo "--- probes (first line of each distinct outcome run):"; docker logs r4-soak4 2>&1 | grep '^probe' | awk '{ r=$0; sub(/^probe t= *[0-9.]+s [0-9:.]+ -> /,"",r); sub(/ \(.*/,"",r); if(r!=last){print}; last=r }'
echo "--- envoy log tail:"; docker logs r4-envoy-main 2>&1 | grep -v '^{' | grep -i 'drain\|shut\|exit\|closing\|term' | cut -c1-200 | tail -8
docker logs r4-soak4 > logs/$N-soak.txt 2>&1; docker logs r4-envoy-main 2>&1 | grep -v '^{"authority' > logs/$N-envoy.txt
docker rm -f r4-soak4 >/dev/null 2>&1
