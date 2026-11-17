#!/bin/bash
# Isolated samples: one stamp at a time on an otherwise quiet cluster, one probing pod at a time, 40 s window.
TAG=$1; R=spike-owed-b; cd $R; . ./kc.sh cilium
OUT=out/s3-cilium-iso-$TAG; mkdir -p $OUT
ip(){ $K -n lenny-system get pod $1 -o jsonpath='{.status.podIP}'; }
CROSS=$(ip ga-cross); SAME=$(ip ga-same)
for i in $(seq 0 5); do p=b$i; [ $((i%2)) = 0 ] && T=$CROSS || T=$SAME
  $K -n agents exec $p -- ob probe $T:3128 45s 100ms > $OUT/$p.log 2>&1 &
  sleep 3
  $K -n lenny-system exec stamper -- ob stamp agents $p lenny.dev/egress-group=group-a > $OUT/$p.stamp 2>&1
  wait
  python3 - "$OUT" $p <<'EOF'
import re,sys
d,p=sys.argv[1:]
s=open(f"{d}/{p}.stamp").read(); m=re.search(r"end=(\d+)",s); t=int(m[1])
op=[int(l.split()[0]) for l in open(f"{d}/{p}.log") if " STATE open" in l]
print(p, "open after return:", "none in window" if not op else round((op[0]-t)/1e9,2))
EOF
done
