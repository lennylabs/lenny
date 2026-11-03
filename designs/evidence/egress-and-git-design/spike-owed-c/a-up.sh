#!/bin/bash
# a-up.sh: networks, decision stand-in, Envoy with server TLS, fake upstreams, git server. Part A.
O=$(cd "$(dirname "$0")" && pwd); cd "$O"; SP=$(dirname "$O")
docker network create --subnet 198.19.0.0/24 oc-up >/dev/null
docker network create --internal --subnet 198.19.1.0/24 oc-pod >/dev/null
for n in 1 2; do docker run -d --name oc-authz$n --network oc-up --ip 198.19.0.1$n --cpus 2 -e NAME=authz$n -v $SP/spike-tls/bin:/b:ro alpine:3.22 /b/authz >/dev/null; done
docker run -d --name oc-up-srv --network oc-up --ip 198.19.0.20 --ulimit nofile=200000:200000 -v $O/bin:/b:ro -v $O/certs:/certs:ro \
  --network-alias api.example.test --network-alias ghes.example.test --network-alias gitlab.example.test --network-alias registry.example.test --network-alias pypi.example.test --network-alias other.example.test --network-alias llm.example.test --network-alias pinned.example.test --network-alias attacker.example \
  -e ACME_SECRET=ACME-REAL-9f2c71 -e GH_SECRET=GH-REAL-55aa01 -e GITLAB_SECRET=GL-REAL-66dd04 -e NPM_SECRET=NPM-REAL-77bb02 -e PYPI_USER=pypiuser -e PYPI_SECRET=PYPI-REAL-33cc03 alpine:3.22 /b/ocup >/dev/null
docker volume create oc-repos >/dev/null
docker run --rm -v oc-repos:/repos oc-gitimg sh -c 'git config --global init.defaultBranch main; git init -q --bare /repos/r.git; cd /tmp && git init -q w && cd w && git -c user.email=a@b -c user.name=a commit -q --allow-empty -m one && echo hello > f && git add f && git -c user.email=a@b -c user.name=a commit -q -m two && git push -q /repos/r.git main && git -C /repos/r.git rev-parse main'
docker run -d --name oc-git --network oc-up --ip 198.19.0.21 --network-alias vcs.example.test --network-alias gitplain.example.test --network-alias gitopen.example.test --cpus 2 -v oc-repos:/repos -v $SP/spike-owed-a/bin:/b:ro -v $O/certs:/certs:ro \
  -e GIT_CONFIG_COUNT=2 -e GIT_CONFIG_KEY_0=http.receivepack -e GIT_CONFIG_VALUE_0=true -e GIT_CONFIG_KEY_1=safe.directory -e GIT_CONFIG_VALUE_1='*' oc-gitimg /b/gitsrv -cert /certs/up/tls.crt -key /certs/up/tls.key >/dev/null
docker create --name oc-envoy --cpus ${ENVOY_CPUS:-4} --sysctl net.ipv4.ip_local_port_range="1024 65000" --sysctl net.ipv4.tcp_tw_reuse=1 --network oc-up --ip 198.19.0.41 --ulimit nofile=200000:200000 -p 127.0.0.1:19961:9901 -v $O/envoy/a-tls.json:/etc/envoy/envoy.yaml:ro -v $O/envoy/sds:/sds:ro -v $O/certs/envoy:/certs/live:ro envoyproxy/envoy:v1.40.2 -c /etc/envoy/envoy.yaml --concurrency ${ENVOY_CPUS:-4} --log-level warn >/dev/null
docker network connect --ip 198.19.1.41 oc-pod oc-envoy
docker start oc-envoy >/dev/null
for i in $(seq 1 40); do curl -sf http://127.0.0.1:19961/ready >/dev/null && break; /bin/sleep 0.5; done
curl -s http://127.0.0.1:19961/ready
