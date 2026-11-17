#!/bin/bash
cd "$(dirname "$0")"; D=$PWD
docker network create --subnet 198.19.0.0/24 stls-pub >/dev/null 2>&1
run() { docker run -d --name "$1" "${@:2}" >/dev/null; }
for n in 1 2; do run t-authz$n --network stls-pub --ip 198.19.0.1$n -e NAME=authz$n -v $D/bin:/b:ro alpine:3.22 sh -c 'while :; do /b/authz; sleep 2; done'; done
run t-target --network stls-pub --ip 198.19.0.20 --network-alias target.spike --ulimit nofile=65536:65536 -v $D/bin:/b:ro alpine:3.22 /b/target
run t-client --network stls-pub --ip 198.19.0.30 --ulimit nofile=65536:65536 -v $D/bin:/b:ro -v $D/certs:/c:ro alpine:3.22 sleep infinity
docker exec t-client apk add -q openssl curl >/dev/null 2>&1
