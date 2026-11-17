#!/bin/bash
# a-na.sh [--cpus X] [--memory M] [netagent flags...]: (re)starts the netagent container. Docker flags come before "--".
O=$(cd "$(dirname "$0")" && pwd); cd "$O"
DF=(); while [ $# -gt 0 ] && [ "$1" != "--" ]; do DF+=("$1"); shift; done; [ "$1" = "--" ] && shift
docker rm -f oc-na >/dev/null 2>&1
mkdir -p run/netagent run/priv run/trust
docker run -d --name oc-na --network oc-pod --ip 198.19.1.10 --ulimit nofile=200000:200000 --sysctl net.ipv4.ip_local_port_range="1024 65000" --sysctl net.ipv4.tcp_tw_reuse=1 "${DF[@]}" -v $O/bin:/b:ro -v $O/certs:/certs:ro -v $O/cfg-bindings.json:/cfg/bindings.json:ro -v $O/run/netagent:/run/netagent -v $O/run/priv:/var/netagent alpine:3.22 /b/netagent -proxy 198.19.1.41:3128 -proxy-ca /certs/egress-ca.pem -cred agent:good "$@" >/dev/null
for i in $(seq 1 50); do docker exec oc-na wget -qO- http://127.0.0.1:9100/stats >/dev/null 2>&1 && break; /bin/sleep 0.2; done
cat run/netagent/ca.pem certs/pub-ca.pem > run/trust/trust.pem; cp certs/pub-ca.pem run/trust/pub-ca.pem; cp run/netagent/ca.pem run/trust/pod-ca.pem
docker logs oc-na 2>&1 | grep -m1 per-pod
