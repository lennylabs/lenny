#!/bin/bash
cd "$(dirname "$0")"; D=$PWD; P=198.19.0.41:3128; X="-proxy-tls -proxy-ca /c/ca1.pem"
docker volume create stls-certs >/dev/null; docker volume create stls-ec >/dev/null
# Envoy's client certificate at /certs/live for this run: a certificate carrying both the server and allowlisted client SAN stands in for two.
docker network create --subnet 198.19.0.0/24 stls-pub >/dev/null 2>&1
docker run -d --name t-target --network stls-pub --ip 198.19.0.20 --network-alias target.spike --ulimit nofile=65536:65536 -v $D/bin:/b:ro alpine:3.22 /b/target >/dev/null
docker run -d --name t-client --network stls-pub --ip 198.19.0.30 --ulimit nofile=65536:65536 -v $D/bin:/b:ro -v $D/certs:/c:ro alpine:3.22 sleep infinity >/dev/null
docker exec t-client apk add -q curl openssl >/dev/null 2>&1
authz(){ docker rm -f t-authz1 t-authz2 >/dev/null 2>&1; for n in 1 2; do docker run -d --name t-authz$n --network stls-pub --ip 198.19.0.1$n -e NAME=authz$n "$@" -v $D/bin:/b:ro -v $D/certs:/c:ro alpine:3.22 sh -c 'while :; do /b/authz; sleep 2; done' >/dev/null; done; /bin/sleep 2; }
one(){ docker exec t-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:good -rate 20 -n 3 -hold 50ms -bucket 60s $X 2>&1 | tail -1 | cut -c1-70; }
SV="-e TLS_CERT=/c/good/tls.crt -e TLS_KEY=/c/good/tls.key -e TLS_CLIENT_CA=/c/ca1.pem"
./rotate.sh good
echo "### I3 mTLS, SAN allowlist, faults at 500/s"
authz $SV -e TLS_CLIENT_SANS=lenny-egress.lenny-system.svc; ./b-envoy.sh i3m; /bin/sleep 3
echo "allowed client: $(one)"
docker exec t-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:good -rate 500 -n 30000 -hold 200ms -bucket 300s $X > /tmp/r.out 2>&1 &
/bin/sleep 10; docker exec t-authz1 pkill -9 authz; /bin/sleep 20; docker pause t-authz2 >/dev/null; /bin/sleep 8; docker unpause t-authz2 >/dev/null; wait
echo "faults: $(tail -1 /tmp/r.out | cut -c1-150)"; /bin/sleep 62
echo "### no faults, I3 plain against I3 mTLS, 500/s for 30 s, for the p99 the earlier run could not separate"
authz; ./b-envoy.sh tls; /bin/sleep 3; docker exec t-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:good -rate 500 -n 15000 -hold 200ms -bucket 300s $X 2>&1 | tail -1 | cut -c1-120 | sed 's/^/I3 plain: /'; /bin/sleep 62
authz $SV -e TLS_CLIENT_SANS=lenny-egress.lenny-system.svc; ./b-envoy.sh i3m; /bin/sleep 3; docker exec t-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:good -rate 500 -n 15000 -hold 200ms -bucket 300s $X 2>&1 | tail -1 | cut -c1-120 | sed 's/^/I3 mTLS : /'
echo "### a client outside the allowlist, and one with no certificate, are refused by the decision service"
authz $SV -e TLS_CLIENT_SANS=lenny-egress-client.lenny-system.svc; ./b-envoy.sh i3m; /bin/sleep 3; echo "SAN not allowed: $(one)"
docker exec t-client sh -c "echo | openssl s_client -connect 198.19.0.11:9000 -alpn h2 -CAfile /c/ca1.pem 2>&1 | grep -E 'Verification|alert|error' | head -2 | cut -c1-110 | tr '\n' ' '"; echo " <- no client certificate"
authz $SV -e TLS_CLIENT_SANS=lenny-egress.lenny-system.svc
echo "### readiness through a TLS handshake (kubelet httpGet HTTPS skips verification): states"
probe(){ docker exec t-client sh -c 'curl -sk -m 3 -o /dev/null -w "%{http_code}" https://198.19.0.41:3443/healthz; echo " rc=$?"'; }
cold(){ # cold NAME setup: start Envoy with a volume prepared by setup
  docker rm -f t-envoy >/dev/null 2>&1; docker volume rm stls-cold >/dev/null 2>&1; docker volume create stls-cold >/dev/null
  docker run --rm -v stls-cold:/certs -v $D/certs:/src:ro alpine:3.22 sh -c "$2"
  docker run -d --name t-envoy --cpus 2 --network stls-pub --ip 198.19.0.41 -p 127.0.0.1:19941:9901 -v $D/envoy/b-ready.json:/etc/envoy/envoy.yaml:ro -v $D/sds:/sds:ro -v stls-cold:/certs envoyproxy/envoy:v1.40.2 -c /etc/envoy/envoy.yaml --concurrency 2 --log-level warn >/dev/null
  /bin/sleep 3; echo "$1: state=$(docker inspect -f '{{.State.Status}}' t-envoy) /ready=$(curl -s -m2 http://127.0.0.1:19941/ready | head -1) tlsprobe=$(probe) client=[$(one)]"
}
MK='mkdir -p /certs/live/..a; ln -s ..a /certs/live/..data; ln -s ..data/tls.crt /certs/live/tls.crt; ln -s ..data/tls.key /certs/live/tls.key;'
cold "valid at start   " "$MK cp /src/good/tls.* /certs/live/..a/; chmod -R a+rX /certs"
cold "directory missing" "true"
cold "empty mount      " "mkdir -p /certs/live; chmod -R a+rX /certs"
cold "keys missing     " "$MK chmod -R a+rX /certs"
cold "garbage material " "$MK echo x > /certs/live/..a/tls.crt; echo x > /certs/live/..a/tls.key; chmod -R a+rX /certs"
echo "### recovery of the garbage case when valid material arrives"
docker run --rm -v stls-cold:/certs -v $D/certs:/src:ro alpine:3.22 sh -c 'mkdir -p /certs/live/..b; cp /src/good/tls.* /certs/live/..b/; chmod -R a+rX /certs; ln -s ..b /certs/live/..tmp; mv -T /certs/live/..tmp /certs/live/..data'; /bin/sleep 3
echo "after valid: /ready=$(curl -s -m2 http://127.0.0.1:19941/ready | head -1) tlsprobe=$(probe) client=[$(one)]"
docker rm -f t-envoy t-client t-target t-authz1 t-authz2 >/dev/null 2>&1; docker network rm stls-pub >/dev/null 2>&1; docker volume rm stls-certs stls-ec stls-cold >/dev/null 2>&1; echo cleaned
