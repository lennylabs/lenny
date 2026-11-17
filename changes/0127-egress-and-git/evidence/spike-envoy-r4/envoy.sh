#!/bin/bash
# envoy.sh NAME IP ADMINPORT CFG [extra envoy args] : (re)start r4-envoy-NAME at 198.19.0.IP, admin on 127.0.0.1:ADMINPORT.
# ENVOY_DOCKER (env) adds docker create flags (e.g. --cpus 2).
cd "$(dirname "$0")"; D=$PWD
NAME=$1; IP=$2; AP=$3; CFG=$4; shift 4
docker rm -f r4-envoy-$NAME >/dev/null 2>&1
docker run -d --name r4-envoy-$NAME $ENVOY_DOCKER --network sr4-pub --ip 198.19.0.$IP --ulimit nofile=65536:65536 \
  -p 127.0.0.1:$AP:9901 -v $D/$CFG:/etc/envoy/envoy.yaml:ro \
  envoyproxy/envoy:${TAG:-v1.40.2} -c /etc/envoy/envoy.yaml --log-level ${LOGLEVEL:-info} "$@" >/dev/null
for i in $(seq 1 60); do curl -sf http://127.0.0.1:$AP/ready >/dev/null 2>&1 && { echo "envoy $NAME ready ($CFG) $(curl -s http://127.0.0.1:$AP/server_info | grep -o '"version": "[^"]*"')"; exit 0; }; sleep 0.25; done
echo "envoy not ready"; docker logs r4-envoy-$NAME 2>&1 | grep -i "error\|critical" | cut -c1-400 | tail -3; exit 1
