#!/bin/bash
# envoy.sh NAME IP ADMINPORT
cd "$(dirname "$0")"; D=$PWD
docker rm -f ls-envoy-$1 >/dev/null 2>&1
docker run -d --name ls-envoy-$1 --network sls-pub --ip 198.19.0.$2 --ulimit nofile=65536:65536 -p 127.0.0.1:$3:9901 -v $D/envoy/final.json:/etc/envoy/envoy.yaml:ro envoyproxy/envoy:v1.40.2 -c /etc/envoy/envoy.yaml --log-level warn >/dev/null
for i in $(seq 1 60); do curl -sf http://127.0.0.1:$3/ready >/dev/null 2>&1 && { echo "envoy $1 ready"; exit 0; }; sleep 0.5; done; echo "envoy not ready"; docker logs ls-envoy-$1 2>&1 | tail -5; exit 1
