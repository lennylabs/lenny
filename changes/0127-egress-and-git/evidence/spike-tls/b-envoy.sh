#!/bin/bash
# b-envoy.sh plain|tls [envoy args]
cd "$(dirname "$0")"; D=$PWD; M=$1; shift
docker rm -f t-envoy >/dev/null 2>&1
docker run -d --name t-envoy --cpus 2 --network stls-pub --ip 198.19.0.41 --ulimit nofile=65536:65536 -p 127.0.0.1:19941:9901 -v $D/envoy/b-$M.json:/etc/envoy/envoy.yaml:ro -v $D/sds:/sds:ro -v stls-certs:/certs:ro envoyproxy/envoy:${TAG:-v1.40.2} -c /etc/envoy/envoy.yaml --concurrency 2 --log-level warn "$@" >/dev/null
for i in $(seq 1 40); do curl -sf http://127.0.0.1:19941/ready >/dev/null 2>&1 && exit 0; /bin/sleep 0.5; done; echo not ready; docker logs t-envoy 2>&1 | tail -4 | cut -c1-260; exit 1
