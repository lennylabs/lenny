#!/bin/bash
L1=$1
P=http://198.19.0.50:3128
c(){ # c USER:PW URL [extra curl args]
  a=$1; u=$2; shift 2
  docker exec ls-pod2 curl -sk -o /dev/null --max-time 6 -x $P ${a:+--proxy-user "$a"} -w 'connect=%{http_connect} http=%{http_code}' "$@" "$u" ; echo; }
sq(){ docker exec ls-squid "$@"; }
reload(){ sq sh -c "cp /l1/$1 /run/l1/p.json && python3 /l1/render.py /run/l1/p.json /run/l1 $2 && chmod -R a+rX /run/l1 && squid -k reconfigure -f /run/l1/squid.conf"; }
./start.sh $L1 policy1.json
echo "### per-identity lists"
echo "acme  -> git.spike (listed)      : $(c p1-acme:acme-pw-1 https://git.spike/fast.git/)"
echo "acme  -> other.spike (not listed): $(c p1-acme:acme-pw-1 https://other.spike/fast.git/)"
echo "globex-> other.spike (listed)    : $(c p1-globex:globex-pw-1 https://other.spike/fast.git/)"
echo "globex-> git.spike (not listed)  : $(c p1-globex:globex-pw-1 https://git.spike/fast.git/)"
echo "no credential                    : $(c '' https://git.spike/fast.git/)"
echo "wrong password                   : $(c p1-acme:nope https://git.spike/fast.git/)"
echo "### gateway resolver through level 1, one tunnel per call"
echo "git.spike : $(docker exec -e PROXY_USER=p1-acme ls-client /b/lsrefs -mode one -insecure -proxy $P -token acme-pw-1 -repo https://git.spike/fast.git)"
echo "real host : $(docker exec -e PROXY_USER=p1-acme ls-client /b/lsrefs -mode one -proxy $P -token acme-pw-1 -repo https://github.com/git/git -ref master)"
echo "denied    : $(docker exec -e PROXY_USER=p1-globex ls-client /b/lsrefs -mode one -insecure -proxy $P -token globex-pw-1 -repo https://git.spike/fast.git)"
echo "### the credential works from any pod that can reach the proxy"
echo "same credential from another container: $(docker exec ls-client wget -qO- --no-check-certificate -e use_proxy=yes -e https_proxy=$P --proxy-user=p1-acme --proxy-password=acme-pw-1 https://git.spike/fast.git/ 2>&1 | wc -c) bytes body (empty body is success; an error prints text)"
echo "### SSRF: stock Squid with no address rule, allowlist contains a name that resolves privately, and literals"
echo "rebind.spike -> 172.30.0.20  : $(c p1-acme:acme-pw-1 https://rebind.spike/fast.git/)"
echo "literal 127.0.0.1            : $(c p1-acme:acme-pw-1 https://127.0.0.1/)"
echo "literal 169.254.169.254      : $(c p1-acme:acme-pw-1 https://169.254.169.254/)"
echo "literal not on the list      : $(c p1-acme:acme-pw-1 https://172.30.0.20/)"
./start.sh $L1 policy1.json ssrf=1
echo "--- with the contract's address rule"
echo "rebind.spike -> 172.30.0.20  : $(c p1-acme:acme-pw-1 https://rebind.spike/fast.git/)"
echo "literal 127.0.0.1            : $(c p1-acme:acme-pw-1 https://127.0.0.1/)"
echo "literal 169.254.169.254      : $(c p1-acme:acme-pw-1 https://169.254.169.254/)"
echo "git.spike still allowed      : $(c p1-acme:acme-pw-1 https://git.spike/fast.git/)"
echo "### server-name fronting: CONNECT to a listed host, TLS and Host naming an unlisted one"
echo "acme, inner other.spike      : $(c p1-acme:acme-pw-1 https://other.spike/fast.git/ --connect-to other.spike:443:git.spike:443)"
echo "### revocation: remove git.spike from acme, reconfigure"
docker exec -d ls-pod2 sh -c "curl -sk --max-time 40 -x $P --proxy-user p1-acme:acme-pw-1 -o /dev/null -w 'held tunnel ended: connect=%{http_connect} time=%{time_total}\n' https://git.spike/slow.git/git-upload-pack > /tmp/held.out 2>&1"
/bin/sleep 2
t0=$(python3 -c 'import time;print(time.time())'); reload policy-acme-loses-git.json ssrf=1
for i in $(seq 1 40); do r=$(c p1-acme:acme-pw-1 https://git.spike/fast.git/); case "$r" in *connect=403*) break;; esac; /bin/sleep 0.25; done
echo "new CONNECT after reconfigure: $r, after $(python3 -c "import time;print(round(time.time()-$t0,2))") s"
/bin/sleep 8; echo "tunnel opened before the edit, 8 s later: $(docker exec ls-pod2 sh -c 'cat /tmp/held.out; pgrep -f slow.git >/dev/null && echo still open || echo closed' | tr '\n' ' ')"
echo "### rotation: add a new verifier beside the old, then drop the old"
NEW=$(openssl passwd -5 acme-pw-2)
python3 - "$L1" "$NEW" <<'PY'
import json,sys
d=json.load(open(sys.argv[1]+"/policy1.json"))
a=d["identities"][0]; a["credentialVerifiers"].append(sys.argv[2]); json.dump(d,open(sys.argv[1]+"/pol-overlap.json","w"))
a["credentialVerifiers"]=[sys.argv[2]]; json.dump(d,open(sys.argv[1]+"/pol-new.json","w"))
PY
./start.sh $L1 pol-overlap.json ssrf=1
echo "overlap, old password : $(c p1-acme:acme-pw-1 https://git.spike/fast.git/)"
echo "overlap, new password : $(c p1-acme:acme-pw-2 https://git.spike/fast.git/)"
reload pol-new.json ssrf=1; /bin/sleep 1
echo "old dropped, old password immediately (ttl 60 s) : $(c p1-acme:acme-pw-1 https://git.spike/fast.git/)"
echo "old dropped, new password                        : $(c p1-acme:acme-pw-2 https://git.spike/fast.git/)"
echo "### default Squid credential cache: stale old password"
python3 - "$L1" <<'PY'
import json,sys
for f in ("pol-overlap.json","pol-new.json"):
    d=json.load(open(sys.argv[1]+"/"+f)); d["credentialsTtlSeconds"]=7200; json.dump(d,open(sys.argv[1]+"/"+f,"w"))
PY
./start.sh $L1 pol-overlap.json ssrf=1
echo "old password cached   : $(c p1-acme:acme-pw-1 https://git.spike/fast.git/)"
reload pol-new.json ssrf=1; /bin/sleep 1
echo "after verifier removal with ttl 7200 s: $(c p1-acme:acme-pw-1 https://git.spike/fast.git/)"
