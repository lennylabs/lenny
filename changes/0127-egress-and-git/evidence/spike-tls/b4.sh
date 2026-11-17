#!/bin/bash
# certificate lifecycle and failure modes on the TLS listener
cd "$(dirname "$0")"; D=$PWD; P=198.19.0.41:3128; X="-proxy-tls -proxy-ca /c/ca1.pem"
who(){ docker exec t-client sh -c "echo | openssl s_client -connect $P -servername lenny-egress.lenny-system.svc -verify_hostname lenny-egress.lenny-system.svc -CAfile /c/$1 ${2:-} 2>&1 | grep -E 'subject=|Verification|Verify return code|alert|no peer' | head -2 | tr '\n' ' ' | cut -c1-120"; echo; }
one(){ docker exec t-client /b/tcl -proxy $P -target target.spike:7000 -cred agent:good -rate 20 -n 3 -hold 50ms -bucket 60s "$@" 2>&1 | tail -1 | cut -c1-100; }
./rotate.sh good; ./b-envoy.sh tls
echo "start:              $(who ca1.pem)"
echo "### rotation with 200 tunnels open"
docker exec t-client /b/soak -proxy $P -target target.spike:7000 -cred agent:good -n 200 -rate 100 -max 30s -report 30s $X > /tmp/s.out 2>&1 &
/bin/sleep 6; ./rotate.sh rotated; /bin/sleep 2
echo "after rotate:       $(who ca1.pem)"
echo "new CONNECT:        $(one $X)"
curl -s http://127.0.0.1:19941/stats | grep -E 'sds.server.(update_success|update_failure|update_rejected)|ssl.connection_error' | cut -c1-80 | tr '\n' ' '; echo
wait; tail -2 /tmp/s.out | cut -c1-150
echo "### invalid material keeps the last good certificate?"
docker run --rm -v stls-certs:/certs alpine:3.22 sh -c 'mkdir -p /certs/live/..bad; echo garbage > /certs/live/..bad/tls.crt; echo garbage > /certs/live/..bad/tls.key; ln -s ..bad /certs/live/..data_tmp; mv -T /certs/live/..data_tmp /certs/live/..data'; /bin/sleep 2
echo "garbage installed:  $(who ca1.pem)"
echo "new CONNECT:        $(one $X)"
curl -s http://127.0.0.1:19941/stats | grep -E 'sds.server.(update_success|update_failure|update_rejected)' | cut -c1-70 | tr '\n' ' '; echo
./rotate.sh good; /bin/sleep 2; echo "repaired:           $(who ca1.pem)"
echo "### client refusals, all must fail closed"
./rotate.sh expired; /bin/sleep 2; echo "expired cert:       $(who ca1.pem) | $(one $X)"
./rotate.sh badsan;  /bin/sleep 2; echo "wrong SAN:          $(who ca1.pem) | $(one $X)"
./rotate.sh ca2leaf; /bin/sleep 2; echo "untrusted CA:       $(who ca1.pem) | $(one $X)"
docker exec t-client sh -c 'cat /c/ca1.pem /c/ca2.pem > /tmp/both.pem'
echo "CA rotation bundle: $(docker exec t-client sh -c "echo | openssl s_client -connect $P -servername lenny-egress.lenny-system.svc -verify_hostname lenny-egress.lenny-system.svc -CAfile /tmp/both.pem 2>&1 | grep -E 'Verification' | head -1")"
./rotate.sh good; /bin/sleep 2
echo "TLS 1.1:            $(who ca1.pem -tls1_1)"
echo "TLS 1.2:            $(who ca1.pem -tls1_2)"
echo "plaintext client:   $(one)"
echo "RSA certificate:    $(./rotate.sh rsa; /bin/sleep 2; who ca1.pem) | $(one $X)"
./rotate.sh good; /bin/sleep 2
echo "### cold start with no certificate installed"
docker rm -f t-envoy >/dev/null 2>&1; docker volume create stls-empty >/dev/null
docker run -d --name t-envoy --cpus 2 --network stls-pub --ip 198.19.0.41 -p 127.0.0.1:19941:9901 -v $D/envoy/b-tls.json:/etc/envoy/envoy.yaml:ro -v $D/sds:/sds:ro -v stls-empty:/certs envoyproxy/envoy:v1.40.2 -c /etc/envoy/envoy.yaml --concurrency 2 --log-level warn >/dev/null
/bin/sleep 3; echo "state: $(docker inspect -f '{{.State.Status}}' t-envoy), ready=$(curl -s -m2 http://127.0.0.1:19941/ready | head -1)"; docker logs t-envoy 2>&1 | grep -E 'critical|error' | tail -2 | cut -c1-200
docker rm -f t-envoy >/dev/null 2>&1; docker volume rm stls-empty >/dev/null 2>&1
