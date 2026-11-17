#!/bin/bash
cd "$(dirname "$0")"; D=$PWD
cpu(){ docker exec "$1" sh -c 'grep usage_usec /sys/fs/cgroup/cpu.stat | cut -d" " -f2'; }
for lim in 1 0.25; do for m in plain tls; do
  docker rm -f t-i7s t-i7c >/dev/null 2>&1
  A=""; [ $m = tls ] && A="-cert /c/good/tls.crt -key /c/good/tls.key"; C=""; [ $m = tls ] && C="-ca /c/ca1.pem"
  docker run -d --name t-i7s --cpus $lim --network stls-pub --ip 198.19.0.70 --ulimit nofile=65536:65536 -v $D/bin:/b:ro -v $D/certs:/c:ro alpine:3.22 sh -c "while :; do /b/i7 serve $A; sleep 0.2; done" >/dev/null; /bin/sleep 1
  docker run -d --name t-i7c --network stls-pub --ip 198.19.0.71 --ulimit nofile=65536:65536 -v $D/bin:/b:ro -v $D/certs:/c:ro alpine:3.22 /b/i7 subs -addr 198.19.0.70:7443 $C -n 2000 -rate 500 -dur 50s >/dev/null
  /bin/sleep 12
  docker logs t-i7c 2>&1 | tail -1 | grep -q 'live=2000' || echo "  not all live before restart: $(docker logs t-i7c 2>&1 | tail -1)"
  K=$(docker logs t-i7c 2>&1 | tail -1 | sed -E 's/t= *([0-9]+)s.*/\1/'); PID=$(docker exec t-i7s sh -c 'pidof i7')
  C0=$(cpu t-i7s); docker exec t-i7s sh -c "kill -9 $PID"; /bin/sleep 25; C1=$(cpu t-i7s)
  docker logs t-i7c 2>&1 | python3 -c "
import re,sys
rows=[(int(a),int(b),int(c)) for a,b,c in re.findall(r't= *(\d+)s live=(\d+) starts=\d+ retries=(\d+)',sys.stdin.read())]
K=$K; after=[r for r in rows if r[0]>K]
low=min(r[1] for r in after); rec=next((r[0]-K for r in after if r[1]==2000 and any(x[1]<2000 and x[0]<r[0] for x in after)),None)
print('cpus=$lim $m killed pid $PID at t=%d: lowest live=%d, all 2000 back after %ss, retries=%d, reconnect cpu=%.1fs'%(K,low,rec,after[-1][2],($C1-$C0)/1e6))"
done; done
docker rm -f t-i7s t-i7c >/dev/null 2>&1
