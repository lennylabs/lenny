#!/bin/bash
# a-envoy.sh TAG CFG
cd "$(dirname "$0")"; D=$PWD
docker rm -f r3-envoy >/dev/null 2>&1
docker create --name r3-envoy --network sr3-pub --ip 198.18.0.10 --ip6 2001:db8:1::10 --add-host evil.spike:127.0.0.1 --add-host meta.spike:169.254.169.254 \
  -p 127.0.0.1:19911:9901 -v $D/$2:/etc/envoy/envoy.yaml:ro -v $D/sds:/sds:ro -v stls-certs:/certs:ro envoyproxy/envoy:$1 -c /etc/envoy/envoy.yaml --log-level warn >/dev/null
docker network connect --ip 10.99.0.10 --ip6 fd00:99::10 sr3-priv r3-envoy
docker network connect --ip6 2001:db8:2::10 sr3-v6only r3-envoy
docker start r3-envoy >/dev/null
for i in $(seq 1 40); do curl -sf http://127.0.0.1:19911/ready >/dev/null 2>&1 && exit 0; /bin/sleep 0.5; done; echo not ready; docker logs r3-envoy 2>&1 | tail -5 | cut -c1-300; exit 1
