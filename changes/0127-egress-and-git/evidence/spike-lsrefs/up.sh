#!/bin/bash
cd "$(dirname "$0")"; D=$PWD
docker network create --subnet 198.19.0.0/24 sls-pub >/dev/null
run() { docker run -d --name "$1" "${@:2}" >/dev/null; }
run ls-authz1 --network sls-pub --ip 198.19.0.11 -e NAME=authz1 -v $D/bin:/b:ro alpine:3.22 sh -c 'while :; do /b/authz; sleep 2; done'
run ls-authz2 --network sls-pub --ip 198.19.0.12 -e NAME=authz2 -v $D/bin:/b:ro alpine:3.22 sh -c 'while :; do /b/authz; sleep 2; done'
run ls-git --network sls-pub --ip 198.19.0.20 --network-alias git.spike --ulimit nofile=65536:65536 -v $D/bin:/b:ro alpine:3.22 /b/hostile
run ls-client --network sls-pub --ip 198.19.0.30 --ulimit nofile=65536:65536 -v $D/bin:/b:ro alpine:3.22 sleep infinity
