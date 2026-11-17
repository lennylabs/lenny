#!/bin/bash
# envoy.sh CONFIG.json: (re)starts oa-envoy on both networks. It reaches the private origin only by the address the decision pins.
D=$(cd "$(dirname "$0")" && pwd); O=$D/..
docker rm -f oa-envoy >/dev/null 2>&1
docker create --name oa-envoy --network oa-net --ip 198.19.50.41 --cpus 2 -m 512m --ulimit nofile=65536:65536 -v $D/envoy/$1:/etc/envoy/envoy.yaml:ro -v $D/sds:/sds:ro -v $O/certs:/c:ro envoyproxy/envoy:v1.40.2 -c /etc/envoy/envoy.yaml --concurrency 2 --log-level warn >/dev/null
docker network connect --ip 172.30.50.41 oa-priv oa-envoy
docker start oa-envoy >/dev/null
for i in $(seq 1 40); do docker exec oa-gitc curl -sf http://198.19.50.41:9901/ready >/dev/null 2>&1 && exit 0; /bin/sleep 0.5; done; echo envoy not ready; docker logs oa-envoy 2>&1 | tail -5 | cut -c1-240; exit 1
