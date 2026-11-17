#!/bin/bash
L1=$1; P=http://198.19.0.50:3128
c(){ a=$1; u=$2; shift 2; docker exec ls-pod2 curl -sk -o /dev/null --max-time 6 -x $P ${a:+--proxy-user "$a"} -w 'connect=%{http_connect} http=%{http_code}' "$@" "$u"; echo; }
reload(){ docker exec ls-squid sh -c "cp /l1/$1 /run/l1/p.json && python3 /l1/render.py /run/l1/p.json /run/l1 ssrf=1 && chmod -R a+rX /run/l1 && squid -k reconfigure -f /run/l1/squid.conf" >/dev/null 2>&1; }
V1=$(openssl passwd -5 pw-gen-1); V2=$(openssl passwd -5 pw-gen-2)
python3 - "$L1" "$V1" "$V2" <<'PY'
import json,sys
L,v1,v2=sys.argv[1:]
base={"version":1,"credentialsTtlSeconds":7200,"identities":[{"name":"p1-acme","hosts":["git.spike"],"credentials":[]}]}
c1={"username":"p1-acme.g1","verifier":v1}; c2={"username":"p1-acme.g2","verifier":v2}
for f,cs in (("r1",[c1]),("r2",[c1,c2]),("r3",[c2])):
    base["identities"][0]["credentials"]=cs; json.dump(base,open(f"{L}/{f}.json","w"))
PY
./start.sh $L1 r1.json ssrf=1
echo "generation 1 only, g1      : $(c p1-acme.g1:pw-gen-1 https://git.spike/fast.git/)"
reload r2.json; /bin/sleep 1
echo "overlap, g1 (cached 7200 s): $(c p1-acme.g1:pw-gen-1 https://git.spike/fast.git/)"
echo "overlap, g2                : $(c p1-acme.g2:pw-gen-2 https://git.spike/fast.git/)"
reload r3.json; /bin/sleep 1
echo "g1 retired, g1 (was cached): $(c p1-acme.g1:pw-gen-1 https://git.spike/fast.git/)"
echo "g1 retired, g2             : $(c p1-acme.g2:pw-gen-2 https://git.spike/fast.git/)"
