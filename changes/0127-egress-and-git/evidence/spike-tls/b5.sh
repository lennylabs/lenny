#!/bin/bash
cd "$(dirname "$0")"; D=$PWD; P=198.19.0.41:3128; X="-proxy-tls -proxy-ca /c/ca1.pem"
echo "### cold start without a certificate: what does a client see, and does it recover"
docker rm -f t-envoy >/dev/null 2>&1; docker volume create stls-cold >/dev/null
docker run -d --name t-envoy --cpus 2 --network stls-pub --ip 198.19.0.41 -p 127.0.0.1:19941:9901 -v $D/envoy/b-tls.json:/etc/envoy/envoy.yaml:ro -v $D/sds:/sds:ro -v stls-cold:/certs envoyproxy/envoy:v1.40.2 -c /etc/envoy/envoy.yaml --concurrency 2 --log-level info >/dev/null
/bin/sleep 3; echo "ready=$(curl -s -m2 http://127.0.0.1:19941/ready | head -1) listeners=$(curl -s -m2 http://127.0.0.1:19941/listeners | head -2 | tr '\n' ' ' | cut -c1-80)"
echo "client: $(docker exec t-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:good -rate 20 -n 3 -hold 50ms -bucket 60s $X 2>&1 | tail -1 | cut -c1-90)"
docker logs t-envoy 2>&1 | grep -iE 'sds|secret|certificate|No such file|warming' | tail -3 | cut -c1-200
docker run --rm -v stls-cold:/certs -v $D/certs:/src:ro alpine:3.22 sh -c 'mkdir -p /certs/live/..a; cp /src/good/tls.* /certs/live/..a/; chmod -R a+rX /certs; ln -s ..a /certs/live/..data; ln -s ..data/tls.crt /certs/live/tls.crt; ln -s ..data/tls.key /certs/live/tls.key'
/bin/sleep 3; echo "after certificate appears: $(docker exec t-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:good -rate 20 -n 3 -hold 50ms -bucket 60s $X 2>&1 | tail -1 | cut -c1-90)"
docker rm -f t-envoy >/dev/null 2>&1; docker volume rm stls-cold >/dev/null 2>&1
echo "### certificate expiry metric"
./rotate.sh good; ./b-envoy.sh tls; curl -s http://127.0.0.1:19941/stats | grep -E 'days_until_first_cert_expiring|seconds_until_first_ocsp' | cut -c1-70; curl -s http://127.0.0.1:19941/certs | python3 -c "import json,sys; d=json.load(sys.stdin); print('certs admin:',[(c['cert_chain'][0]['subject_alt_names'][0]['dns'], c['cert_chain'][0]['days_until_expiration']) for c in d['certificates'] if c['cert_chain']][:1])"
echo "### resolver through the TLS proxy hop"
docker rm -f t-authz1 t-authz2 t-hostile >/dev/null 2>&1
for n in 1 2; do docker run -d --name t-authz$n --network stls-pub --ip 198.19.0.1$n -e NAME=authz$n -e PRIV=198.19.0.21:443 -v $D/bin:/b:ro alpine:3.22 sh -c 'while :; do /b/authz; sleep 2; done' >/dev/null; done
docker run -d --name t-hostile --network stls-pub --ip 198.19.0.21 --network-alias git.spike -v $D/bin:/b:ro alpine:3.22 /b/hostile >/dev/null; /bin/sleep 3
L="docker exec t-client /b/lsrefs -proxy-ca /c/ca1.pem"; E=http://198.19.0.41:3128; E2=http://198.19.0.42:3128
echo "real GitHub : $($L -mode one -proxy $E -repo https://github.com/git/git -ref master)  truth=$(perl -e 'alarm 20; exec @ARGV' git ls-remote https://github.com/git/git refs/heads/master </dev/null | cut -c1-12)"
echo "private raw : $($L -mode one -insecure -proxy $E -tenant initech -repo https://gitlab.initech.internal/fast.git)"
echo "denied      : $($L -mode one -insecure -proxy $E -tenant acme -repo https://gitlab.initech.internal/fast.git)"
echo "hostile big : $($L -mode one -insecure -proxy $E -tenant initech -repo https://gitlab.initech.internal/big.git)"
echo "plain bench : $(docker exec t-client /b/lsrefs -mode bench -insecure -proxy $E2 -repo https://git.spike/fast.git -n 1 2>&1 | head -1 | cut -c1-40) (second Envoy not up yet)"
./b-envoy.sh plain; echo "bench plain : $(docker exec t-client /b/lsrefs -mode bench -insecure -proxy $E -repo https://git.spike/fast.git -n 800 -c 1)"
./b-envoy.sh tls;   echo "bench tls   : $($L -mode bench -insecure -proxy $E -repo https://git.spike/fast.git -n 800 -c 1)"
echo "burst tls   : $($L -mode bench -insecure -proxy $E -repo https://git.spike/fast.git -n 3000 -c 50)"
docker rm -f t-envoy2 >/dev/null 2>&1; docker run -d --name t-envoy2 --cpus 2 --network stls-pub --ip 198.19.0.42 -v $D/envoy/b-tls.json:/etc/envoy/envoy.yaml:ro -v $D/sds:/sds:ro -v stls-certs:/certs:ro envoyproxy/envoy:v1.40.2 -c /etc/envoy/envoy.yaml --concurrency 2 --log-level warn >/dev/null; /bin/sleep 3
RUN="$L -mode run -insecure -repo https://git.spike/fast.git -rps 25 -timeout 10s -retries 1 -dial 1s -proxy $E,$E2"
( /bin/sleep 5; docker stop -t0 t-envoy2 >/dev/null ) & echo "one stopped : $($RUN -dur 12s | cut -c1-120)"; wait; docker start t-envoy2 >/dev/null; /bin/sleep 3
( /bin/sleep 5; docker pause t-envoy2 >/dev/null ) & echo "one frozen  : $($RUN -dur 12s | cut -c1-120)"; wait; docker unpause t-envoy2 >/dev/null
./rotate.sh expired; /bin/sleep 2; echo "bad cert    : $($L -mode one -insecure -proxy $E -retries 1 -repo https://git.spike/fast.git | cut -c1-190)"; ./rotate.sh good; docker rm -f t-envoy2 >/dev/null 2>&1
