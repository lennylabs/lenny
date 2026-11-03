#!/bin/bash
# ssrf.sh <envoy-tag> <filter on|off> [family] : send verbatim CONNECTs for each SSRF
# target and print Envoy's answer plus the access-log details and upstream host.
cd "$(dirname "$0")"
TAG=$1; F=$2; FAM=${3:-ALL}
python3 envoy/render.py envoy/ssrf.yaml filter=$F family=$FAM && ./envoy.sh $TAG envoy/ssrf.yaml >/dev/null || exit 1
targets=(target.spike:443 example.com:443 evil.spike:9901 localtest.me:9901 meta.spike:80 internal.spike:80
  169.254.169.254:80 10.0.0.1:443 10.99.0.5:80 127.0.0.1:9901 0.0.0.0:9901 "[::1]:9901" "[::]:9901"
  "[::ffff:127.0.0.1]:9901" "[::ffff:10.99.0.5]:80" "[::ffff:a63:5]:80" "[fe80::1]:80" 2130706433:9901 0x7f000001:9901 127.1:9901 0177.0.0.1:9901
  localhost:9901 "[64:ff9b::a63:5]:80")
for t in "${targets[@]}"; do
  r=$(./tun.sh -target "$t" -max 1500ms 2>&1 | head -1)
  printf "%-26s %s\n" "$t" "$r"
done
sleep 5
echo "--- access log (authority code flags details upstream_host)"
docker logs se-envoy 2>&1 | grep '^{' | python3 -c "
import json,sys
for l in sys.stdin:
  d=json.loads(l); print('%-26s %s %-3s %s up=%s %s' % (d['authority'],d['code'],d['flags'],d['details'],d['up_host'],d['up_fail'] or ''))"
