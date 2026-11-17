#!/bin/bash
# up.sh: the internal network, the stub, and the long-lived client. The network is --internal, so the client and netagent have no route off it.
O=$(cd "$(dirname "$0")" && pwd); cd "$O"
docker network inspect gh-pod >/dev/null 2>&1 || docker network create --internal gh-pod >/dev/null
docker rm -f gh-stub >/dev/null 2>&1
HOSTS=ghes.example.test,gitlab.example.test,api.example.test,api.github.com,uploads.github.com,github.com,other.example.test
mkdir -p run/stub run/trust run/na/ca run/na/anchor
A=(); for h in ${HOSTS//,/ }; do A+=(--network-alias $h); done
docker run -d --name gh-stub --cpus 1 --memory 256m --network gh-pod "${A[@]}" -e STUB_HOSTS=$HOSTS -v $O/bin:/b:ro -v $O/run/stub:/out alpine:3.22 /b/stub /out/stub-ca.pem >/dev/null
for i in 1 2 3 4 5 6 7 8 9 10; do [ -s run/stub/stub-ca.pem ] && break; /bin/sleep 0.3; done
