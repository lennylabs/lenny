#!/bin/bash
. "$(dirname "$0")/lib.sh"
PERIOD=30; WIN=15; SYNC=3
docker exec oa-hc sh -c 'true'
sq(){ docker exec oa-hc curl -s -o /dev/null -k --max-time 7 --proxy https://lenny-egress.lenny-system.svc:3128 --proxy-cacert /c/ca.pem --proxy-user "$1" -w '%{http_connect}' "$2"; true; }
echo "=== A. reverse lookup in dstdomain: a literal whose PTR falls in a listed wildcard. 198.19.50.66 is not on any list."
docker rm -f oa-tgt6 >/dev/null 2>&1; docker run -d --name oa-tgt6 --network oa-net --ip 198.19.50.66 --cpus 0.5 -m 64m -v $O/bin:/b:ro -v $O/certs:/c:ro alpine:3.22 /b/otgt /c/target/tls.crt /c/target/tls.key >/dev/null
infra 30; /bin/sleep 1
for v in correct correct-n; do squid $v 2; echo "$v: probe-addr -> literal 198.19.50.66 = $(sq probe-addr.g1:probe-addr-pw-1 https://198.19.50.66/)  target connections $(docker exec oa-hc curl -s http://198.19.50.66:9300/count)"; done
echo "=== B. IPv4-mapped and NAT64 literals, with every authenticated CONNECT allowed so only the address rule can refuse"
for v in openlist-norule openlist; do squid $v 2; for t in '[::ffff:127.0.0.1]' '[::ffff:169.254.169.254]' '[::ffff:172.30.50.20]' '[64:ff9b::7f00:1]' '127.1' '2130706433' '0x7f.1'; do echo "$v  $t = $(sq probe-a.g2:probe-a-pw-2 https://$t/)"; done; done
echo "=== C. a proxy that answers each status and carries no traffic"
docker rm -f oa-squid >/dev/null 2>&1; docker run -d --name oa-squid --network oa-net --ip 198.19.50.50 --cpus 0.5 -m 64m -v $O/bin:/b:ro -v $O/certs:/c:ro alpine:3.22 /b/tlsfake /c/proxy/tls.crt /c/proxy/tls.key http://198.19.50.51:8080/probe >/dev/null; /bin/sleep 1
echo "status only:"; probe -window 5s -status-only | jq -c 'select(.verdict or .pass==false)'
echo "with a handshake through the tunnel:"; probe -window 5s | jq -c 'select(.verdict or .pass==false)'
docker rm -f oa-squid >/dev/null
echo "=== D. the retired generation's status: cached versus never used"
infra $PERIOD; squid correct-n $SYNC
for i in $(seq 1 3); do probe -window ${WIN}s >/dev/null; /bin/sleep 1; done
echo "never cached, not in the document: $(sq probe-a.g1:probe-a-pw-1 https://public.oa.test/)"
echo "=== E. a healthy proxy, probed every 2 s for 100 s: both canary rules, apply window ${WIN}s, sync every ${SYNC}s, rotation every ${PERIOD}s"
for i in $(seq 1 50); do s=$(probe -window ${WIN}s | jq -r 'select(.verdict)|.verdict'); l=$(probe -canary-rule literal | jq -r 'select(.verdict)|.verdict'); printf '%s/%s ' $s $l; /bin/sleep 2; done; echo
docker exec oa-hc curl -s http://198.19.50.51:8080/probe | jq -c '[.canary[]|{n,at}]'
docker exec oa-squid tail -3 /tmp/sync.log
echo "=== F. stopped sync job, probed every 2 s with the settled rule"
c=$(docker exec oa-hc curl -s http://198.19.50.51:8080/probe); gen=$(jq -r '.canary[-1].n' <<<"$c"); echo "current rotation $gen"
docker exec oa-squid sh /l1/killsync.sh; T0=$(python3 -c 'import time;print(time.time())')
first=""
for i in $(seq 1 45); do r=$(probe -window ${WIN}s); v=$(jq -r 'select(.verdict)|.verdict' <<<"$r"); t=$(python3 -c "import time;print(round(time.time()-$T0,1))"); printf 't=%ss %s %s\n' $t $v "$(jq -r 'select(.pass==false)|.check' <<<"$r" | tr '\n' ' ' | cut -c1-120)"; if [ $v = FAIL ] && [ -z "$first" ]; then first=$t; fi; [ -n "$first" ] && [ $i -gt 25 ] && break; /bin/sleep 2; done
echo "first FAIL at t=$first s after the sync job stopped"
echo "while stale: statuses of the newest rotation's generation and host, and the retired generation"
c=$(docker exec oa-hc curl -s http://198.19.50.51:8080/probe); jq -c '.canary[-1]|{n,user,host,retired:.retired.user}' <<<"$c"
u=$(jq -r '.canary[-1].user' <<<"$c"); pw=$(jq -r '.canary[-1].password' <<<"$c"); h=$(jq -r '.canary[-1].host' <<<"$c"); ru=$(jq -r '.canary[-1].retired.user' <<<"$c"); rp=$(jq -r '.canary[-1].retired.password' <<<"$c")
echo "new generation -> new host: $(sq $u:$pw https://${h%:443}/)  retired generation -> its old host: $(sq $ru:$rp https://public.oa.test/)"
docker exec oa-squid tail -2 /tmp/sync.log
echo "=== G. frozen canary source: nothing to detect until the freshness assertion fires"
infra 10; squid correct-n 2; /bin/sleep 12; probe -window 5s | jq -c 'select(.verdict)'; docker exec oa-hc curl -s http://198.19.50.51:8080/freeze; echo; docker exec oa-squid sh /l1/killsync.sh
T0=$(python3 -c 'import time;print(time.time())')
for i in $(seq 1 14); do r=$(probe -window 5s); v=$(jq -r 'select(.verdict)|.verdict' <<<"$r"); printf 't=%ss %s %s\n' "$(python3 -c "import time;print(round(time.time()-$T0,1))")" $v "$(jq -r 'select(.pass==false)|.check' <<<"$r" | tr '\n' ' ' | cut -c1-80)"; /bin/sleep 2; done
echo "=== H. netagent's check in front of Squid over TLS, Squid correct"
infra 30; squid correct-n 2
docker rm -f oa-agent oa-agentn >/dev/null 2>&1
docker run -d --name oa-agent --network oa-net --ip 198.19.50.70 --cpus 1 -m 128m -v $O/bin:/b:ro -v $O/certs:/c:ro alpine:3.22 /b/sniagent -proxy 198.19.50.50:3128 -proxy-ca /c/ca.pem -cred p1-acme.g1:acme-pw-1 >/dev/null
docker run -d --name oa-agentn --network oa-net --ip 198.19.50.71 --cpus 1 -m 128m -v $O/bin:/b:ro -v $O/certs:/c:ro alpine:3.22 /b/sniagent -check=false -proxy 198.19.50.50:3128 -proxy-ca /c/ca.pem -cred p1-acme.g1:acme-pw-1 >/dev/null
/bin/sleep 1
H(){ printf '%-34s ' "$1"; shift; docker exec oa-hc /b/hc "$@" 2>&1 | cut -c1-110; }
H "check, match" -proxy 198.19.50.70:3128 -sni target.oa.test
H "check, mismatch" -proxy 198.19.50.70:3128 -sni other.oa.test
H "no check, mismatch (stock Squid)" -proxy 198.19.50.71:3128 -sni other.oa.test
H "check, TLS 1.2 match" -proxy 198.19.50.70:3128 -sni target.oa.test -tls 12
H "check, unlisted host" -proxy 198.19.50.70:3128 -authority other.oa.test:443 -sni other.oa.test
for r in 1 2 3 4 5; do printf 'through Squid check    '; docker exec oa-hc /b/hc -proxy 198.19.50.70:3128 -bench 500 -sni target.oa.test; printf 'through Squid nocheck  '; docker exec oa-hc /b/hc -proxy 198.19.50.71:3128 -bench 500 -sni target.oa.test; done
docker exec oa-squid tail -4 /tmp/access.log | cut -c1-140
