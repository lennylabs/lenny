#!/bin/bash
# Item 4: the ClientHello check, in a stand-in for netagent's egress endpoint.
D=$(cd "$(dirname "$0")" && pwd); O=$D/..; S=$O/..
docker rm -f oa-agent oa-agentn oa-tgt4 oa-hc >/dev/null 2>&1
A="--network-alias target.oa.test --network-alias other.oa.test"
docker run -d --name oa-tgt4 --network oa-net --ip 198.19.50.20 $A --cpus 1 -m 128m -v $O/bin:/b:ro -v $O/certs:/c:ro alpine:3.22 /b/otgt /c/target/tls.crt /c/target/tls.key >/dev/null
docker run -d --name oa-agent --network oa-net --ip 198.19.50.70 --cpus 1 -m 128m -v $O/bin:/b:ro alpine:3.22 /b/sniagent >/dev/null
docker run -d --name oa-agentn --network oa-net --ip 198.19.50.71 --cpus 1 -m 128m -v $O/bin:/b:ro alpine:3.22 /b/sniagent -check=false >/dev/null
docker run -d --name oa-hc --network oa-net --ip 198.19.50.31 --cpus 2 -m 256m -v $O/bin:/b:ro -v $O/certs:/c:ro oa-git sleep infinity >/dev/null
/bin/sleep 1
hc(){ docker exec oa-hc /b/hc "$@"; }
log(){ docker logs --tail 1 oa-agent 2>&1 | jq -c '{result,reason,sni,hello_bytes,reads,records,check_us,parse_us}'; }
c(){ l=$1; shift; printf '%-44s check: ' "$l"; hc -proxy 198.19.50.70:3128 "$@" 2>&1 | cut -c1-120; printf '%-44s agent: ' ""; log; printf '%-44s no check: ' ""; hc -proxy 198.19.50.71:3128 "$@" 2>&1 | cut -c1-120; }
echo "=== cases: the same client with the check and without it"
c "match, TLS 1.3" -sni target.oa.test -tls 13
c "match, TLS 1.2" -sni target.oa.test -tls 12
c "mismatch, TLS 1.3" -sni other.oa.test -tls 13
c "mismatch, TLS 1.2" -sni other.oa.test -tls 12
c "no SNI" -sni ""
c "non-TLS first byte (plain HTTP)" -case nontls
c "SSLv2-format hello" -case v2-hello
c "split: first flight in 1-byte TCP segments" -sni target.oa.test -split-tcp 1 -gap 1ms
c "split: 5-byte TCP segments, 20 ms apart" -sni target.oa.test -split-tcp 5 -gap 20ms
c "split: mismatch, 40-byte TCP segments" -sni other.oa.test -split-tcp 40 -gap 5ms
c "split: hello across TLS records (64 bytes)" -sni target.oa.test -split-rec 64
c "split: mismatch across TLS records (64)" -sni other.oa.test -split-rec 64
c "split: records of 1 byte" -sni target.oa.test -split-rec 1
c "client silent after the 200" -case silent
c "3 bytes then a stall" -case stall
c "two server_name extensions" -case dup-ext
c "two names in the list" -case two-names
c "record length 65535" -case big-record
c "hello declares 1 MiB across records" -case huge-hello
c "authority is an IP literal, no SNI" -authority 198.19.50.20:443 -sni ""
c "authority is an IP literal, SNI names a host" -authority 198.19.50.20:443 -sni target.oa.test
c "authority upper case" -authority TARGET.OA.TEST:443 -sni target.oa.test
c "authority with trailing dot" -authority target.oa.test.:443 -sni target.oa.test
echo "=== first-record-only variant: the hello split across TLS records"
docker run -d --name oa-agentf --network oa-net --ip 198.19.50.72 --cpus 1 -m 128m -v $O/bin:/b:ro alpine:3.22 /b/sniagent -first-record-only >/dev/null; /bin/sleep 0.5
printf 'match, records of 64 bytes: '; hc -proxy 198.19.50.72:3128 -sni target.oa.test -split-rec 64 2>&1 | cut -c1-120; docker logs --tail 1 oa-agentf 2>&1 | jq -c '{result,reason}'
printf 'mismatch, records of 64 bytes: '; hc -proxy 198.19.50.72:3128 -sni other.oa.test -split-rec 64 2>&1 | cut -c1-120; docker logs --tail 1 oa-agentf 2>&1 | jq -c '{result,reason}'
printf 'match, unsplit: '; hc -proxy 198.19.50.72:3128 -sni target.oa.test 2>&1 | cut -c1-120; docker logs --tail 1 oa-agentf 2>&1 | jq -c '{result,reason,hello_bytes,reads}'
docker rm -f oa-agentf >/dev/null
echo "=== real clients through the check, TLS 1.2 and 1.3, with the hello they actually send"
R(){ l=$1; shift; printf '%-40s ' "$l"; "$@" 2>&1 | tail -1 | cut -c1-110; printf '%-40s agent: ' ""; log; }
PX=http://198.19.50.70:3128; U=https://target.oa.test/
R "curl OpenSSL default" docker exec oa-hc curl -sS --max-time 8 --cacert /c/ca.pem -x $PX -o /dev/null -w 'http=%{http_code} tls=%{ssl_verify_result}\n' $U
R "curl --tls-max 1.2" docker exec oa-hc curl -sS --max-time 8 --tls-max 1.2 --cacert /c/ca.pem -x $PX -o /dev/null -w 'http=%{http_code}\n' $U
R "curl --tlsv1.3" docker exec oa-hc curl -sS --max-time 8 --tlsv1.3 --cacert /c/ca.pem -x $PX -o /dev/null -w 'http=%{http_code}\n' $U
R "curl uppercase URL host" docker exec oa-hc curl -sS --max-time 8 --cacert /c/ca.pem -x $PX -o /dev/null -w 'http=%{http_code}\n' https://TARGET.OA.TEST/
R "curl trailing dot URL host" docker exec oa-hc curl -sS --max-time 8 --cacert /c/ca.pem -x $PX -o /dev/null -w 'http=%{http_code}\n' https://target.oa.test./
R "curl --connect-to mismatch" docker exec oa-hc sh -c "curl -sS --max-time 8 --cacert /c/ca.pem -x $PX --connect-to other.oa.test:443:target.oa.test:443 -o /dev/null -w 'http=%{http_code}\n' https://other.oa.test/"
R "curl by IP, no SNI" docker exec oa-hc curl -sS -k --max-time 8 -x $PX -o /dev/null -w 'http=%{http_code}\n' https://198.19.50.20/
R "git OpenSSL ls-remote (expects no repo)" docker exec -e GIT_SSL_CAINFO=/c/ca.pem -e https_proxy=$PX oa-hc git ls-remote $U/x.git
docker run --rm --name oa-c-ossl --network oa-net -v $O/certs:/c:ro oa-git sh -c "for v in -tls1_2 -tls1_3; do echo | openssl s_client -proxy 198.19.50.70:3128 -connect target.oa.test:443 -servername target.oa.test -CAfile /c/ca.pem \$v 2>&1 | grep -E 'Protocol *:|New, ' | head -1 | cut -c1-80; done; echo | openssl s_client -proxy 198.19.50.70:3128 -connect target.oa.test:443 -servername other.oa.test -CAfile /c/ca.pem 2>&1 | grep -E 'CONNECTED|errno|write:|no peer|New, ' | head -3 | cut -c1-100"
docker logs --tail 3 oa-agent 2>&1 | jq -c '{result,reason,sni,hello_bytes,reads}'
docker run --rm --name oa-c-py4 --network oa-net -v $O/certs:/c:ro -e HTTPS_PROXY=$PX oa-py python3 -c "
import requests,httpx,ssl
print('requests',requests.get('$U',verify='/c/ca.pem',timeout=8).status_code)
print('httpx',httpx.get('$U',verify=ssl.create_default_context(cafile='/c/ca.pem'),timeout=8).status_code)
" 2>&1 | tail -2; docker logs --tail 2 oa-agent 2>&1 | jq -c '{result,sni,hello_bytes,reads}'
docker run --rm --name oa-c-node4 --network oa-net -v $O/certs:/c:ro -e HTTPS_PROXY=$PX -e NODE_USE_ENV_PROXY=1 -e NODE_EXTRA_CA_CERTS=/c/ca.pem node:24-slim node -e "fetch('$U').then(r=>console.log('node fetch',r.status)).catch(e=>console.log('node fail',e.cause||e))" 2>&1 | tail -1 | cut -c1-120; docker logs --tail 1 oa-agent 2>&1 | jq -c '{result,sni,hello_bytes,reads}'
docker run --rm --name oa-c-java4 --network oa-net -v $O/certs:/c:ro -v $O/item1/clients:/j:ro eclipse-temurin:21-jdk bash -c "cd /tmp; keytool -importcert -noprompt -alias a -file /c/ca.pem -keystore t.p12 -storetype PKCS12 -storepass changeit >/dev/null 2>&1; cp /j/Nc.java .; javac Nc.java; java -Dhttps.proxyHost=198.19.50.70 -Dhttps.proxyPort=3128 -Djavax.net.ssl.trustStore=t.p12 -Djavax.net.ssl.trustStorePassword=changeit -cp . Nc $U" 2>&1 | tail -1 | cut -c1-120; docker logs --tail 1 oa-agent 2>&1 | jq -c '{result,sni,hello_bytes,reads}'
echo "=== added latency: CONNECT plus TLS handshake, same client, check and no check alternating, 5 x 1000 each, TLS 1.3 and 1.2"
for v in 13 12; do for r in 1 2 3 4 5; do
  printf 'tls%s check    ' $v; hc -proxy 198.19.50.70:3128 -bench 1000 -tls $v -sni target.oa.test
  printf 'tls%s nocheck  ' $v; hc -proxy 198.19.50.71:3128 -bench 1000 -tls $v -sni target.oa.test
done; done
echo "=== the check's own time, from the agent's log (all passing tunnels so far)"
docker logs oa-agent 2>&1 | jq -c 'select(.result=="pass") | [.check_us,.parse_us,.reads,.hello_bytes]' | python3 -c "
import sys,json
r=[json.loads(l) for l in sys.stdin]
c=sorted(x[0] for x in r if x[2]==1); p=sorted(x[1] for x in r)
print('tunnels',len(r),'single-read tunnels',len(c),'read+parse us p50',c[len(c)//2],'p99',c[int(len(c)*.99)],'parser alone us p50',p[len(p)//2],'p99',p[int(len(p)*.99)])
print('hello bytes seen',sorted(set(x[3] for x in r))[:12],'reads seen',sorted(set(x[2] for x in r))[:8])"
echo "=== memory after the run"; docker stats --no-stream --format '{{.Name}} {{.MemUsage}}' oa-agent oa-agentn
