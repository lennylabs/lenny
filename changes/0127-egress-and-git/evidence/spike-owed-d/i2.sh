#!/bin/bash
# i2.sh: level 1 with the Go netagent against stock Squid over TLS.
. "$(dirname "$0")/lib-l1.sh"
OUT=raw/i2-level1.txt; exec > >(tee -a $OUT) 2>&1
l1_up; squid correct-n 2; nal a 198.19.50.10 pod-a; nal b 198.19.50.11 pod-b
docker exec od-squid squid -v | head -1
echo "### T1 per-CONNECT decision: an allowed, listed host"
s0=$(sqlog); echo "curl: $(via a https://target.oa.test/)"; echo "squid access lines added: $(( $(sqlog)-s0 )); $(docker exec od-squid tail -1 /tmp/access.log | cut -c1-140)"; echo "i7: $(ctl stats)"
echo "### T2 200 sequential new CONNECTs: decisions, proxy lines, and target connections"
d0=$(nastat a); s0=$(sqlog); c0=$(tgtcount)
docker exec od-cla sh -c 'n=0; for i in $(seq 1 200); do curl -s -o /dev/null --cacert /c/ca.pem -x http://127.0.0.1:3128 https://target.oa.test/ && n=$((n+1)); done; echo ok=$n'
echo "netagent before: $d0"; echo "netagent after:  $(nastat a)"; echo "squid lines added: $(( $(sqlog)-s0 )); target before $c0 after $(tgtcount)"
echo "### T3 denied by the decision, on the identity's list: public.oa.test removed from the decision, still listed at the proxy"
ctl "allow?pod=pod-a&hosts=target.oa.test,api.example.test,other.example.test,other.oa.test,rebind.oa.test,private.oa.test" >/dev/null
s0=$(sqlog); c0=$(tgtcount); echo "curl: $(via a https://public.oa.test/)"; echo "squid lines added: $(( $(sqlog)-s0 )); target before $c0 after $(tgtcount)"
echo "body: $(docker exec od-cla curl -s --max-time 5 -x http://127.0.0.1:3128 --proxy-header 'X: y' https://public.oa.test/ -o /dev/null -v 2>&1 | grep -E '^< HTTP|CONNECT tunnel failed' | head -2 | tr '\n' ' ')"
ctl "allow?pod=pod-a&hosts=target.oa.test,public.oa.test,api.example.test,other.example.test,other.oa.test,rebind.oa.test,private.oa.test" >/dev/null
echo "### T4 allowed by the decision, not on the identity's list (sync lag or a contract failure), and per-identity separation"
s0=$(sqlog); echo "pod-a -> other.oa.test (decision allows, pod-a's list lacks it): $(via a https://other.oa.test/)"; echo "squid lines added: $(( $(sqlog)-s0 )); $(docker exec od-squid tail -1 /tmp/access.log | cut -c1-140)"; echo "pod-a stats: $(nastat a)"
echo "reason shown to the client: $(docker exec od-cla curl -s --max-time 5 -x http://127.0.0.1:3128 https://other.oa.test/ -o /dev/null -v 2>&1 | grep -E '^< HTTP' | head -1)"
echo "pod-b -> other.oa.test (listed): $(via b https://other.oa.test/)"; echo "pod-b -> target.oa.test (decision allows, pod-b's list lacks it): $(via b https://target.oa.test/)"
echo "pod-a -> target.oa.test: $(via a https://target.oa.test/)"; echo "pod-b stats: $(nastat b)"
echo "### T4b list edit reaches a new CONNECT: add other.oa.test to pod-a, sync every 2 s"
docctl "host?id=pod-a&add=other.oa.test"; t0=$(date +%s.%N 2>/dev/null); for i in $(seq 1 60); do r=$(via a https://other.oa.test/); case "$r" in *http=200*) echo "pod-a reached it after attempt $i: $r"; break;; esac; /bin/sleep 0.25; done; docctl "host?id=pod-a&del=other.oa.test"; /bin/sleep 5
echo "### T5 decision service frozen, killed, and back: fail closed"
docker pause od-i7 >/dev/null; s0=$(sqlog); echo "frozen: $(via a https://target.oa.test/)"; echo "squid lines added: $(( $(sqlog)-s0 ))"; docker unpause od-i7 >/dev/null
docker stop -t 0 od-i7 >/dev/null; s0=$(sqlog); echo "stopped: $(via a https://target.oa.test/)"; echo "squid lines added: $(( $(sqlog)-s0 ))"; docker start od-i7 >/dev/null; for i in $(seq 1 25); do r=$(via a https://target.oa.test/); case "$r" in *http=200*) echo "recovered at attempt $i: $r"; break;; esac; /bin/sleep 0.2; done
echo "### T6 credential unavailable: a fresh netagent with I8 refusing"
ctl "creddown?v=1" >/dev/null; nal c 198.19.50.12 pod-a; echo "fresh netagent: $(via c https://target.oa.test/)"; ctl "creddown?v=0" >/dev/null; echo "I8 back: $(via c https://target.oa.test/)"; docker rm -f od-clc od-nac >/dev/null
echo "### T7 TLS required: plaintext proxy, wrong CA, wrong server name, no CA, and what crosses the network"
docker run -d --name od-cap --network od-net --ip 198.19.50.52 -v $O:/o:ro python:3.13-alpine python3 /o/capture.py >/dev/null; /bin/sleep 1
PROXYADDR=198.19.50.52:3128 nal d 198.19.50.13 pod-a; echo "netagent against a plaintext listener: $(via d https://target.oa.test/)"; echo "bytes that listener saw (first 96, hex): $(docker logs od-cap 2>&1 | tail -3 | cut -c1-260)"; echo "Proxy-Authorization or the credential in those bytes: $(docker logs od-cap 2>&1 | grep -c -E 'PLAINTEXT_CRED')"; docker rm -f od-cld od-nad od-cap >/dev/null
echo "control, the plaintext listener with a client that does not require TLS:"; docker run -d --name od-cap --network od-net --ip 198.19.50.52 -v $O:/o:ro python:3.13-alpine python3 /o/capture.py >/dev/null; /bin/sleep 1; docker exec od-hc curl -s -o /dev/null --max-time 3 -x http://198.19.50.52:3128 --proxy-user pod-a.g1:pod-a-pw-1 https://target.oa.test/; echo "credential seen in plaintext: $(docker logs od-cap 2>&1 | grep -c PLAINTEXT_CRED)"; docker rm -f od-cap >/dev/null
squid notls 2; nal d 198.19.50.13 pod-a; s0=$(sqlog); echo "Squid with http_port: $(via d https://target.oa.test/); squid lines added $(( $(sqlog)-s0 )); $(nastat d | cut -c1-90)"; docker logs od-nad 2>&1 | grep -m1 'proxy:' | cut -c1-200; docker rm -f od-cld od-nad >/dev/null
squid wrongca 2; nal d 198.19.50.13 pod-a; s0=$(sqlog); echo "Squid with a certificate from another CA: $(via d https://target.oa.test/); squid lines added $(( $(sqlog)-s0 ))"; docker logs od-nad 2>&1 | grep -m1 'proxy:' | cut -c1-200; docker rm -f od-cld od-nad >/dev/null
squid correct-n 2; nal d 198.19.50.13 pod-a -proxy-sni wrong.example; s0=$(sqlog); echo "server name that does not match: $(via d https://target.oa.test/); squid lines added $(( $(sqlog)-s0 ))"; docker logs od-nad 2>&1 | grep -m1 'proxy:' | cut -c1-200; docker rm -f od-cld od-nad >/dev/null
docker run --rm --name od-nocheck --network od-net -v $O/bin:/b:ro -v $O/cfg-l1-bindings.json:/cfg/bindings.json:ro -v $O/certs:/certs:ro alpine:3.22 /b/netagent -level1 -proxy 198.19.50.50:3128 -roots /certs/pub-ca.pem -ca-dir /tmp/x -anchor /tmp/a.pem -i7 https://198.19.50.70:8443 -i7-ca /certs/egress-ca.pem -token pod-a 2>&1 | tail -1 | cut -c1-120
nal a 198.19.50.10 pod-a; nal b 198.19.50.11 pod-b
echo "### T8 407, then one refresh: I8 first hands out a wrong password"
ctl "cred?pod=pod-a&user=pod-a.g1&pass=stale" >/dev/null; nal e 198.19.50.14 pod-a; ctl "cred?pod=pod-a&user=pod-a.g1&pass=pod-a-pw-1" >/dev/null; echo "(the stale one was not fetched yet: netagent fetches on the first CONNECT)"; ctl "cred?pod=pod-a&user=pod-a.g1&pass=stale" >/dev/null; r=$(via e https://target.oa.test/); echo "stale from I8, refresh gets the same stale: $r"; echo "  $(nastat e)"; ctl "cred?pod=pod-a&user=pod-a.g1&pass=pod-a-pw-1" >/dev/null; echo "next CONNECT, refresh gets the good one: $(via e https://target.oa.test/)"; echo "  $(nastat e)"; docker rm -f od-cle od-nae >/dev/null
echo "### T9 rotation with the design's order, and a retired generation still held by netagent"
docctl "gens?id=pod-a&v=1,2"; /bin/sleep 5; echo "overlap: g1 $(docker exec od-hc curl -s -o /dev/null --proxy-cacert /c/ca.pem -x https://lenny-egress.lenny-system.svc:3128 --proxy-user pod-a.g1:pod-a-pw-1 --cacert /c/ca.pem -w 'connect=%{http_connect}' https://target.oa.test/)  g2 $(docker exec od-hc curl -s -o /dev/null --proxy-cacert /c/ca.pem -x https://lenny-egress.lenny-system.svc:3128 --proxy-user pod-a.g2:pod-a-pw-2 --cacert /c/ca.pem -w 'connect=%{http_connect}' https://target.oa.test/)"
echo "netagent still holds g1: $(via a https://target.oa.test/)"; ctl "cred?pod=pod-a&user=pod-a.g2&pass=pod-a-pw-2" >/dev/null; docctl "gens?id=pod-a&v=2"; /bin/sleep 5
echo "g1 retired at the proxy, I8 now releases g2, netagent holds g1 in memory (no binding change)"
for i in 1 2 3; do echo "  CONNECT $i: $(via a https://target.oa.test/)"; done; echo "  $(nastat a)"; echo "  squid: $(docker exec od-squid tail -3 /tmp/access.log | cut -c1-130 | tr '\n' ';')"
nal f 198.19.50.15 pod-a -refresh-on-403; ctl "cred?pod=pod-a&user=pod-a.g1&pass=pod-a-pw-1" >/dev/null; docctl "gens?id=pod-a&v=1,2"; /bin/sleep 5; echo "variant with a refresh on 403 too: g1 held: $(via f https://target.oa.test/)"; ctl "cred?pod=pod-a&user=pod-a.g2&pass=pod-a-pw-2" >/dev/null; docctl "gens?id=pod-a&v=2"; /bin/sleep 5; for i in 1 2; do echo "  CONNECT $i after retirement: $(via f https://target.oa.test/)"; done; echo "  $(nastat f)"; docker rm -f od-clf od-naf >/dev/null
ctl "cred?pod=pod-a&user=pod-a.g1&pass=pod-a-pw-1" >/dev/null; docctl "gens?id=pod-a&v=1"; /bin/sleep 5; nal a 198.19.50.10 pod-a
echo "### T10 server-name fronting: CONNECT to a listed host, TLS naming an unlisted one"
c0=$(docker logs od-tgt 2>&1 | grep -c 'sni="other.oa.test"'); echo "through netagent: $(via a https://other.oa.test/ --connect-to other.oa.test:443:public.oa.test:443)"; echo "  target saw sni=other.oa.test: $(( $(docker logs od-tgt 2>&1 | grep -c 'sni="other.oa.test"')-c0 )); $(nastat a | grep -o '"sniClosed":[0-9]*')"
c0=$(docker logs od-tgt 2>&1 | grep -c 'sni="other.oa.test"'); echo "control, the same credential straight to Squid: $(docker exec od-hc curl -s -o /dev/null --max-time 6 --cacert /c/ca.pem --proxy-cacert /c/ca.pem -x https://lenny-egress.lenny-system.svc:3128 --proxy-user pod-a.g1:pod-a-pw-1 -w 'connect=%{http_connect} http=%{http_code}' --connect-to other.oa.test:443:public.oa.test:443 https://other.oa.test/)"; echo "  target saw sni=other.oa.test: $(( $(docker logs od-tgt 2>&1 | grep -c 'sni="other.oa.test"')-c0 ))"
echo "authority typed in upper case with a trailing dot: $(via a https://TARGET.OA.TEST./)"; echo "tunnel with no TLS: $(docker exec od-cla sh -c 'curl -s -o /dev/null --max-time 6 -x http://127.0.0.1:3128 -p http://target.oa.test:443/ -w "connect=%{http_connect} http=%{http_code} rc=%{exitcode}"')"; echo "  $(nastat a | grep -o '"sniClosed":[0-9]*')"
echo "### T11 address rule through netagent: the decision allows rebind.oa.test, listed for pod-a, which resolves to 172.30.50.20"
c0=$(tgtcount); echo "correct-n: $(via a https://rebind.oa.test/)"; echo "  target before $c0 after $(tgtcount)"
echo "### T12 inject through level 1: terminated host, decision per upstream tunnel"
d0=$(nastat a | grep -o '"decide":[0-9]*'); out=$(docker exec od-cla sh -c 'cat /run/netagent/ca.pem /certs/pub-ca.pem > /tmp/t.pem; curl -s --max-time 6 --cacert /tmp/t.pem -x http://127.0.0.1:3128 -H "Authorization: Bearer lenny-injected" https://api.example.test/echo'); echo "echo through inject: $(echo "$out" | cut -c1-260)"; echo "real secret visible to the client in that body: $(echo "$out" | grep -c ACME-REAL)"
echo "ocup received the real secret (scrubbed copy above shows masked): $(echo "$out" | grep -c xxxxxxxx)"
echo "decisions before $d0 after $(nastat a | grep -o '"decide":[0-9]*')"
docker exec od-cla sh -c 'cat /run/netagent/ca.pem /certs/pub-ca.pem > /tmp/t.pem; curl -s -o /dev/null --max-time 15 --cacert /tmp/t.pem -x http://127.0.0.1:3128 $(for i in $(seq 1 20); do echo https://api.example.test/small; done) -w "%{http_code} "'; echo; echo "after 20 requests on one client connection: $(nastat a | grep -o '"decide":[0-9]*'), proxy tunnels $(nastat a | grep -o '"opens":[0-9]*')"
echo "### T13 tunnel lifetime, binding change, and open tunnels at the proxy"
nal g 198.19.50.16 pod-a -tunnel-max 3s; docker exec -d od-clg sh -c 'curl -s -k --max-time 30 -x http://127.0.0.1:3128 -o /dev/null -w "%{time_total} rc=%{exitcode}\n" https://other.example.test/hold > /tmp/h1 2>&1'; /bin/sleep 6; echo "tunnel with a 3 s lifetime ended: $(docker exec od-clg cat /tmp/h1)"; echo "  $(nastat g)"; docker rm -f od-clg od-nag >/dev/null
docker exec -d od-cla sh -c 'curl -s -k --max-time 30 -x http://127.0.0.1:3128 -o /dev/null -w "%{time_total} rc=%{exitcode}\n" https://other.example.test/hold > /tmp/h2 2>&1'; /bin/sleep 3; ctl "bump?pod=pod-a" >/dev/null; /bin/sleep 1; echo "binding change at about t=3 s, tunnel ended: $(docker exec od-cla cat /tmp/h2)"; echo "  $(nastat a)"
echo "a list edit alone: tunnel open before the edit, after the edit"; docker exec -d od-cla sh -c 'curl -s -k --max-time 12 -x http://127.0.0.1:3128 -o /dev/null -w "%{time_total} rc=%{exitcode}\n" https://other.example.test/hold > /tmp/h3 2>&1'; /bin/sleep 2; docctl "host?id=pod-a&del=other.example.test"; /bin/sleep 5; echo "  new CONNECT after the edit: $(via a https://other.example.test/ -k)"; /bin/sleep 6; echo "  the open tunnel lasted: $(docker exec od-cla cat /tmp/h3)"; docctl "host?id=pod-a&add=other.example.test"; /bin/sleep 5
echo "the proxy keeps accepting the identity after the binding ended, from any source: $(docker exec od-hc curl -s -o /dev/null --max-time 6 --cacert /c/ca.pem --proxy-cacert /c/ca.pem -x https://lenny-egress.lenny-system.svc:3128 --proxy-user pod-a.g1:pod-a-pw-1 -w 'connect=%{http_connect} http=%{http_code}' https://target.oa.test/)"
echo "grace: decision service stopped with a tunnel open, -grace 5s"; nal g 198.19.50.16 pod-a -grace 5s; /bin/sleep 1; docker exec -d od-clg sh -c 'curl -s -k --max-time 25 -x http://127.0.0.1:3128 -o /dev/null -w "%{time_total} rc=%{exitcode}\n" https://other.example.test/hold > /tmp/h4 2>&1'; /bin/sleep 2; docker stop -t 0 od-i7 >/dev/null; /bin/sleep 22; echo "  tunnel ended (the long poll must fail first): $(docker exec od-clg cat /tmp/h4)"; docker start od-i7 >/dev/null; /bin/sleep 2; docker rm -f od-clg od-nag >/dev/null
echo "### T14 reports: opens and closes against the proxy log"
nal h 198.19.50.17 pod-a; s0=$(sqlog); docker exec od-clh sh -c 'for i in $(seq 1 30); do curl -s -o /dev/null --cacert /c/ca.pem -x http://127.0.0.1:3128 https://target.oa.test/; done'; /bin/sleep 2.5; echo "  netagent $(nastat h)"; echo "  squid lines added $(( $(sqlog)-s0 ))"; echo "  i7 $(ctl stats)"; docker rm -f od-clh od-nah >/dev/null
echo "### T15 proxy stopped"
docker stop -t 0 od-squid >/dev/null; echo "stopped: $(via a https://target.oa.test/)"; echo "  $(nastat a | cut -c1-90)"; docker start od-squid >/dev/null; for i in $(seq 1 40); do docker logs od-squid 2>&1 | tail -30 | grep -q Accepting && break; /bin/sleep 0.25; done; /bin/sleep 2; echo "started: $(via a https://target.oa.test/)"
echo "### T16 the probe against this Squid with netagent traffic beside it"
sum(){ docker exec od-py python3 -c 'import sys,json
p=f=0;v=""
for l in sys.stdin:
    d=json.loads(l)
    if "verdict" in d: v=d["verdict"]; continue
    if d["pass"]: p+=1
    else: print("    FAIL",d["check"],d["target"],d["detail"][:90])
print("   verdict",v,"pass",p)' ; }
probe(){ docker exec od-hc /b/l1probe -proxy 198.19.50.50:3128 -source http://198.19.50.51:8080/probe "$@"; }
docker exec -d od-cla sh -c 'timeout 60 /b/naload rps -kind h1 -ca /c/ca.pem -rate 50 -for 55s -new -url https://target.oa.test/ > /tmp/bg.out 2>&1'
echo "preflight:"; probe -mode preflight | docker exec -i od-py python3 -c 'import sys,json
for l in sys.stdin:
    d=json.loads(l)
    print("  ",{k:d.get(k) for k in ("check","pass","verdict") if k in d})'
for f in "-window 5s" "-window 5s -addr-control"; do echo "full $f:"; probe $f | docker exec -i od-py python3 -c 'import sys,json
p=0;v=""
for l in sys.stdin:
    d=json.loads(l)
    if "verdict" in d: v=d["verdict"]; continue
    if d["pass"]: p+=1
    else: print("    FAIL",d["check"],d["target"],d["detail"][:100])
print("   verdict",v,"checks passed",p)'; done
/bin/sleep 45; echo "netagent traffic beside the probe: $(docker exec od-cla cat /tmp/bg.out | cut -c1-160)"
echo "probe with -status-only is out of scope here. Broken configurations with netagent's outcome beside the probe's:"
for v in noauth fakeauth refuseall noaddr addrafter partialaddr-n; do squid $v 2; a=$(probe -window 5s -addr-control | docker exec -i od-py python3 -c 'import sys,json
n=[]; v=""
for l in sys.stdin:
    d=json.loads(l)
    if "verdict" in d: v=d["verdict"]
    elif not d["pass"]: n.append(d["check"])
print(v,n[:6])'); echo "  $v: probe $a | netagent to a listed host: $(via a https://target.oa.test/) | netagent to the private name: $(via a https://rebind.oa.test/)"; done
squid correct-n 2
echo "### T17 CONNECT setup cost through netagent at level 1, new connection per request, TLS through to target"
for d in "" "-no-decide"; do nal i 198.19.50.18 pod-a $d; for r in 50 200; do B=$(docker exec od-squid cat /sys/fs/cgroup/cpu.stat | awk '/usage_usec/{print $2}'); x=$(docker exec od-cli /b/naload rps -kind h1 -ca /c/ca.pem -rate $r -for 20s -new -url https://target.oa.test/); A=$(docker exec od-squid cat /sys/fs/cgroup/cpu.stat | awk '/usage_usec/{print $2}'); echo "  ${d:-with decision}: $x | squid $(( (A-B)/20000 ))m $(docker exec od-squid awk '{printf "%.0f MiB",$1/1048576}' /sys/fs/cgroup/memory.current)"; done; echo "  netagent: $(docker exec od-cli curl -s http://127.0.0.1:9100/stats | cut -c1-120)"; docker rm -f od-cli od-nai >/dev/null; done
echo "### T18 git clone through level 1, opaque tunnel to a git host on the list"
docker run --rm --name od-gitinit -v od-repos:/repos spike-tls-git sh -c 'git config --global init.defaultBranch main; rm -rf /repos/r.git; git init -q --bare /repos/r.git; cd /tmp && git init -q w && cd w && git -c user.email=a@b -c user.name=a commit -q --allow-empty -m one && echo hello > f && git add f && git -c user.email=a@b -c user.name=a commit -q -m two && git push -q /repos/r.git main && git -C /repos/r.git rev-parse main'
docker rm -f od-ocup >/dev/null; docker run -d --name od-git --network od-net --ip 198.19.50.21 --cpus 2 -v od-repos:/repos -v $(dirname $O)/spike-owed-a/bin:/b2:ro -v $O/bin:/b:ro -v $O/certs:/certs:ro -e GIT_CONFIG_COUNT=2 -e GIT_CONFIG_KEY_0=http.receivepack -e GIT_CONFIG_VALUE_0=true -e GIT_CONFIG_KEY_1=safe.directory -e GIT_CONFIG_VALUE_1='*' spike-tls-git /b2/gitsrv -cert /certs/up/tls.crt -key /certs/up/tls.key >/dev/null 2>&1 || echo git server failed
/bin/sleep 1; docker run --rm --name od-gitc --network container:od-naa -v $O/certs:/certs:ro -e https_proxy=http://127.0.0.1:3128 spike-tls-git sh -c 'GIT_SSL_CAINFO=/certs/pub-ca.pem git clone -q https://other.example.test/r.git /tmp/c 2>&1 | tail -2; git -C /tmp/c log --oneline 2>&1 | wc -l'
echo "  netagent: $(nastat a)"
echo "### done"
