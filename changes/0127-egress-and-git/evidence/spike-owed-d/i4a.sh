#!/bin/bash
# i4a.sh: replica loss. The durable-sequence report and the gap record, replica incarnation rows, and the producer's rehash.
. "$(dirname "$0")/lib-b.sh"
exec >> raw/i4a-replica-loss.txt 2>&1
res(){ flush_all; wait_queue; /bin/sleep 3; flush_all; wait_empty; /bin/sleep 3
  TOOL gaps -truth http://198.19.0.11:9002/positions -show 3 2>&1 | tail -6 | cut -c1-420
  destat; for n in 1 2; do evstat $n; done; echo "   stored records: $(psqld "select coalesce(sum(records),0) from manifest") in $(psqld "select count(*) from manifest") rows"
  echo "   incarnation rows: $(psqld "select replica, closed_at is not null from evidence_writers order by started_at" | tr '\n' ' ')"; timeline | sed 's/^/   /' | cut -c1-170; }
# S1: the kill just before the load ends, so a lost tail has no later record to reveal it
s1(){ L=$1; shift; echo; echo "### S1 [$L]: SIGKILL at t=70 s, the load ends 5 s later, restart at t=85 s. 2,000/s, 2,500 tenants, 25 regulated"
  reset_store; ev_start 1 0; ev_start 2 1; dec_start -e REG_MOD=100 "$@"; envoy_start als; /bin/sleep 1
  (/bin/sleep 70; k=$(now); b=$(ev_stats 2); echo "   killed at $k: $(echo $b | grep -o '"buffered":[0-9]*') $(echo $b | grep -o '"records":[0-9]*')"; docker kill od-ev2 >/dev/null; /bin/sleep 15; ev_start 2 1) &
  load -rate 2000 -for 75s -warm 0s | sumfmt; wait; /bin/sleep 8; res; }
s1 "earlier behaviour: no report, no rehash" -e REHASH=0 -e REPORT=0
s1 "revision 42"
TOOL windows 2>&1 | cut -c1-200 | head -30
s1 "revision 42, second run"
TOOL windows 2>&1 | cut -c1-200 | head -30
echo; echo "### S2 [revision 42]: SIGKILL at t=120 s, restart at t=140 s, load to t=240 s"
reset_store; ev_start 1 0; ev_start 2 1; dec_start -e REG_MOD=100; envoy_start als; /bin/sleep 1
(/bin/sleep 120; k=$(now); b=$(ev_stats 2); echo "   killed at $k: $(echo $b | grep -o '"buffered":[0-9]*')"; docker kill od-ev2 >/dev/null; /bin/sleep 20; ev_start 2 1) &
load -rate 2000 -for 240s -warm 0s | sumfmt; wait; res; TOOL windows 2>&1 | cut -c1-200 | head -40
echo; echo "### S3 [revision 42]: SIGTERM at t=70 s, restart at t=80 s, load to t=120 s"
reset_store; ev_start 1 0; ev_start 2 1; dec_start -e REG_MOD=100; envoy_start als; /bin/sleep 1
(/bin/sleep 70; k=$(now); echo "   SIGTERM at $k"; docker stop -t 30 od-ev2 >/dev/null; docker logs od-ev2 2>&1 | tail -1 | cut -c1-120; /bin/sleep 3; ev_start 2 1) &
load -rate 2000 -for 120s -warm 0s | sumfmt; wait; res; TOOL windows 2>&1 | cut -c1-200 | head -12
echo; echo "### S4 [revision 42]: a replica frozen for 30 s at t=60 s, then released: the producer has written it off, and its buffers flush"
reset_store; ev_start 1 0; ev_start 2 1; dec_start -e REG_MOD=100; envoy_start als; /bin/sleep 1
(/bin/sleep 60; k=$(now); echo "   frozen at $k"; docker pause od-ev2 >/dev/null; /bin/sleep 30; docker unpause od-ev2 >/dev/null; echo "   released at $(now)") &
load -rate 2000 -for 120s -warm 0s | sumfmt; wait; res
echo; echo "### S5: restart against the 8 MiB buffer, 2,000/s, no regulated tenants, SIGKILL at t=30 s"
for cfg in "REHASH=0 REPORT=0|20" "REHASH=0 REPORT=0|40" "REHASH=0 REPORT=0|90" "|40" "|90"; do E=${cfg%%|*}; R=${cfg##*|}; EF=(); for x in $E; do EF+=(-e $x); done
  echo "--- ${E:-revision 42} restart after ${R} s"; reset_store; ev_start 1 0; ev_start 2 1; dec_start "${EF[@]}"; envoy_start als; /bin/sleep 1
  (/bin/sleep 30; k=$(now); echo "   killed at $k"; docker kill od-ev2 >/dev/null; /bin/sleep $R; ev_start 2 1; echo "   restarted at $(now)") &
  (/bin/sleep $((30+R/2)); echo "   queued at mid-outage: $(dec_stats | grep -o '"queuedBytes":[0-9]*') $(dec_stats | grep -o '"queuedTenants":[0-9]*')") &
  load -rate 2000 -for $((30+R+15))s -warm 0s | sumfmt; wait; flush_all; wait_queue; /bin/sleep 2; destat; timeline | sed 's/^/   /' | cut -c1-170; TOOL gaps -truth http://198.19.0.11:9002/positions 2>&1 | tail -2 | cut -c1-360
done
echo done > raw/i4a-done
