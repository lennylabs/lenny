#!/bin/bash
cd "$(dirname "$0")"; D=$PWD
cpu(){ docker exec "$1" sh -c 'grep usage_usec /sys/fs/cgroup/cpu.stat | cut -d" " -f2'; }
echo "### I5: stock git through a loopback forwarder to the mirror stand-in"
docker rm -f t-mir t-gitc >/dev/null 2>&1
docker run -d --name t-mir --cpus 2 --network stls-pub --ip 198.19.0.60 -v stls-repos:/repos:ro -v $D/bin:/b:ro -v $D/certs:/c:ro -e GIT_CONFIG_COUNT=2 -e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0='*' -e GIT_CONFIG_KEY_1=uploadpack.allowFilter -e GIT_CONFIG_VALUE_1=true spike-tls-git sh -c '/b/mir serve -addr :8080 & /b/mir serve -addr :8443 -cert /c/good/tls.crt -key /c/good/tls.key & wait' >/dev/null
docker run -d --name t-gitc --network stls-pub --ip 198.19.0.61 -v $D/bin:/b:ro -v $D/certs:/c:ro spike-tls-git sh -c '/b/mir fwd -addr 127.0.0.1:3129 -up http://198.19.0.60:8080 & /b/mir fwd -addr 127.0.0.1:3130 -up https://198.19.0.60:8443 -ca /c/ca1.pem & wait' >/dev/null
/bin/sleep 1.5
cat > /tmp/gt.sh <<'IN'
port=$1; name=$2; shift 2; d=$(mktemp -d)
s=$(date +%s.%N); git "$@" "http://127.0.0.1:$port/git.git" "$d/r" 2>"$d/err"; rc=$?; e=$(date +%s.%N)
head=$(git -C "$d/r" rev-parse HEAD 2>/dev/null | cut -c1-10); n=$(git -C "$d/r" count-objects -v 2>/dev/null | awk '/in-pack/{print $2}')
printf "%-10s rc=%s %6.1fs head=%s objects=%s\n" "$name" $rc "$(echo "$e - $s" | bc)" "$head" "$n"
IN
docker cp /tmp/gt.sh t-gitc:/gt.sh >/dev/null; docker exec t-gitc apk add -q bc >/dev/null 2>&1
for m in "3129 plain" "3130 tls"; do set -- $m; P=$1; N=$2
  M0=$(cpu t-mir); C0=$(cpu t-gitc)
  docker exec t-gitc sh /gt.sh $P $N-full clone -q --bare
  M1=$(cpu t-mir); C1=$(cpu t-gitc); python3 -c "print('  $N full clone cpu: mirror',round(($M1-$M0)/1e6,1),'s, client side',round(($C1-$C0)/1e6,1),'s')"
  docker exec t-gitc sh /gt.sh $P $N-shallow clone -q --depth 1 --single-branch
  docker exec t-gitc sh /gt.sh $P $N-partial clone -q --filter=blob:none --no-checkout
  docker exec t-gitc sh -c "git ls-remote http://127.0.0.1:$P/git.git refs/heads/master | cut -c1-10" | sed "s/^/  $N ls-remote /"
  docker exec t-gitc sh -c "d=\$(mktemp -d); git clone -q --depth 1 --single-branch http://127.0.0.1:$P/git.git \$d/r && git -C \$d/r fetch -q --deepen=200 && git -C \$d/r fsck --connectivity-only 2>&1 | tail -1 | cut -c1-50; git -C \$d/r rev-list --count HEAD" | tr '\n' ' ' | sed "s/^/  $N deepen+fsck (gzip POST): /"; echo
  docker exec t-gitc sh -c "d=\$(mktemp -d); git clone --progress --depth 2000 --single-branch http://127.0.0.1:$P/git.git \$d/r 2>&1 | tr '\r' '\n' | grep -c -E 'Counting|Compressing|Receiving|remote:'" | sed "s/^/  $N progress lines streamed: /"
done
docker rm -f t-mir t-gitc >/dev/null 2>&1
echo "### I7: 2000 gRPC streams with a per-RPC token, then a server restart"
for m in plain tls; do
  docker rm -f t-i7s t-i7c >/dev/null 2>&1
  A=""; [ $m = tls ] && A="-cert /c/good/tls.crt -key /c/good/tls.key"; C=""; [ $m = tls ] && C="-ca /c/ca1.pem"
  docker run -d --name t-i7s --cpus 1 --network stls-pub --ip 198.19.0.70 --ulimit nofile=65536:65536 -v $D/bin:/b:ro -v $D/certs:/c:ro alpine:3.22 sh -c "while :; do /b/i7 serve $A; sleep 1; done" >/dev/null; /bin/sleep 1
  docker run -d --name t-i7c --network stls-pub --ip 198.19.0.71 --ulimit nofile=65536:65536 -v $D/bin:/b:ro -v $D/certs:/c:ro alpine:3.22 /b/i7 subs -addr 198.19.0.70:7443 $C -n 2000 -rate 500 -dur 45s >/dev/null
  /bin/sleep 12; M=$(docker stats --no-stream --format '{{.MemUsage}}' t-i7s | cut -d/ -f1); L=$(docker logs t-i7c 2>&1 | tail -1)
  C0=$(cpu t-i7s); docker exec t-i7s pkill -9 -x i7; /bin/sleep 25
  C1=$(cpu t-i7s); echo "$m: server mem at 2000 streams=$M, before restart [$L]"
  docker logs t-i7c 2>&1 | awk -v t=12 '{split($2,a,"="); split($0,b," "); print}' | sed -n '12,40p' | awk 'BEGIN{p=""} {if ($0 ~ /live=2000/ && !f && NR>2 && seen0) {print "   recovered: " $0; f=1} if ($0 ~ /live=[0-9]{1,3} /) seen0=1}'
  python3 -c "print('   reconnect cpu over 25 s:',round(($C1-$C0)/1e6,1),'cpu-s')"
done
docker rm -f t-i7s t-i7c >/dev/null 2>&1
echo "### level 1: Squid over TLS with Basic"
L1=$D/squid; mkdir -p $L1
cp $D/../spike-level1/render.py $L1/render.py
python3 - "$L1" <<'PY'
import json,subprocess,sys
L=sys.argv[1]
v=subprocess.check_output(["openssl","passwd","-5","acme-pw-1"],text=True).strip()
json.dump({"credentialsTtlSeconds":60,"identities":[{"name":"p1-acme","hosts":["target.spike"],"credentials":[{"username":"p1-acme.g1","verifier":v}]}]},open(L+"/p.json","w"))
s=open(L+"/render.py").read().replace('http_port 3128\n','{"https_port 3128 tls-cert=/c/good/tls.crt tls-key=/c/good/tls.key" if "tls=1" in sys.argv else "http_port 3128"}\n')
open(L+"/render.py","w").write(s)
PY
for m in plain tls; do
  docker rm -f t-squid >/dev/null 2>&1; A=""; [ $m = tls ] && A="tls=1"
  docker run -d --name t-squid --cpus 2 --network stls-pub --ip 198.19.0.50 --add-host target.spike:198.19.0.20 -v $L1:/l1:ro -v $D/certs:/c:ro spike-tls-squid sh -c "mkdir -p /run/l1 && python3 /l1/render.py /l1/p.json /run/l1 ssrf=1 $A && chmod -R a+rX /run/l1 && exec squid -N -f /run/l1/squid.conf" >/dev/null
  for i in $(seq 1 30); do docker logs t-squid 2>&1 | grep -q Accepting && break; /bin/sleep 0.5; done
  X=""; [ $m = tls ] && X="-proxy-tls -proxy-ca /c/ca1.pem"
  q(){ printf "  %-22s " "$1"; shift; docker exec t-client /b/snicl -proxy 198.19.0.50:3128 $X "$@" 2>&1 | tail -1 | cut -c1-120; }
  echo "$m:"; q "valid credential" -cred p1-acme.g1:acme-pw-1 -target target.spike:443 -sni target.spike
  q "wrong password" -cred p1-acme.g1:nope -target target.spike:443 -sni target.spike
  q "no credential" -cred "" -target target.spike:443 -sni target.spike
  q "unlisted host" -cred p1-acme.g1:acme-pw-1 -target other.spike:443 -sni other.spike
  q "20 x 200" -cred p1-acme.g1:acme-pw-1 -target target.spike:443 -sni target.spike -n 400 -c 20
done
docker exec t-client sh -c "echo | openssl s_client -connect 198.19.0.50:3128 -servername lenny-egress.lenny-system.svc -verify_hostname lenny-egress.lenny-system.svc -CAfile /c/ca1.pem 2>&1 | grep -E 'Verification|subject=' | tr '\n' ' '"; echo
docker rm -f t-squid >/dev/null 2>&1
