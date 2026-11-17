#!/bin/bash
# Start the r3 spike networks and helper containers (containers r3-*, networks sr3-*).
set -e
cd "$(dirname "$0")"; D=$PWD
docker network create --ipv6 --subnet 198.18.0.0/24 --subnet 2001:db8:1::/64 sr3-pub >/dev/null
docker network create --ipv6 --subnet 10.99.0.0/24 --subnet fd00:99::/64 sr3-priv >/dev/null
docker network create --ipv6 --ipv4=false --subnet 2001:db8:2::/64 sr3-v6only >/dev/null
run() { docker run -d --name "$1" "${@:2}" >/dev/null; }
run r3-authz    --network sr3-pub --ip 198.18.0.11 -v $D/bin:/b:ro alpine:3.22 /b/${AUTHZ:-authz}
run r3-proxya   --network sr3-pub --ip 198.18.0.12 -v $D/bin:/b:ro alpine:3.22 /b/upproxy2
run r3-proxyb   --network sr3-pub --ip 198.18.0.13 -e REQUIRE_AUTH=tenant-globex:upstream-secret -v $D/bin:/b:ro alpine:3.22 /b/upproxy2
run r3-proxya-auth --network sr3-pub --ip 198.18.0.14 -e REQUIRE_AUTH=tenant-globex:upstream-secret -v $D/bin:/b:ro alpine:3.22 /b/upproxy2
run r3-proxyd   --network sr3-pub --ip 198.18.0.15 -e DENY403=1 -v $D/bin:/b:ro alpine:3.22 /b/upproxy2
run r3-target   --network sr3-pub --ip 198.18.0.20 --ip6 2001:db8:1::20 --network-alias target.spike -v $D/bin:/b:ro alpine:3.22 /b/target
run r3-target6  --network sr3-v6only --ip6 2001:db8:2::20 -v $D/bin:/b:ro alpine:3.22 /b/target
run r3-internal --network sr3-priv --ip 10.99.0.5 --ip6 fd00:99::5 --network-alias internal.spike -v $D/bin:/b:ro alpine:3.22 /b/target
run r3-dns      --network sr3-pub --ip 198.18.0.53 -v $D/bin:/b:ro alpine:3.22 /b/dnsd
run r3-client   --network sr3-pub --ip 198.18.0.30 --entrypoint sleep curlimages/curl:latest infinity
docker ps --filter name=r3- --format '{{.Names}} {{.Status}}'
