#!/bin/bash
# Item 3 part 2: gateway client -> mirror stand-in -> real Envoy (raw tunnel) -> fault front -> real Gitea.
D=$(cd "$(dirname "$0")" && pwd); O=$D/..; S=$O/..; . $D/tokens.env
G=gitea.internal.oa.test
python3 $S/spike-lsrefs/gen5.py $D/envoy/final.json authz=198.19.50.11:9000
python3 $S/spike-lsrefs/gen5.py $D/envoy/badauthz.json authz=198.19.50.99:9000 cto=0.1s
docker rm -f oa-authz oa-envoy oa-mirror oa-fproxy >/dev/null 2>&1; docker volume rm oa-mirdata >/dev/null 2>&1
docker run -d --name oa-authz --network oa-net --ip 198.19.50.11 --cpus 0.5 -m 128m -e NAME=authz1 -e PIN=172.30.50.23:443 -v $S/spike-lsrefs/bin:/b:ro alpine:3.22 /b/authz >/dev/null
envoy(){ docker rm -f oa-envoy >/dev/null 2>&1; docker create --name oa-envoy --network oa-net --ip 198.19.50.41 --cpus 2 -m 256m -v $D/envoy/$1:/etc/envoy/envoy.yaml:ro envoyproxy/envoy:v1.40.2 -c /etc/envoy/envoy.yaml --concurrency 2 --log-level warn >/dev/null; docker network connect --ip 172.30.50.41 oa-priv oa-envoy; docker start oa-envoy >/dev/null; for i in $(seq 1 40); do docker exec oa-cl1 curl -sf http://198.19.50.41:9901/ready >/dev/null 2>&1 && break; /bin/sleep 0.5; done; }
docker exec oa-cl1 sh -c 'grep -q lenny-git-mirror /etc/hosts || echo "198.19.50.60 lenny-git-mirror.lenny-system.svc" >> /etc/hosts'
envoy final.json
docker create --name oa-fproxy --network oa-net --ip 198.19.50.42 --cpus 0.5 -m 64m -v $O/bin:/b:ro alpine:3.22 /b/fproxy >/dev/null; docker network connect --ip 172.30.50.42 oa-priv oa-fproxy; docker start oa-fproxy >/dev/null
docker volume create oa-mirdata >/dev/null
docker run -d --name oa-mirror --network oa-net --ip 198.19.50.60 --cpus 1 -m 256m -v oa-mirdata:/data -v $O/bin:/b:ro -v $O/certs:/c:ro -e GIT_CONFIG_COUNT=1 -e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0='*' oa-git /b/mirls -proxy http://198.19.50.41:3128 -i3 http://198.19.50.11:9002/count -cert /c/mirror/tls.crt -key /c/mirror/tls.key -store "priv=https://$G/alice/priv.git,cred=alice:$A" -store "pub=https://$G/alice/pub.git" >/dev/null
/bin/sleep 1.5
F(){ docker exec oa-cl2 curl -sS -o /dev/null "http://172.30.50.23:9100/set?mode=$1"; }
FP(){ docker exec oa-cl1 curl -sS -o /dev/null "http://198.19.50.42:9101/set?$1"; }
M(){ docker exec oa-cl1 curl -sS -o /dev/null "http://198.19.50.60:9200/set?$1"; }
ST(){ docker exec oa-cl1 curl -sS "http://198.19.50.60:9200/state?store=$1"; }
waitm(){ for i in $(seq 1 30); do docker exec oa-cl1 curl -s -o /dev/null -m 1 "http://198.19.50.60:9200/state?store=priv" && return; /bin/sleep 0.3; done; }
R(){ docker exec oa-cl1 /b/lsrefs -mode one -no-v0 -ca /c/ca.pem -repo https://lenny-git-mirror.lenny-system.svc:8443/s/$1 -ref $2; }
P=0; X=0
t(){ # t LABEL STORE REF want-class want-from-store(true|-) [cause-substring] [sha]
  l=$1; out=$(R $2 $3); c=$(jq -r .class <<<"$out"); code=$(jq -r .code <<<"$out"); reason=$(jq -r .reason <<<"$out"); fs=$(jq -r '.["X-Lenny-Resolved-From-Store"]//"-"' <<<"$out"); cause=$(jq -r '.["X-Lenny-Cause"]//""' <<<"$out"); sha=$(jq -r .sha <<<"$out"); ms=$(jq -r .ms <<<"$out"); hs=$(jq -r .host_status <<<"$out"); at=$(jq -r '.["X-Lenny-Confirmed-At"]//""' <<<"$out")
  v=PASS; [ "$c" = "$4" ] || v=FAIL; [ "$fs" = "$5" ] || v=FAIL; [ -z "$6" ] || [[ "$cause" == *"$6"* ]] || v=FAIL; [ -z "$7" ] || [ "$sha" = "$7" ] || v=FAIL
  [ $v = PASS ] && P=$((P+1)) || X=$((X+1))
  printf '%s %-52s class=%s code=%s reason=%s fromStore=%s status=%s sha=%s ms=%s cause=[%s] at=%s\n' $v "$l" "$c" "${code#GIT_CLONE_REF_}" "$reason" "$fs" "$hs" "${sha:0:8}" "$ms" "$cause" "$at"
}
lsr(){ docker exec -e GIT_SSL_CAINFO=/c/ca.pem oa-cl2 git ls-remote https://alice:$A@$G/alice/$1.git "$2" | cut -f1; }
MAIN=$(lsr priv refs/heads/main); PUBM=$(lsr pub refs/heads/main)
M coalesce=0s
docker exec -e GIT_SSL_CAINFO=/c/ca.pem oa-cl2 sh -c "cd /tmp/wpriv && git reset -q --hard v1 && git push -q -f origin main main:refs/heads/dev 2>&1 | grep -v remote:"
MAIN=$(lsr priv refs/heads/main)
echo "=== S1 baseline through Envoy, a store miss schedules a fetch"
t "priv main, upstream answer" priv main ok - "" $MAIN
for i in $(seq 1 40); do docker exec oa-mirror sh -c "git --git-dir /data/stores/priv.git cat-file -e $MAIN" >/dev/null 2>&1 && { echo "scheduled fetch landed within $((i*250)) ms poll"; break; }; /bin/sleep 0.25; done
echo "=== S2 host failures inside the tunnel: listed causes answer from the store"
for s in 502 503 504; do F status:$s; t "host $s" priv main ok true "host $s" $MAIN; done
F reset; t "connection reset after TLS" priv main ok true "connection or TLS" $MAIN
F hang; t "host accepts and hangs (8 s limit)" priv main ok true "timeout" $MAIN
echo "=== S3 real outages"
F pass; docker stop -t0 oa-gitea >/dev/null; /bin/sleep 1; t "Gitea stopped behind the front (its dial hangs)" priv main ok true "timeout" $MAIN; docker start oa-gitea >/dev/null
for i in $(seq 1 60); do docker exec oa-cl2 curl -s --cacert /c/ca.pem -o /dev/null https://$G/api/healthz && break; /bin/sleep 1; done
docker stop -t0 oa-fault >/dev/null; /bin/sleep 1; t "origin address down: Envoy CONNECT 503" priv main ok true "CONNECT 503" $MAIN; docker start oa-fault >/dev/null; /bin/sleep 1
docker stop -t0 oa-envoy >/dev/null; /bin/sleep 1; t "Envoy stopped: no reply from lenny-egress" priv main ok true "no reply" $MAIN; docker start oa-envoy >/dev/null; for i in $(seq 1 40); do docker exec oa-cl1 curl -sf http://198.19.50.41:9901/ready >/dev/null 2>&1 && break; /bin/sleep 0.5; done
M proxy=http://198.19.50.42:3128
FP "mode=pass&dial=172.30.50.23:8443"; t "certificate for another name: TLS failure" priv main ok true "connection or TLS" $MAIN
FP "dial=172.30.50.23:443"
FP mode=blackhole; t "proxy accepts and never replies" priv main ok true "no reply" $MAIN
FP mode=status:503; t "CONNECT 503 (fake proxy)" priv main ok true "CONNECT 503" $MAIN
echo "=== S4 answers that are passed on, and keep confirmations"
for s in 403 407 502 429; do FP mode=status:$s; c=transient; [ $s -lt 410 ] && c=unresolvable; t "CONNECT $s" priv main $c - "CONNECT $s"; done
FP mode=status:503; t "confirmations survive the CONNECT refusals above" priv main ok true "" $MAIN
M proxy=http://198.19.50.41:3128; FP mode=pass
F status:429; t "host 429 passed on" priv main transient - ""
F status:500; t "host 500 passed on (not a listed cause)" priv main transient - ""
F status:503; t "confirmations survive 429 and 500" priv main ok true "" $MAIN
echo "=== S5 host 401, 403, 404 stop the fallback until the next success"
for s in 401 403 404; do
  F status:$s; t "host $s passed on" priv main unresolvable - ""; echo "   state: $(ST priv | jq -c .state)"
  F status:503; t "then 503: transient, not the store" priv main transient - "fallback stopped"
  F pass; t "next success" priv main ok - "" $MAIN
  F status:503; t "then 503: the store again" priv main ok true "" $MAIN
done
F pass
echo "=== S6 mint"
M mint=unavailable; t "mint unavailable: listed cause" priv main ok true "mint unavailable" $MAIN
M mint=refused; t "mint refused: passed on" priv main unresolvable - "mint refused"; M mint=ok; F status:503; t "then 503: transient" priv main transient - "fallback stopped"; F pass; t "next success" priv main ok - "" $MAIN
echo "=== S7 a real repository made private, on the anonymous public store"
t "pub main, anonymous" pub main ok - "" $PUBM; docker exec oa-cl1 curl -sS -o /dev/null "http://198.19.50.60:9200/fetch?store=pub"
F status:503; t "pub from the store" pub main ok true "" $PUBM; F pass
api(){ docker exec oa-cl2 curl -sS --cacert /c/ca.pem -H "Authorization: token $A" -H 'Content-Type: application/json' "$@"; }
api -X PATCH https://$G/api/v1/repos/alice/pub -d '{"private":true}' | jq -c '{private}'
t "repository now private: real 401" pub main unresolvable - ""; F status:503; t "then 503: no store answer for the old public copy" pub main transient - "fallback stopped"; F pass
api -X PATCH https://$G/api/v1/repos/alice/pub -d '{"private":false}' | jq -c '{private}'
t "public again" pub main ok - "" $PUBM
echo "=== S8 a real bad token and another user's token on the tenant store"
M "store=priv&cred=alice:badbadbad"; t "invalid token: real 401" priv main unresolvable - ""; echo "   state: $(ST priv | jq -c .state)"
M "store=priv&cred=alice:$A"; t "restored" priv main ok - "" $MAIN
M "store=priv&cred=bob:$B"; t "other user's token: real 404" priv main unresolvable - ""; echo "   state: $(ST priv | jq -c .state)"
M "store=priv&cred=alice:$A"; t "restored" priv main ok - "" $MAIN
echo "=== S9 a deleted ref clears its pair, and only that pair"
t "dev resolves" priv dev ok - "" ; DEV=$(lsr priv refs/heads/dev); docker exec oa-cl1 curl -sS -o /dev/null "http://198.19.50.60:9200/fetch?store=priv"; t "v1 resolves peeled" priv v1 ok - "" $MAIN
echo "   pairs: $(ST priv | jq -c '.state.pairs|keys')"
docker exec -e GIT_SSL_CAINFO=/c/ca.pem oa-cl2 sh -c "cd /tmp/wpriv && git push -q origin :refs/heads/dev 2>&1 | grep -v remote:"
t "dev deleted upstream: absent" priv dev unresolvable - ""
echo "   pairs: $(ST priv | jq -c '.state.pairs|keys')"
F status:503; t "503: deleted dev does not resolve" priv dev transient - "no confirmation"; t "503: main still does" priv main ok true "" $MAIN; t "503: tag still does" priv v1 ok true "" $MAIN; F pass
echo "--- the literal reading: every pair the answer omits is cleared, though ref-prefix narrowed the answer"
M naive=1; t "main resolves, naive clear" priv main ok - "" $MAIN; echo "   pairs: $(ST priv | jq -c '.state.pairs|keys')"; F status:503; t "503: tag v1 lost its pair under the literal reading" priv v1 transient - "no confirmation"; F pass; M naive=0; docker exec oa-cl1 curl -sS -o /dev/null "http://198.19.50.60:9200/fetch?store=priv"
echo "=== S10 force-push: the store lacks the new commit"
M nofetch=1; t "main at A" priv main ok - "" $MAIN
docker exec -e GIT_SSL_CAINFO=/c/ca.pem oa-cl2 sh -c "cd /tmp/wpriv && git commit -q --amend -m forced && git push -q -f origin main 2>&1 | grep -v remote:"; NEW=$(lsr priv refs/heads/main); echo "A=${MAIN:0:8} B=${NEW:0:8}"
t "ls-refs sees B" priv main ok - "" $NEW
F status:503; t "503: transient, never A" priv main transient - "store lacks"; F pass
M nofetch=0; t "next call schedules the fetch" priv main ok - "" $NEW
for i in $(seq 1 40); do docker exec oa-mirror sh -c "git --git-dir /data/stores/priv.git cat-file -e $NEW" >/dev/null 2>&1 && break; /bin/sleep 0.25; done
F status:503; t "503: B from the store" priv main ok true "" $NEW; F pass
echo "=== S11 age limit (scaled to 2 s)"
M maxage=2s; t "confirm" priv main ok - "" $NEW; /bin/sleep 3; F status:503; t "503 past the age limit: transient" priv main transient - "too old"; F pass; M maxage=6h
echo "=== S12 the confirmation index survives a restart"
t "confirm" priv main ok - "" $NEW; F status:503; a=$(R priv main | jq -r '.["X-Lenny-Confirmed-At"]'); docker restart -t1 oa-mirror >/dev/null; waitm; M coalesce=0s; b=$(R priv main | jq -r '.["X-Lenny-Confirmed-At"]'); echo "confirmedAt before=$a after restart=$b"; t "503 after restart" priv main ok true "" $NEW; F pass
echo "=== S13 coalescing: I3 on every request, one upstream call"
M coalesce=5s; s0=$(ST priv); for i in $(seq 1 20); do R priv main >/dev/null; done; s1=$(ST priv); echo "20 sequential: upstream +$(( $(jq .upstream <<<"$s1") - $(jq .upstream <<<"$s0") )), coalesced +$(( $(jq .coalesced <<<"$s1") - $(jq .coalesced <<<"$s0") )), I3 +$(( $(jq .i3 <<<"$s1") - $(jq .i3 <<<"$s0") ))"
/bin/sleep 5.2; s0=$(ST priv); F hang; for i in 1 2 3 4 5 6 7 8; do (R priv main | jq -c '{class,from:.["X-Lenny-Resolved-From-Store"],ms}') & done; wait; s1=$(ST priv); echo "8 concurrent during a hang: upstream +$(( $(jq .upstream <<<"$s1") - $(jq .upstream <<<"$s0") ))"; F pass
/bin/sleep 5.2; t "fresh success" priv main ok - "" $NEW; api -X PATCH https://$G/api/v1/repos/alice/pub -d '{"private":true}' >/dev/null
R pub main >/dev/null; /bin/sleep 5.2; R pub main >/dev/null
M "store=pub&cred=alice:badbadbad"; /bin/sleep 5.2; t "pub token bad: real 401 (setup)" pub main unresolvable - ""; M "store=pub&cred=alice:$A"; /bin/sleep 5.2
t "pub success with a good token" pub main ok - "" $PUBM; M "store=pub&cred=alice:badbadbad"
for i in 1 2 3 4 5 6 7; do printf 't+%ss after the credential broke: ' $i; R pub main | jq -c '{class,host_status,coalesced:.["X-Lenny-Coalesced"]}'; /bin/sleep 1; done
M "store=pub&cred="; api -X PATCH https://$G/api/v1/repos/alice/pub -d '{"private":false}' >/dev/null; M coalesce=0s
echo "=== S14 decision service"
t "confirm" priv main ok - "" $NEW
docker stop -t0 oa-authz >/dev/null; /bin/sleep 1; t "decision service down: mirror's I3 fails, no store" priv main transient - "decision service"; docker start oa-authz >/dev/null; /bin/sleep 2
envoy badauthz.json; t "mirror's I3 fine, Envoy cannot reach the decision service" priv main ok true "CONNECT 503" $NEW; envoy final.json
echo "=== S15 a host that speaks only v0, behind the mirror"
F v0only; t "v0-only host, mirror falls back" priv main ok - "" $NEW; t "v0-only host, tag peeled" priv v1 ok - "" $MAIN; t "v0-only host, absent ref" priv nosuch unresolvable - ""; F status:503; t "503: confirmation from a v0 answer" priv main ok true "" $NEW; F pass
echo "=== summary PASS=$P FAIL=$X"
