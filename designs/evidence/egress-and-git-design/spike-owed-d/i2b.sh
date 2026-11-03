#!/bin/bash
# i2b.sh: the parts of i2.sh a harness defect spoiled (a restarted Squid container exits at once, so T15's recovery and T16's first runs saw no proxy), plus T12b and T18 again.
. "$(dirname "$0")/lib-l1.sh"
OUT=raw/i2-level1.txt; exec > >(tee -a $OUT) 2>&1
echo; echo "##### second pass, $(date)"
echo "### T18 again"
c=$(docker run --rm --name od-gitc --network container:od-naa -v $O/certs:/certs:ro -e https_proxy=http://127.0.0.1:3128 spike-tls-git sh -c 'GIT_SSL_CAINFO=/certs/pub-ca.pem git clone https://other.example.test/r.git /tmp/c 2>&1 | tail -3; git -C /tmp/c log --oneline 2>&1 | cut -c1-60; git -C /tmp/c rev-parse HEAD 2>&1'); echo "$c"; echo "server head: $(docker run --rm --name od-gitr -v od-repos:/repos spike-tls-git git -C /repos/r.git -c safe.directory='*' rev-parse main 2>&1)"
docker rm -f od-git >/dev/null; docker run -d --name od-ocup --network od-net --ip 198.19.50.21 --ulimit nofile=200000:200000 -v $O/bin:/b:ro -v $O/certs:/certs:ro -e ACME_SECRET=ACME-REAL-9f2c71 alpine:3.22 /b/ocup -quiet >/dev/null
squid correct-n 2; nal a 198.19.50.10 pod-a; docker exec od-py true
echo "### T15 proxy recreated after a stop"
docker rm -f od-squid >/dev/null; echo "proxy gone: $(via a https://target.oa.test/)"; echo "  $(nastat a | cut -c1-90)"; squid correct-n 2; for i in $(seq 1 40); do r=$(via a https://target.oa.test/); case "$r" in *http=200*) echo "proxy back at attempt $i: $r"; break;; esac; /bin/sleep 0.25; done
echo "### T12b a terminated host: the upstream tunnel netagent keeps idle, and a decision that has changed"
d0=$(nastat a | grep -o '"decide":[0-9]*'); docker exec od-cla sh -c 'cat /run/netagent/ca.pem /certs/pub-ca.pem > /tmp/t.pem; curl -s -o /dev/null --cacert /tmp/t.pem -x http://127.0.0.1:3128 -w "first client connection: %{http_code}\n" https://api.example.test/small'; echo "  decisions $d0 -> $(nastat a | grep -o '"decide":[0-9]*')"
ctl "allow?pod=pod-a&hosts=target.oa.test,public.oa.test" >/dev/null; echo "  the decision service no longer allows api.example.test"
docker exec od-cla sh -c 'for i in 1 2 3; do curl -s -o /dev/null --cacert /tmp/t.pem -x http://127.0.0.1:3128 -w "new client CONNECT $i: connect=%{http_connect} http=%{http_code}\n" https://api.example.test/small; done'; echo "  decisions now $(nastat a | grep -o '"decide":[0-9]*'), opens $(nastat a | grep -o '"opens":[0-9]*')"
ctl "bump?pod=pod-a" >/dev/null; /bin/sleep 1; docker exec od-cla sh -c 'curl -s -o /dev/null --cacert /tmp/t.pem -x http://127.0.0.1:3128 -w "after a binding change closes tunnels: connect=%{http_connect} http=%{http_code}\n" https://api.example.test/small'
ctl "allow?pod=pod-a&hosts=target.oa.test,public.oa.test,api.example.test,other.example.test,other.oa.test,rebind.oa.test,private.oa.test" >/dev/null
echo "### T12c a private destination at level 1: the decision returns an address and netagent dials it directly"
s0=$(sqlog); c0=$(tgtcount); echo "private.oa.test: $(via a https://private.oa.test/ --resolve private.oa.test:443:198.19.50.20 -k)"; echo "  squid lines added $(( $(sqlog)-s0 )), target before $c0 after $(tgtcount); $(nastat a | grep -o '"direct":[0-9]*')"
echo "### T16 the probe, correct-n, with netagent traffic beside it"
probe(){ docker exec od-hc /b/l1probe -proxy 198.19.50.50:3128 -source http://198.19.50.51:8080/probe "$@"; }
docker exec -d od-cla sh -c 'timeout 60 /b/naload rps -kind h1 -ca /c/ca.pem -rate 50 -for 55s -new -url https://target.oa.test/ > /tmp/bg.out 2>&1'
echo "preflight:"; probe -mode preflight | docker exec -i od-py python3 -c 'import sys,json
for l in sys.stdin:
    d=json.loads(l)
    print("  ",{k:d.get(k) for k in ("check","pass","verdict","detail") if k in d})'
for f in "-window 5s" "-window 5s -addr-control" "-window 5s -addr-control"; do echo "full $f:"; probe $f | docker exec -i od-py python3 -c 'import sys,json
p=0;v=""
for l in sys.stdin:
    d=json.loads(l)
    if "verdict" in d: v=d["verdict"]; continue
    if d["pass"]: p+=1
    else: print("    FAIL",d["check"],d["target"],d["detail"][:100])
print("   verdict",v,"checks passed",p)'; /bin/sleep 3; done
/bin/sleep 45; echo "netagent traffic beside the probe: $(docker exec od-cla cat /tmp/bg.out | cut -c1-160)"
echo "### T16b TLS-required variants through the probe: notls and wrongca"
for v in notls wrongca; do squid $v 2; a=$(probe -mode preflight | docker exec -i od-py python3 -c 'import sys,json
n=[]; v=""
for l in sys.stdin:
    d=json.loads(l)
    if "verdict" in d: v=d["verdict"]
    elif not d["pass"]: n.append((d["check"],d["detail"][:70]))
print(v,n[:3])'); echo "  $v: preflight $a"; done
echo "### done second pass"
docker rm -f $(docker ps -aq --filter name=od-cl) $(docker ps -aq --filter name=od-na) od-squid od-l1doc od-tgt od-ocup od-i7 od-hc >/dev/null 2>&1; docker network rm od-net od-priv od-priv2 >/dev/null 2>&1; docker volume rm od-repos >/dev/null 2>&1; echo done > raw/i2-done
