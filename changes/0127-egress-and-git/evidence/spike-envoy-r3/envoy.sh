#!/bin/bash
# envoy.sh <image-tag> <config-file> [extra envoy args] : (re)start r3-envoy with that config.
# ENVOY_DOCKER (env) adds docker create flags (e.g. --cpus 2).
cd "$(dirname "$0")"; D=$PWD
TAG=$1; CFG=$2; shift 2
docker rm -f r3-envoy >/dev/null 2>&1
docker create --name r3-envoy $ENVOY_DOCKER --network sr3-pub --ip 198.18.0.10 --ip6 2001:db8:1::10 \
  --add-host evil.spike:127.0.0.1 --add-host meta.spike:169.254.169.254 \
  -p 127.0.0.1:19911:9901 -v $D/$CFG:/etc/envoy/envoy.yaml:ro \
  envoyproxy/envoy:$TAG -c /etc/envoy/envoy.yaml --log-level ${LOGLEVEL:-info} "$@" >/dev/null
docker network connect --ip 10.99.0.10 --ip6 fd00:99::10 sr3-priv r3-envoy
docker network connect --ip6 2001:db8:2::10 sr3-v6only r3-envoy
docker start r3-envoy >/dev/null
for i in $(seq 1 60); do curl -sf http://127.0.0.1:19911/ready >/dev/null 2>&1 && { echo "envoy $TAG ready ($CFG)"; exit 0; }; sleep 0.25; done
echo "envoy not ready"; docker logs r3-envoy 2>&1 | grep -v work-in-progress | tail -6; exit 1
