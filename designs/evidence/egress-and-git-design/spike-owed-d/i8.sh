#!/bin/bash
# i8.sh: reruns after prototype and harness corrections, and checks the first pass left out.
. "$(dirname "$0")/lib-b.sh"
exec >> raw/i8-reruns.txt 2>&1
res(){ flush_all; wait_queue; /bin/sleep 3; flush_all; wait_empty; /bin/sleep 3
  TOOL gaps -truth http://198.19.0.11:9002/positions -show 4 2>&1 | tail -7 | cut -c1-420
  destat; for n in 1 2; do evstat $n; done; echo "   stored records: $(psqld "select coalesce(sum(records),0) from manifest") in $(psqld "select count(*) from manifest") rows"
  echo "   incarnation rows: $(psqld "select replica, closed_at is not null from evidence_writers order by started_at" | tr '\n' ' ')"; timeline | sed 's/^/   /' | cut -c1-170; }
echo "=== S3 again: the drain bound no longer fires after a replica has returned, and a refused commit during a drain is named. SIGTERM at t=70 s, restart at t=80 s"
reset_store; ev_start 1 0; ev_start 2 1; dec_start -e REG_MOD=100; envoy_start als; /bin/sleep 1
(/bin/sleep 70; k=$(now); echo "   SIGTERM at $k"; docker stop -t 30 od-ev2 >/dev/null; docker logs od-ev2 2>&1 | tail -1 | cut -c1-120; /bin/sleep 3; ev_start 2 1) &
load -rate 2000 -for 120s -warm 0s | sumfmt; wait; /bin/sleep 35; res; TOOL windows 2>&1 | cut -c1-220 | head -8
echo; echo "=== S4 again: frozen 30 s, with the producer naming every refusal it cannot settle (UNSETTLED_GAP=1)"
reset_store; ev_start 1 0; ev_start 2 1; dec_start -e REG_MOD=100 -e UNSETTLED_GAP=1; envoy_start als; /bin/sleep 1
(/bin/sleep 60; echo "   frozen at $(now)"; docker pause od-ev2 >/dev/null; /bin/sleep 30; docker unpause od-ev2 >/dev/null; echo "   released at $(now)") &
load -rate 2000 -for 120s -warm 0s | sumfmt; wait; res
echo; echo "=== S5 again, reading the audit only after every buffer has emptied"
for cfg in "REHASH=0 REPORT=0|40" "|40" "|90"; do E=${cfg%%|*}; R=${cfg##*|}; EF=(); for x in $E; do EF+=(-e $x); done
  echo "--- ${E:-revision 42} restart after ${R} s"; reset_store; ev_start 1 0; ev_start 2 1; dec_start "${EF[@]}"; envoy_start als; /bin/sleep 1
  (/bin/sleep 30; echo "   killed at $(now)"; docker kill od-ev2 >/dev/null; /bin/sleep $R; ev_start 2 1; echo "   restarted at $(now)") &
  load -rate 2000 -for $((30+R+15))s -warm 0s | sumfmt; wait; flush_all; wait_queue; /bin/sleep 2; flush_all; wait_empty; /bin/sleep 3; destat; TOOL gaps -truth http://198.19.0.11:9002/positions 2>&1 | tail -2 | cut -c1-400
done
echo; echo "=== 4f again: a busy tenant whose allows interleave with denials (the first pass's tenant variable was overwritten, so that case had no interleaving)"
fl(){ L=$1; NT=$2; shift 2; echo; echo "### $L"; reset_store; ev_start 1 0; ev_start 2 1; dec_start; envoy_start als; /bin/sleep 1; rm -f work/dids.txt
  load -rate 500 -for 90s -dids /w/dids.txt -did-every 20 -noisy $NT "$@" | sumfmt; /bin/sleep 2; wait_queue; flush_all; wait_empty; /bin/sleep 2; destat; for n in 1 2; do evstat $n; done
  TOOL gaps -truth http://198.19.0.11:9002/positions -stream egress.decision -noisy $NT 2>&1 | tail -3 | cut -c1-430
  echo "   noisy tenant stored: $(psqld "select count(*)||' rows, '||sum(records)||' records, '||pg_size_pretty(sum(bytes))||' stored' from manifest where tenant_id='$NT' and stream='egress.decision'")"; echo "   sampled identifiers: $(wc -l < work/dids.txt)"
  TOOL lookup -file /w/dids.txt -max 400 2>&1 | tail -1 | cut -c1-300; }
fl "t0 (a busy tenant, about 50 allows/s) with 1,000 denied/s of one host, beside 500/s" t0 -noisyrate 1000
echo; echo "### 100,000 records/s through the producer's flood path, for 45 s, beside 500/s: new hosts, then one host"
for u in "&unique=1" ""; do reset_store; ev_start 1 0; ev_start 2 1; dec_start; envoy_start als; /bin/sleep 1; echo "--- flood${u:+ of new hosts}${u:-, one host}"; (/bin/sleep 12; docker exec od-client wget -qO- "http://198.19.0.11:9002/flood?tenant=t7&rate=100000&for=45s$u" >/dev/null) & load -rate 500 -for 70s | sumfmt; wait; /bin/sleep 2; wait_queue; flush_all; wait_empty; /bin/sleep 2; destat; for n in 1 2; do evstat $n; done; TOOL gaps -truth http://198.19.0.11:9002/positions -stream egress.decision -noisy t7 2>&1 | tail -3 | cut -c1-430; echo "   noisy stored: $(psqld "select count(*)||' rows, '||sum(records)||' records, '||pg_size_pretty(sum(bytes)) from manifest where tenant_id='t7' and stream='egress.decision'")"; done
echo; echo "=== 5a again: rolling restarts, the high-water cursor against the ranges cursor, with gap records on a cursor of their own"
reset_store; ev_start 1 0 -e FLUSH=5s; ev_start 2 1 -e FLUSH=5s; dec_start; envoy_start als; /bin/sleep 1; rm -f work/consume.txt work/cursor.json work/export.txt; psqld "insert into evidence_consumer_reg(consumer) values('siem')"
(/bin/sleep 20; docker stop -t 30 od-ev2 >/dev/null; ev_start 2 1 -e FLUSH=5s; /bin/sleep 12; docker stop -t 30 od-ev1 > /dev/null; ev_start 1 0 -e FLUSH=5s) &
(for i in 1 2 3 4 5 6; do /bin/sleep 8; TOOL export 2>&1 | tail -1 | cut -c1-110; done) &
load -rate 1000 -for 55s -tenants 300 -busy 3 -warm 0s | sumfmt; wait; wait_queue; flush_all; wait_empty; /bin/sleep 3; TOOL export 2>&1 | tail -1 | cut -c1-110
echo "   high-water cursor: $(TOOL exportcheck -in /w/export.txt 2>&1 | tail -6 | cut -c1-190)"; echo "   batches starting at or below an earlier batch's last, per stream: $(psqld "with x as (select stream, seq_first, lag(seq_last) over (partition by tenant_id,stream,producer,incarnation order by id) prev from manifest) select stream, count(*) from x where seq_first <= prev group by 1" | tr '\n' ' ')"
rm -f work/consume.txt; TOOL consume -urls https://198.19.0.31:8443,https://198.19.0.32:8443 -consumer siem -stream egress.decision -tenants t:0:300 -limit 5000 2>&1 | tail -1 | cut -c1-160; echo "   ranges cursor through the service: $(TOOL exportcheck -in /w/consume.txt -stream egress.decision 2>&1 | tail -4 | cut -c1-200)"; timeline | sed 's/^/   /' | cut -c1-150 | head -8; TOOL gaps -truth http://198.19.0.11:9002/positions -stream egress.decision 2>&1 | tail -2 | cut -c1-360
echo; echo "=== 5a with a frozen replica: false gap records, then the same consumer"
reset_store; ev_start 1 0 -e FLUSH=5s; ev_start 2 1 -e FLUSH=5s; dec_start; envoy_start als; /bin/sleep 1; rm -f work/consume.txt; psqld "insert into evidence_consumer_reg(consumer) values('siem')"
(/bin/sleep 20; docker pause od-ev2 >/dev/null; /bin/sleep 10; docker unpause od-ev2 >/dev/null) & load -rate 1000 -for 45s -tenants 300 -busy 3 -warm 0s | sumfmt; wait; wait_queue; flush_all; wait_empty; /bin/sleep 3; TOOL consume -urls https://198.19.0.31:8443 -consumer siem -stream egress.decision -tenants t:0:300 -limit 5000 2>&1 | tail -1 | cut -c1-160; echo "   ranges cursor: $(TOOL exportcheck -in /w/consume.txt -stream egress.decision 2>&1 | tail -3 | cut -c1-200)"; TOOL gaps -truth http://198.19.0.11:9002/positions -stream egress.decision 2>&1 | tail -2 | cut -c1-360
echo; echo "=== 5b again: retention and anchors. Interval anchors exist before retention runs"
H(){ docker exec od-pg psql -q -tA -U postgres -c "begin; select set_config('app.current_tenant','__all__',true); update manifest set held=$2 where tenant_id='$1'; commit;" >/dev/null; }
for tomb in false true; do reset_store; ev_start 1 0 -e FLUSH=3s; ev_start 2 1 -e FLUSH=3s; dec_start; envoy_start als; /bin/sleep 1; load -rate 300 -for 20s -tenants 30 -busy 2 -warm 0s | sumfmt; wait_queue; flush_all; wait_empty; /bin/sleep 6; for n in 1 2; do docker exec od-client wget -qO- http://198.19.0.3$n:9100/anchor >/dev/null; done; echo "--- chain event for retention: $tomb"; TOOL verify 2>&1 | tail -1 | cut -c1-280; TOOL retain -stream egress.decision -age 3s -bound 5s -tombstone=$tomb -honour-consumers=false 2>&1 | tail -1 | cut -c1-200; TOOL verify 2>&1 | tail -1 | cut -c1-280; done
echo "--- a hold set while retention runs"; reset_store; ev_start 1 0 -e FLUSH=1s; ev_start 2 1 -e FLUSH=1s; dec_start; envoy_start als; /bin/sleep 1; load -rate 1000 -for 25s -tenants 400 -busy 2 -warm 0s | sumfmt; wait_queue; flush_all; wait_empty; /bin/sleep 4
(for i in $(seq 1 30); do H t$((RANDOM%400)) true; /bin/sleep 0.05; done) & TOOL retain -stream egress.decision -age 3s -bound 5s -honour-consumers=false 2>&1 | tail -8 | cut -c1-200; wait; echo "   held decision rows left: $(psqld "select count(*) from manifest where stream='egress.decision' and held"), unheld: $(psqld "select count(*) from manifest where stream='egress.decision' and not held")"; TOOL verify 2>&1 | tail -1 | cut -c1-280
echo; echo "=== 5d again: the generator keeps its connections, and the tool has the port range"
ALL=$(docker exec od-py python3 -c 'print(",".join("t%d"%i for i in range(2500)))')
C="od-envoy od-dec1 od-ev1 od-ev2 od-pg od-minio"
reset_store; MIRRORS=$ALL ev_start 1 0; MIRRORS=$ALL ev_start 2 1; dec_start -e REG_MOD=100; envoy_start als; /bin/sleep 1; B=$(cg $C); echo "--- baseline again, 180 s"; load -rate 2000 -for 180s | sumfmt; A=$(cg $C); cgdiff "$B" "$A" 180
reset_store; MIRRORS=$ALL ev_start 1 0; MIRRORS=$ALL ev_start 2 1; dec_start -e REG_MOD=100; envoy_start als; /bin/sleep 1; B=$(cg $C); echo "--- with egress.local 2,000/s, git.operation 200/s, and credential.mint 40/s"
(TOOLCPUS=2 TOOL gen2 local -rate 2000 -for 180s 2>&1 | tail -1 | cut -c1-200) & (TOOLCPUS=1 TOOL gen2 gitop -rate 200 -for 180s 2>&1 | tail -1 | cut -c1-200) & (TOOLCPUS=1 TOOL gen2 mint -rate 40 -for 180s 2>&1 | tail -1 | cut -c1-200) &
load -rate 2000 -for 180s | sumfmt; wait; A=$(cg $C); cgdiff "$B" "$A" 180; wait_queue; flush_all; wait_empty; /bin/sleep 3; destat; evstat 1; evstat 2; echo "   local reports refused: $(dec_stats | grep -o '"localRefused":[0-9]*') accepted: $(dec_stats | grep -o '"local":[0-9]*')"
TOOL gaps -truth http://198.19.0.11:9002/positions 2>&1 | tail -2 | cut -c1-420; psqld "select stream, count(*), sum(records), pg_size_pretty(sum(bytes)), round(100.0*sum(bytes)/sum(raw_bytes),1), round(1.0*sum(raw_bytes)/sum(records)) from manifest group by 1 order by 1"
echo "   forged tenant rows: $(psqld "select count(*) from manifest where tenant_id='forged-tenant'")"; for s in egress.local git.operation credential.mint; do echo "   sample $s: $(TOOL sample $s 2>&1 | head -1 | cut -c1-420)"; done
echo; echo "=== 5e again: a harder attempt at numbering outside the send order: few tenants, 400 decisions/s each, 1 s batches, three runs"
for run in 1 2 3; do reset_store; ev_start 1 0 -e FLUSH=1s; ev_start 2 1 -e FLUSH=1s; dec_start -e SEQ_ATOMIC=0 -e SEND_EVERY=10ms; envoy_start als; /bin/sleep 1; rm -f work/export.txt work/cursor.json; load -rate 2000 -for 60s -tenants 6 -busy 5 -busyshare 1 -deny 0 -warm 0s | sumfmt; wait_queue; flush_all; wait_empty; /bin/sleep 3; echo "   batches starting at or below an earlier batch's last, per stream: $(psqld "with x as (select stream, seq_first, lag(seq_last) over (partition by tenant_id,stream,producer,incarnation order by id) prev from manifest) select stream, count(*) from x where seq_first <= prev group by 1" | tr '\n' ' ') of $(psqld "select count(*) from manifest") rows"; TOOL export 2>&1 | tail -1 | cut -c1-110; echo "   high-water: $(TOOL exportcheck 2>&1 | tail -3 | cut -c1-170)"; done
echo done > raw/i8-done
