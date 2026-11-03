#!/bin/bash
# i2c.sh: third pass: git through level 1 with a fixture that has a default branch, and the probe with traffic whose result is read after it ends.
. "$(dirname "$0")/lib-l1.sh"
OUT=raw/i2-level1.txt; exec > >(tee -a $OUT) 2>&1
echo; echo "##### third pass, $(date)"
l1_up; squid correct-n 2; nal a 198.19.50.10 pod-a
docker exec -d od-cla sh -c '/b/naload rps -kind h1 -ca /c/ca.pem -rate 50 -for 40s -new -url https://target.oa.test/ > /tmp/bg.out 2>&1'
probe(){ docker exec od-hc /b/l1probe -proxy 198.19.50.50:3128 -source http://198.19.50.51:8080/probe "$@"; }
for f in "-window 5s -addr-control" "-window 5s -addr-control"; do probe $f | docker exec -i od-py python3 -c 'import sys,json
p=0;v=""
for l in sys.stdin:
    d=json.loads(l)
    if "verdict" in d: v=d["verdict"]; continue
    if d["pass"]: p+=1
    else: print("    FAIL",d["check"],d["target"],d["detail"][:100])
print("   probe verdict",v,"checks passed",p)'; /bin/sleep 3; done; /bin/sleep 40; echo "   netagent traffic beside the probe: $(docker exec od-cla cat /tmp/bg.out | cut -c1-160)"
echo "### T18 git clone and push through level 1, opaque tunnel"
docker volume create od-repos >/dev/null; docker run --rm --name od-gitinit -v od-repos:/repos spike-tls-git sh -c 'rm -rf /repos/r.git; git init -q --bare -b main /repos/r.git; cd /tmp && git init -q -b main w && cd w && git -c user.email=a@b -c user.name=a commit -q --allow-empty -m one && echo hello > f && git add f && git -c user.email=a@b -c user.name=a commit -q -m two && git push -q /repos/r.git main && git -C /repos/r.git rev-parse main' 2>&1 | tail -1
docker rm -f od-ocup >/dev/null; docker run -d --name od-git --network od-net --ip 198.19.50.21 --cpus 2 -v od-repos:/repos -v $(dirname $O)/spike-owed-a/bin:/b2:ro -v $O/bin:/b:ro -v $O/certs:/certs:ro -e GIT_CONFIG_COUNT=2 -e GIT_CONFIG_KEY_0=http.receivepack -e GIT_CONFIG_VALUE_0=true -e GIT_CONFIG_KEY_1=safe.directory -e GIT_CONFIG_VALUE_1='*' spike-tls-git /b2/gitsrv -cert /certs/up/tls.crt -key /certs/up/tls.key >/dev/null; /bin/sleep 1
docker run --rm --name od-gitc --network container:od-naa -v $O/certs:/certs:ro -e https_proxy=http://127.0.0.1:3128 -e GIT_SSL_CAINFO=/certs/pub-ca.pem spike-tls-git sh -c 'git clone -q https://other.example.test/r.git /tmp/c 2>&1 | tail -2; git -C /tmp/c log --oneline 2>&1 | wc -l; git -C /tmp/c rev-parse HEAD; cd /tmp/c; git -c user.email=a@b -c user.name=a commit -q --allow-empty -m three; git push -q origin HEAD:refs/heads/main 2>&1 | tail -2; echo push rc=$?; git rev-parse HEAD'; echo "   server head after the push: $(docker run --rm --name od-gitr -v od-repos:/repos spike-tls-git git -C /repos/r.git -c safe.directory='*' rev-parse main 2>&1)"
echo "   netagent: $(nastat a)"; echo "   squid: $(docker exec od-squid tail -2 /tmp/access.log | cut -c1-120 | tr '\n' ';')"
docker rm -f $(docker ps -aq --filter name=od-cl) $(docker ps -aq --filter name=od-na) od-squid od-l1doc od-tgt od-git od-i7 od-hc >/dev/null 2>&1; docker network rm od-net od-priv od-priv2 >/dev/null 2>&1; docker volume rm od-repos >/dev/null 2>&1; echo "### done third pass"
