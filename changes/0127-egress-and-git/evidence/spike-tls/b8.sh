#!/bin/bash
cd "$(dirname "$0")"; D=$PWD; P=198.19.0.41:3128; X="-proxy-tls -proxy-ca /c/ca1.pem"
docker network create --subnet 198.19.0.0/24 stls-pub >/dev/null 2>&1
docker run -d --name t-target --network stls-pub --ip 198.19.0.20 --network-alias target.spike --ulimit nofile=65536:65536 -v $D/bin:/b:ro alpine:3.22 /b/target >/dev/null
docker run -d --name t-client --network stls-pub --ip 198.19.0.30 --ulimit nofile=65536:65536 -v $D/bin:/b:ro -v $D/certs:/c:ro alpine:3.22 sleep infinity >/dev/null
authz(){ docker rm -f t-authz1 t-authz2 >/dev/null 2>&1; for n in 1 2; do docker run -d --name t-authz$n --network stls-pub --ip 198.19.0.1$n -e NAME=authz$n $1 -v $D/bin:/b:ro -v $D/certs:/c:ro alpine:3.22 sh -c 'while :; do /b/authz; sleep 2; done' >/dev/null; done; /bin/sleep 2; }
run(){ # label envoycfg
  ./b-envoy.sh $2; /bin/sleep 3
  docker exec t-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:good -rate 500 -n 30000 -hold 200ms -bucket 300s $X > /tmp/r.out 2>&1 &
  /bin/sleep 6; S1=$(docker stats --no-stream --format '{{.CPUPerc}} {{.MemUsage}}' t-envoy | cut -d/ -f1); /bin/sleep 4
  docker exec t-authz1 pkill -9 authz; /bin/sleep 20; docker pause t-authz2 >/dev/null; /bin/sleep 8; docker unpause t-authz2 >/dev/null; wait
  echo "$1: envoy[$S1] $(tail -1 /tmp/r.out | cut -c1-150)"; /bin/sleep 62
}
authz ""; run "I2 TLS, I3 plain" tls
authz "-e TLS_CERT=/c/good/tls.crt -e TLS_KEY=/c/good/tls.key"; run "I2 TLS, I3 TLS  " i3
echo "### I3 fails closed on an untrusted decision service, and on a decision service speaking plaintext"
authz "-e TLS_CERT=/c/ca2leaf/tls.crt -e TLS_KEY=/c/ca2leaf/tls.key"; ./b-envoy.sh i3; /bin/sleep 3
echo "untrusted: $(docker exec t-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:good -rate 20 -n 3 -hold 50ms -bucket 60s $X 2>&1 | tail -1 | cut -c1-90)"
authz ""; ./b-envoy.sh i3; /bin/sleep 3
echo "plaintext: $(docker exec t-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:good -rate 20 -n 3 -hold 50ms -bucket 60s $X 2>&1 | tail -1 | cut -c1-90)"
docker rm -f t-envoy t-client t-target t-authz1 t-authz2 >/dev/null 2>&1; docker network rm stls-pub >/dev/null 2>&1; docker volume rm stls-certs >/dev/null 2>&1; echo cleaned
