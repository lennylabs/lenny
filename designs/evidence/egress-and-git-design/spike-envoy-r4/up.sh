#!/bin/bash
# Start the r4 network and helper containers (containers r4-*, network sr4-pub 198.19.0.0/24).
set -e
cd "$(dirname "$0")"; D=$PWD
docker network create --subnet 198.19.0.0/24 sr4-pub >/dev/null
run() { docker run -d --name "$1" "${@:2}" >/dev/null; }
# authz containers run the binary under a restart loop so a process kill keeps the container (and its IP) alive.
run r4-authz1 --network sr4-pub --ip 198.19.0.11 -e NAME=authz1 -v $D/bin:/b:ro alpine:3.22 sh -c 'while :; do /b/authz3; sleep 2; done'
run r4-authz2 --network sr4-pub --ip 198.19.0.12 -e NAME=authz2 -v $D/bin:/b:ro alpine:3.22 sh -c 'while :; do /b/authz3; sleep 2; done'
run r4-target --network sr4-pub --ip 198.19.0.20 --network-alias target.spike --ulimit nofile=65536:65536 -v $D/bin:/b:ro alpine:3.22 /b/target
run r4-client --network sr4-pub --ip 198.19.0.30 --entrypoint sleep curlimages/curl:latest infinity
docker ps --filter name=r4- --format '{{.Names}} {{.Status}}'
