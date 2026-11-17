#!/bin/bash
# envoy.sh <image-tag> <config-file> : (re)start se-envoy with that config.
cd "$(dirname "$0")"
D=$PWD
docker rm -f se-envoy >/dev/null 2>&1
docker create --name se-envoy $ENVOY_EXTRA --network spike-envoy-pub --ip 198.18.0.10 \
  --add-host evil.spike:127.0.0.1 --add-host meta.spike:169.254.169.254 \
  -p 127.0.0.1:19901:9901 -v $D/$2:/etc/envoy/envoy.yaml:ro \
  envoyproxy/envoy:$1 -c /etc/envoy/envoy.yaml --log-level info >/dev/null
docker network connect --ip 10.99.0.10 spike-envoy-priv se-envoy
docker start se-envoy >/dev/null
for i in $(seq 1 40); do curl -sf http://127.0.0.1:19901/ready >/dev/null 2>&1 && { echo "envoy $1 ready"; exit 0; }; sleep 0.25; done
echo "envoy not ready"; docker logs se-envoy 2>&1 | tail -30; exit 1
