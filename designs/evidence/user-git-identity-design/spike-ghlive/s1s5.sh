#!/bin/sh
. /w/lib.sh
U=lennylabsdev
P=$(utok $U)
echo "parent valid: $(valid $P)  parent expires_in at issue: $(jq -r .expires_in /state/$U.tok)s"
c1=$(child "$P" '["test-repo"]' '{"contents":"read"}'); C1=$(echo "$c1" | jq -r .token)
c2=$(child "$P" '["test-repo"]' '{"contents":"read"}'); C2=$(echo "$c2" | jq -r .token)
echo "child1: $(echo "$c1" | jq -c '{issued: (.token != null), expires_at, permissions, repos: [.repositories[]?.full_name], message}')"
echo "child2: $(echo "$c2" | jq -c '{issued: (.token != null), expires_at}')"
echo "now: $(date -u +%FT%TZ)"
echo "child1 same as parent: $([ "$C1" = "$P" ] && echo yes || echo no); child1 same as child2: $([ "$C1" = "$C2" ] && echo yes || echo no)"
echo "== S5: revoke child1"
echo "revoke child1 status: $(revoke_one $C1)"
sleep 2
echo "after revoke: child1 $(valid $C1)  child2 $(valid $C2)  parent $(valid $P)"
echo "== S1: refresh the parent"
echo "refresh: $(refresh $U)"
P2=$(utok $U)
sleep 2
echo "after refresh: old parent $(valid $P)  new parent $(valid $P2)  child2 $(valid $C2)"
echo "child2 still reads test-repo contents: $(api $C2 GET /repos/lenny-test/test-repo/contents/ | cut -c1-3)"
echo "$C2" > /state/s1.child2; chmod 600 /state/s1.child2
