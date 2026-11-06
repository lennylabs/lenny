#!/bin/sh
. /w/lib.sh
U=itsfivepastnine; P=$(utok $U); ok=0; fail=0; first_fail=""
i=15
while [ $i -le 50 ]; do
  r=$(child "$P" '["test-repo"]' '{"contents":"read"}'); tok=$(echo "$r" | jq -r '.token // empty')
  if [ -n "$tok" ]; then ok=$((ok+1)); echo "$tok" > /state/s2/c$i; echo c$i >> /state/s2/list; else fail=$((fail+1)); [ -z "$first_fail" ] && first_fail="#$i: $(echo "$r" | jq -c '{message}' | cut -c1-120)"; fi
  i=$((i+1))
done
echo "children 15-50: issued $ok, failed $fail ${first_fail:+first failure $first_fail}"
dead=0; alive=0; for c in $(cat /state/s2/list); do [ "$(valid $(cat /state/s2/$c))" = 200 ] && alive=$((alive+1)) || dead=$((dead+1)); done
echo "of all children created in S2: $alive valid, $dead invalid; parent: $(valid $P)"
echo "revoking every S2 child: $(for c in $(cat /state/s2/list); do revoke_one $(cat /state/s2/$c); done | sort | uniq -c | tr '\n' ' ')"
