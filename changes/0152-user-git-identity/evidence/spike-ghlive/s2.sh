#!/bin/sh
. /w/lib.sh
U=itsfivepastnine; P=$(utok $U)
echo "parent valid at start: $(valid $P)"
mkdir -p /state/s2; : > /state/s2/list
echo "== create 14 children with identical scope, checking every earlier child after each creation"
i=1
while [ $i -le 14 ]; do
  hdrs=/tmp/h$i; r=$(jq -n --arg at "$P" '{access_token: $at, target: "lenny-test", repositories: ["test-repo"], permissions: {contents: "read"}}' | curl -s -D $hdrs -X POST -u "$GITHUB_APP_CLIENT_ID:$GITHUB_APP_CLIENT_SECRET" -H 'Accept: application/vnd.github+json' -d @- $API/applications/$GITHUB_APP_CLIENT_ID/token/scoped)
  code=$(head -1 $hdrs | awk '{print $2}'); tok=$(echo "$r" | jq -r '.token // empty')
  rl=$(grep -i '^x-ratelimit-remaining\|^x-ratelimit-resource' $hdrs | tr -d '\r' | tr '\n' ' ')
  if [ -n "$tok" ]; then echo "$tok" > /state/s2/c$i; echo c$i >> /state/s2/list; fi
  alive=""; for c in $(cat /state/s2/list); do alive="$alive $c:$(valid $(cat /state/s2/$c))"; done
  echo "create #$i: http $code issued=$([ -n "$tok" ] && echo yes || echo no) msg=$(echo "$r" | jq -r '.message // ""' | cut -c1-80) [$rl] alive:$alive parent:$(valid $P)"
  i=$((i+1))
done
echo "== refresh the parent three times"
for j in 1 2 3; do echo "refresh $j: $(refresh $U)"; done
P=$(utok $U); echo "parent valid at end: $(valid $P)"
alive=""; for c in $(cat /state/s2/list); do alive="$alive $c:$(valid $(cat /state/s2/$c))"; done; echo "children at end:$alive"
