#!/bin/bash
# t2.sh TAG : IPv6 reachability, dns_lookup_family variants, and the SSRF filter on v6 targets.
cd "$(dirname "$0")"; TAG=${1:-v1.40.2}
row() { r=$(./t.sh agent:good -s "$1" | grep -E "hello|=>" | tr '\n' ' '); printf "  %-34s %s\n" "$1" "$r"; }
for fam in ALL AUTO V4_PREFERRED V4_ONLY V6_ONLY; do
  python3 gen.py envoy/g-v6.json dns=dnsd family=$fam && ./envoy.sh $TAG envoy/g-v6.json >/dev/null || { echo "family $fam: envoy failed"; continue; }
  echo "## family=$fam filter=on"
  for u in "http://[2001:db8:2::20]/" "http://v6only.spike/" "http://dual.spike/" "http://target.spike/"; do row "$u"; done
done
python3 gen.py envoy/g-v6.json dns=dnsd family=ALL && ./envoy.sh $TAG envoy/g-v6.json >/dev/null
echo "## SSRF v6, family=ALL filter=on"
LL=$(docker exec r3-internal ip -6 addr show eth0 | awk '/fe80/{print $2}' | cut -d/ -f1)
for u in "http://[fd00:99::5]/" "http://ula.spike/" "http://ll.spike/" "http://[$LL]/" "http://loop6.spike:9901/" "http://mapped.spike/" "http://nat64.spike/" "http://[::ffff:198.18.0.20]/" "http://[2001:db8:1::20]/"; do row "$u"; done
sleep 1; docker logs r3-envoy 2>&1 | grep '^{' | python3 -c "
import json,sys
for l in sys.stdin:
  d=json.loads(l); print('   log %-26s %s %-3s %s up=%s' % (d['authority'],d['code'],d['flags'],d['details'],d['up_host']))"
python3 gen.py envoy/g-v6-off.json dns=dnsd family=ALL filter=off && ./envoy.sh $TAG envoy/g-v6-off.json >/dev/null
echo "## SSRF v6, family=ALL filter=OFF (control: shows the targets are reachable)"
for u in "http://[fd00:99::5]/" "http://ula.spike/" "http://mapped.spike/" "http://[::ffff:198.18.0.20]/"; do row "$u"; done
echo "## dnsd query log tail"; docker exec r3-dns wget -qO- localhost:8053/log | tail -12
