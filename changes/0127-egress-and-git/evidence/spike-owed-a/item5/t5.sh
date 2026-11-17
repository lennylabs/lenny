#!/bin/bash
. "$(dirname "$0")/lib.sh"
# Item 5 matrix: each Squid configuration, the probe's verdict, and an independent check with curl of what the configuration does.
sum(){ jq -r 'select(.verdict) | "\(.verdict) passed=\(.passed) failed=\(.failed)"' <<<"$1"; jq -r 'select(.pass==false) | "    FAIL \(.check) \(.target): \(.detail)"' <<<"$1" | cut -c1-150; }
truth(){ # truth SCHEME INSECURE
  P="--proxy $1://198.19.50.50:3128"; [ "$1" = https ] && P="--proxy https://lenny-egress.lenny-system.svc:3128 --proxy-cacert /c/ca.pem"; [ "$2" = 1 ] && P="$P --proxy-insecure"
  q(){ docker exec oa-hc curl -s -o /dev/null -k --max-time 7 $P ${1:+--proxy-user "$1"} -w '%{http_connect}' "$2"; true; }
  c0=$(docker exec oa-hc curl -s http://198.19.50.20:9300/count)
  printf '    truth: no-cred->target=%s wrong-pw->target=%s tenant->unlisted=%s tenant->private-172=%s tenant->private-10=%s tenant->metadata-name=%s tenant->loopback-name=%s\n' \
   "$(q '' https://target.oa.test/)" "$(q p1-acme.g1:nope https://target.oa.test/)" "$(q p1-acme.g1:acme-pw-1 https://other.oa.test/)" "$(q p1-acme.g1:acme-pw-1 https://rebind.oa.test/)" "$(q p1-acme.g1:acme-pw-1 https://rebind2.oa.test/)" "$(q p1-acme.g1:acme-pw-1 https://meta.oa.test/)" "$(q p1-acme.g1:acme-pw-1 https://loop.oa.test/)"
  echo "    target connections before=$c0 after=$(docker exec oa-hc curl -s http://198.19.50.20:9300/count)"
}
infra 30
for v in correct correct-n mapped-rule noauth noauth-list fakeauth refuseall refuse-hard notls wrongca noaddr addrafter droplits droplits-n droplits-rfc1918 droplits-rfc1918-n partialaddr-n; do
  echo "=== $v"; squid $v 2
  s=https; i=0; [ $v = notls ] && s=http; [ $v = wrongca ] && i=1
  a=$(probe -mode preflight); echo "  preflight: $(sum "$a")"
  a=$(probe -window 5s); echo "$a" > $D/raw/probe-$v.jsonl; echo "  full: $(sum "$a")"
  a=$(probe -window 5s -addr-control); echo "  full + address control: $(sum "$a")"
  truth $s $i
  docker logs oa-squid 2>&1 | grep -aE 'FATAL|ERROR: ' | grep -v 'cache_log' | grep -v 'accept a TLS' | head -3 | cut -c1-160
done
