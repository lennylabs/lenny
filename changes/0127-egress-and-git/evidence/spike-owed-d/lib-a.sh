O=spike-owed-d; cd "$O"
py(){ docker exec -w /o od-py python3 "$@"; }
pyup(){ docker inspect od-py >/dev/null 2>&1 || docker run -d --name od-py -v $O:/o python:3.13-alpine sleep 864000 >/dev/null; }
a_up(){
  pyup
  docker network create --subnet 198.19.0.0/24 od-up >/dev/null
  docker network create --internal --subnet 198.19.1.0/24 od-pod >/dev/null
  for n in 1 2; do docker run -d --name od-authz$n --network od-up --ip 198.19.0.1$n --cpus 2 -e NAME=authz$n -v $O/bin:/b:ro alpine:3.22 /b/authz >/dev/null; done
  srv -quiet
  docker create --name od-envoy --cpus 4 --sysctl net.ipv4.ip_local_port_range="1024 65000" --sysctl net.ipv4.tcp_tw_reuse=1 --network od-up --ip 198.19.0.41 --ulimit nofile=200000:200000 -p 127.0.0.1:19981:9901 -v $O/envoy/a-tls.json:/etc/envoy/envoy.yaml:ro -v $O/envoy/sds:/sds:ro -v $O/certs/envoy:/certs/live:ro envoyproxy/envoy:v1.40.2 -c /etc/envoy/envoy.yaml --concurrency 4 --log-level warn >/dev/null
  docker network connect --ip 198.19.1.41 od-pod od-envoy
  docker start od-envoy >/dev/null
  for i in $(seq 1 40); do curl -sf http://127.0.0.1:19981/ready >/dev/null && break; /bin/sleep 0.5; done; curl -s http://127.0.0.1:19981/ready
}
srv(){
  docker rm -f od-up-srv >/dev/null 2>&1
  docker run -d --name od-up-srv --network od-up --ip 198.19.0.20 --ulimit nofile=200000:200000 -v $O/bin:/b:ro -v $O/certs:/certs:ro \
  --network-alias api.example.test --network-alias ghes.example.test --network-alias gitlab.example.test --network-alias registry.example.test --network-alias pypi.example.test --network-alias other.example.test --network-alias llm.example.test --network-alias pinned.example.test --network-alias attacker.example \
  -e ACME_SECRET=ACME-REAL-9f2c71 -e GH_SECRET=GH-REAL-55aa01 -e GITLAB_SECRET=GL-REAL-66dd04 -e NPM_SECRET=NPM-REAL-77bb02 -e PYPI_USER=pypiuser -e PYPI_SECRET=PYPI-REAL-33cc03 alpine:3.22 /b/ocup "$@" >/dev/null
  /bin/sleep 0.5
}
# na [docker flags] -- [netagent flags]
na(){
  DF=(); while [ $# -gt 0 ] && [ "$1" != "--" ]; do DF+=("$1"); shift; done; [ "$1" = "--" ] && shift
  docker rm -f od-na >/dev/null 2>&1; rm -rf run/priv/* run/netagent/*
  docker run -d --name od-na --network od-pod --ip 198.19.1.10 --ulimit nofile=200000:200000 --sysctl net.ipv4.ip_local_port_range="1024 65000" --sysctl net.ipv4.tcp_tw_reuse=1 "${DF[@]}" -v $O/bin:/b:ro -v $O/certs:/certs:ro -v $O/cfg-bindings.json:/cfg/bindings.json:ro -v $O/run/netagent:/run/netagent -v $O/run/priv:/var/netagent alpine:3.22 /b/netagent -proxy 198.19.1.41:3128 -proxy-ca /certs/egress-ca.pem -cred agent:good "$@" >/dev/null
  for i in $(seq 1 25); do [ -s run/netagent/ca.pem ] && break; /bin/sleep 0.2; done; /bin/sleep 0.4
}
