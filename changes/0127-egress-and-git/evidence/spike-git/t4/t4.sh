#!/bin/sh
# Test 4: sideband progress/keepalive latency through net/http/cgi+http-backend vs direct --stateless-rpc.
cd /spike/t4; chmod +x hook-*.sh
rm -rf /srv/git/org/small.git; git clone -q --bare /srv/git/org/repo.git /srv/git/org/small.git 2>/dev/null
# make the served repo small: drop the big blobs history by building a fresh one
rm -rf /srv/git/org/small.git /tmp/sm; git init -q -b main /tmp/sm; for i in 1 2 3; do echo $i > /tmp/sm/f; git -C /tmp/sm add f; git -C /tmp/sm -c user.name=a -c user.email=a@a commit -qm $i; done
git clone -q --bare /tmp/sm /srv/git/org/small.git
for hook in quiet progress; do
 printf '[uploadpack]\n    packObjectsHook = /spike/t4/hook-%s.sh\n    keepAlive = 2\n' $hook > /tmp/srv-$hook.gc
 for mode in cgi direct; do
  pkill -f bin/upstream; sleep 0.3
  GIT_CONFIG_GLOBAL=/tmp/srv-$hook.gc HOME=/tmp /spike/t3/bin/upstream -mode $mode 2>/dev/null & sleep 0.4
  rm -rf /tmp/c4; start=$(date +%s.%N)
  HOME=/tmp/h4 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TRACE_PACKET=/tmp/pt.$hook.$mode \
    git -c protocol.version=2 clone -q --progress http://alice:s3cret@127.0.0.1:8080/org/small.git /tmp/c4 2>/dev/null
  echo "### hook=$hook upstream=$mode  (seconds since clone start at which sideband packets arrived)"
  python3 - /tmp/pt.$hook.$mode $start <<'P'
import sys,re
start=float(sys.argv[2]); import datetime
out=[]
for l in open(sys.argv[1]):
    m=re.match(r'(\d+):(\d+):(\d+\.\d+) .*packet: +(\S+)< (\\[12])(.*)',l)
    if not m: continue
    h,mi,s=int(m[1]),int(m[2]),float(m[3]); t=h*3600+mi*60+s
    d=datetime.datetime.fromtimestamp(start, datetime.timezone.utc); t0=d.hour*3600+d.minute*60+d.second+d.microsecond/1e6
    kind = "keepalive" if m[5]=="\\1" and m[6].strip()=="" else ("progress" if m[5]=="\\2" else "data")
    if kind!="data" or not out or out[-1][1]!="data": out.append((round(t-t0,2),kind,m[6][:30]))
for o in out[:14]: print("  ",o)
P
 done
done
pkill -f bin/upstream
