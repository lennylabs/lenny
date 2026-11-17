#!/bin/bash
# Start the spike networks and helper containers (everything named se-*).
set -e
cd "$(dirname "$0")"
D=$PWD
docker network create --subnet 198.18.0.0/24 spike-envoy-pub >/dev/null
docker network create --subnet 10.99.0.0/24 spike-envoy-priv >/dev/null
run() { docker run -d --name "$1" "${@:2}" >/dev/null; }
run se-authz    --network spike-envoy-pub --ip 198.18.0.11 -v $D/bin:/b:ro alpine:3.22 /b/authz
run se-proxya   --network spike-envoy-pub --ip 198.18.0.12 -v $D/bin:/b:ro alpine:3.22 /b/upproxy
run se-proxyb   --network spike-envoy-pub --ip 198.18.0.13 -e REQUIRE_AUTH=tenant-globex:upstream-secret -v $D/bin:/b:ro alpine:3.22 /b/upproxy
run se-proxya-auth --network spike-envoy-pub --ip 198.18.0.14 -e REQUIRE_AUTH=tenant-globex:upstream-secret -v $D/bin:/b:ro alpine:3.22 /b/upproxy
run se-target   --network spike-envoy-pub --ip 198.18.0.20 --network-alias target.spike -v $D/bin:/b:ro alpine:3.22 /b/target
run se-internal --network spike-envoy-priv --ip 10.99.0.5 --network-alias internal.spike -v $D/bin:/b:ro alpine:3.22 /b/target
run se-client   --network spike-envoy-pub --ip 198.18.0.30 --entrypoint sleep curlimages/curl:latest infinity
docker ps --filter name=se- --format '{{.Names}} {{.Status}}'
