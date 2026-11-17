#!/bin/bash
# b5.sh: a replica killed mid-load, and what a consumer can detect.
. "$(dirname "$0")/b-lib.sh"
res(){ for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null; done; /bin/sleep 15; "${TOOL[@]}" gaps -per-tenant 3 -truth http://198.19.0.11:9002/positions 2>&1 | tail -6 | cut -c1-300
  d=$(dec_stats); echo "   producer: $(echo $d | python3 -c 'import sys,json; d=json.load(sys.stdin); print("emitted=%d prodDropped=%d sendFail=%d queued=%d commitOK=%d refused=%d" % (d["emitted"],d["prodDropped"],d["sendFail"],d["queuedBytes"],d["commitOK"],d["refused"]))')"; echo "   stored records: $(psqld "select coalesce(sum(records),0) from manifest") in $(psqld "select count(*) from manifest") rows; writers: $(psqld "select string_agg(distinct writer, ',') from manifest")"; echo "   writer incarnations (table added by this prototype): $(psqld "select writer, closed_at is not null from evidence_writers" | tr '\n' ' ')"; }
echo "### SIGKILL of one replica at t=120, restart at t=140, load continues to t=240: 2,000/s, 2,500 tenants, 25 regulated"
reset_store; ev_start 1 0; ev_start 2 1; dec_start -e REG_MOD=100; envoy_start als; /bin/sleep 1
(/bin/sleep 120; b=$(ev_stats 2); echo "   replica 1 just before the kill: $(echo $b | grep -o '"buffered":[0-9]*') $(echo $b | grep -o '"records":[0-9]*')"; docker kill oc-ev2 >/dev/null; /bin/sleep 20; ev_start 2 1) &
load -rate 2000 -for 240s -warm 0s | sumfmt; wait; res
echo; echo "### SIGKILL at t=70, the load ends 5 s later, restart afterwards: the tail has no later record to expose it"
reset_store; ev_start 1 0; ev_start 2 1; dec_start -e REG_MOD=100; envoy_start als; /bin/sleep 1
(/bin/sleep 70; docker kill oc-ev2 >/dev/null; /bin/sleep 15; ev_start 2 1) &
load -rate 2000 -for 75s -warm 0s | sumfmt; wait; /bin/sleep 8; res
echo; echo "### SIGTERM (graceful) at t=70, restart at t=80, load to t=120"
reset_store; ev_start 1 0; ev_start 2 1; dec_start -e REG_MOD=100; envoy_start als; /bin/sleep 1
(/bin/sleep 70; docker stop -t 30 oc-ev2 >/dev/null; docker logs oc-ev2 2>&1 | tail -1 | cut -c1-100; /bin/sleep 3; ev_start 2 1) &
load -rate 2000 -for 120s -warm 0s | sumfmt; wait; res
