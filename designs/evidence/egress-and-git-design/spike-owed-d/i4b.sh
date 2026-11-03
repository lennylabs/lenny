#!/bin/bash
# i4b.sh: one transaction per tenant, netagent's single retry, aggregates with lookup, per-session sequences, key invalidation, and the compacted row.
. "$(dirname "$0")/lib-b.sh"
exec >> raw/i4b-rest.txt 2>&1
echo "### pg_test_fsync on this volume"; docker exec od-pg pg_test_fsync -s 2 -f /var/lib/postgresql/data/od_fsync_test 2>&1 | grep -E 'fdatasync|fsync  |open_datasync' | head -5
reg(){ L=$1; R=$2; S=$3; shift 3; echo; echo "### $L: $R decisions/s for ${S}s, all tenants regulated, 2,500 tenants"
  reset_store; ev_start 1 0 "$@"; ev_start 2 1 "$@"; dec_start -e REG_ALL=1; envoy_start als; /bin/sleep 1
  C="od-envoy od-dec1 od-ev1 od-ev2 od-pg od-pgb"; B=$(cg $C)
  (for i in $(seq 1 $((S/30))); do /bin/sleep 30; x=$(dec_stats); docker exec od-client wget -qO- http://198.19.0.11:9002/reset >/dev/null; echo "   window $((i*30))s: $(echo "$x" | docker exec -i od-py python3 -c 'import sys,json; d=json.load(sys.stdin); print("commits=%d p50=%.1f p99=%.1f p99.9=%.1f max=%.1f ms refused=%d" % (d["commits"],d["commitP50us"]/1000,d["commitP99us"]/1000,d["commitP999us"]/1000,d["commitMaxUs"]/1000,d["refused"]))')"; done) &
  load -rate $R -for ${S}s | sumfmt; wait; A=$(cg $C); cgdiff "$B" "$A" $S; for n in 1 2; do evstat $n; done; }
echo; echo "=== 4d. One transaction per tenant"
reg "One transaction per tenant, 500/s" 500 180
reg "One transaction per tenant, 2,000/s" 2000 180
reg "Earlier single transaction under the sentinel, 2,000/s" 2000 120 -e WALMODE=sentinel
reg "One transaction per tenant, also for the manifest, 2,000/s" 2000 120 -e MANIFEST_MODE=pertenant
echo; echo "### Manifest writes per tenant, no regulated tenants, 2,000/s, 150 s: the sentinel, then one transaction per tenant"
for M in "" pertenant; do reset_store; ev_start 1 0 -e MANIFEST_MODE=$M; ev_start 2 1 -e MANIFEST_MODE=$M; dec_start; envoy_start als; /bin/sleep 1; C="od-pg od-pgb od-ev1 od-ev2"; B=$(cg $C); load -rate 2000 -for 150s | sumfmt; A=$(cg $C); cgdiff "$B" "$A" 150; for n in 1 2; do evstat $n; done; echo "   manifest rows $(psqld "select count(*) from manifest") mode=${M:-sentinel}"; done
echo; echo "=== 4e. netagent's single retry on the distinct 503 reason"
retry(){ L=$1; PAUSE=$2; EVERY=$3; shift 3; echo; echo "### $L: 2,000/s, all regulated, 120 s, stall of the database ${PAUSE:-none}"
  reset_store; ev_start 1 0; ev_start 2 1; dec_start -e REG_ALL=1; envoy_start als; /bin/sleep 1; B=$(cg od-pg od-pgb od-ev1 od-ev2 od-dec1)
  if [ -n "$PAUSE" ]; then (for i in $(seq 1 $((110/EVERY))); do /bin/sleep $EVERY; docker pause od-pg >/dev/null; /bin/sleep $PAUSE; docker unpause od-pg >/dev/null; done) & fi
  (for i in 1 2 3 4; do /bin/sleep 30; x=$(dec_stats); docker exec od-client wget -qO- http://198.19.0.11:9002/reset >/dev/null; echo "   window $((i*30))s: $(echo "$x" | docker exec -i od-py python3 -c 'import sys,json; d=json.load(sys.stdin); print("decisions so far=%d commits=%d p50=%.1f p99=%.1f max=%.1f refused=%d" % (d["decisions"],d["commits"],d["commitP50us"]/1000,d["commitP99us"]/1000,d["commitMaxUs"]/1000,d["refused"]))')"; done) &
  load -rate 2000 -for 120s "$@" | sumfmt; wait; A=$(cg od-pg od-pgb od-ev1 od-ev2 od-dec1); cgdiff "$B" "$A" 120; dec_stats | grep -o '"decisions":[0-9]*'; for n in 1 2; do evstat $n; done; }
for P in "" 0.4 2; do
  retry "No retry" "$P" 15
  retry "Retry after 100 ms" "$P" 15 -retry503 -retry-delay 100ms
  retry "Retry after 500 ms" "$P" 15 -retry503 -retry-delay 500ms
  retry "Retry after 200 ms with up to 300 ms of jitter" "$P" 15 -retry503 -retry-delay 200ms -retry-jitter 300ms
done
echo; echo "=== 4f. Aggregates over contiguous ranges, and a decision identifier resolving during a flood"
flood(){ L=$1; N=$2; shift 2; echo; echo "### $L"; reset_store; ev_start 1 0; ev_start 2 1; dec_start; envoy_start als; /bin/sleep 1; rm -f work/dids.txt
  load -rate 500 -for 90s -dids /w/dids.txt -did-every 20 -noisy $N "$@" | sumfmt; /bin/sleep 2; wait_queue; flush_all; wait_empty; /bin/sleep 2; destat; for n in 1 2; do evstat $n; done
  TOOL gaps -truth http://198.19.0.11:9002/positions -stream egress.decision -noisy $N 2>&1 | tail -3 | cut -c1-430
  echo "   noisy tenant stored: $(psqld "select count(*)||' rows, '||sum(records)||' records, '||pg_size_pretty(sum(bytes))||' stored' from manifest where tenant_id='$N' and stream='egress.decision'")"; echo "   sampled identifiers: $(wc -l < work/dids.txt)"
  TOOL lookup -file /w/dids.txt -max 400 2>&1 | tail -1 | cut -c1-300; TOOL compact -par 8 2>&1 | tail -1 | cut -c1-150; TOOL lookup -file /w/dids.txt -max 200 2>&1 | tail -1 | sed 's/^/   after compaction: /' | cut -c1-300; }
flood "3,000 denied/s, one host, beside 500/s" t7 -noisyrate 3000
flood "3,000 denied/s, two hosts alternating" t7 -noisyrate 3000 -noisyalt 2
flood "3,000 denied/s, a new host each" t7 -noisyrate 3000 -noisyunique
flood "One busy tenant whose allows interleave with 1,000 denied/s of one host" t0 -noisyrate 1000
echo; echo "=== 4g. Per-session sequences"
ps(){ L=$1; shift; echo; echo "### $L"; reset_store; ev_start 1 0 -e CHUNK=1 "$@"; ev_start 2 1 -e CHUNK=1 "$@"; dec_start "$@"; /bin/sleep 1
  A(){ docker exec od-client wget -qO- "http://198.19.0.11:9002/activity?tenant=tq&n=$1&session=$2" >/dev/null; /bin/sleep 0.5; flush_all; /bin/sleep 3; }
  A 2000 s3,s4; A 2000 s3,s4
  for n in 1 2; do docker exec od-client wget -qO- "http://198.19.0.3$n:9100/dropchunk?tenant=tq&session=s3" >/dev/null; done; A 2000 s3,s4; A 2000 s3,s4; wait_queue; flush_all; wait_empty
  TOOL erase 2>&1 | grep -E 'chunk session' | head -8; TOOL gaps -truth http://198.19.0.11:9002/positions -stream agent.activity -show 4 2>&1 | tail -6 | cut -c1-400; }
SPS=0 ps "A single sequence for the tenant (the earlier layout): one chunk of s3 lost" -e SEQ_PER_SESSION=0
SPS=1 ps "A sequence per session (revision 42): one chunk of s3 lost" -e SEQ_PER_SESSION=1
echo; echo "=== 4h. Cached key invalidation on destruction"
E(){ TOOL erase 2>&1 | tail -1 | cut -c1-260; }
keyrun(){ L=$1; shift; echo; echo "### $L"; reset_store; ev_start 1 0 "$@"; ev_start 2 1 "$@"; /bin/sleep 1.5
  P(){ TOOL actpost -url https://198.19.0.3$1:8443 -tenant tk -session s1 -n $2 -from $3 -inc act$1; }
  P 1 100 1; P 2 100 1; flush_all; /bin/sleep 3; echo "   before destruction: $(E)"
  t0=$(now); echo "   destroy through replica 0: $(docker exec od-client wget -qO- 'http://198.19.0.31:9100/keys/destroy?tenant=tk&session=s1')"
  seq=101; for i in $(seq 1 ${LOOPS:-3}); do P 1 10 $seq; P 2 10 $seq; seq=$((seq+10)); /bin/sleep 1; flush_all; done; wait_empty; /bin/sleep 1.5
  echo "   keys: $(psqld "select gen, key is null, destroyed_at is not null from evidence_session_keys order by gen" | tr '\n' ' ')"; echo "   after destruction, $(( $(printf %.0f $(now)) - $(printf %.0f $t0) )) s later: $(E)"; for n in 1 2; do ev_stats $n | grep -o '"keysCreated":[0-9]*'; done; }
keyrun "Per-record encryption, push to the other replica"
LOOPS=14 keyrun "Per-record encryption, no push, cached key TTL 10 s: the other replica's lag" -e KEY_PUSH=0 -e KEY_TTL=10s
chunkrun(){ L=$1; shift; echo; echo "### $L"; reset_store; ev_start 1 0 -e CHUNK=1 "$@"; ev_start 2 1 -e CHUNK=1 "$@"; /bin/sleep 1.5
  TOOL actpost -url https://198.19.0.31:8443 -tenant tk -session s1 -n 500 -from 1 -inc act1; flush_all; /bin/sleep 3; TOOL actpost -url https://198.19.0.31:8443 -tenant tk -session s1 -n 300 -from 501 -inc act1
  echo "   destroy with 300 payloads still buffered in plaintext: $(docker exec od-client wget -qO- 'http://198.19.0.31:9100/keys/destroy?tenant=tk&session=s1')"; flush_all; wait_empty; /bin/sleep 2; echo "   $(E)"; TOOL gaps -stream agent.activity 2>&1 | tail -2 | cut -c1-300; }
chunkrun "Chunks, encrypted at flush: buffered payloads at destruction"
chunkrun "Chunks, with the buffer purged on destruction (not in the text)" -e PURGE_ON_DESTROY=1
echo; echo "=== 4i. The compacted row"
rowrun(){ L=$1; shift; echo; echo "### $L"; reset_store; ( export "$@"; TOOL gen -hour 2026110300 -tenants 50 -busy 5 -rate 200 -secs 3600 -compacted=false -par 6 2>&1 | tail -1 | cut -c1-200
  TOOL compact -par 8 2>&1 | tail -1 | cut -c1-200; TOOL sizes 2>&1 | tail -1
  TOOL verify 2>&1 | tail -1 | cut -c1-330; rm -f work/cursor.json work/export.txt; TOOL export 2>&1 | tail -1 | cut -c1-200 ; rm -f work/export.txt; echo "   export again, nothing new: $(TOOL export 2>&1 | tail -1 | cut -c1-160)"
  psqld "vacuum full manifest" ; psqld "vacuum full audit_chain"; TOOL sizes 2>&1 | tail -1
  TOOL tamper flip 2>&1 | tail -1 | cut -c1-120; TOOL verify 2>&1 | grep -E 'MISMATCH|verify:' | tail -2 | cut -c1-260 ); }
rowrun "The list in the row, anchors naming every batch (round 11)" PARTS_IN_ROW=1 ANCHOR_DETAIL=list
rowrun "Row holds the digest, list in the object's index, anchors naming every batch (the text)" PARTS_IN_ROW=0 ANCHOR_DETAIL=list
rowrun "Row holds the digest, list in the index, compaction anchors hold a digest only" PARTS_IN_ROW=0 ANCHOR_DETAIL=digest
echo; echo "### A compromised compactor that drops one batch and writes a consistent object, index, row, and anchor, on data from running ingest"
drop(){ L=$1; AB=$2; AD=$3; echo "--- $L"; reset_store; ev_start 1 0 -e ANCHOR_BATCHES=$AB -e FLUSH=10s; ev_start 2 1 -e ANCHOR_BATCHES=$AB -e FLUSH=10s; dec_start; envoy_start als; /bin/sleep 1; load -rate 300 -for 70s -tenants 40 -busy 5 -warm 0s | sumfmt; wait_queue; flush_all; wait_empty; for n in 1 2; do docker exec od-client wget -qO- http://198.19.0.3$n:9100/anchor >/dev/null; done; ANCHOR_DETAIL=$AD DROP_ONE=1 TOOL compact -par 8 2>&1 | tail -1 | cut -c1-140; TOOL verify 2>&1 | tail -1 | cut -c1-330; echo "   anchor-related lines: $(TOOL verify 2>&1 | grep -c -E 'ANCHOR')"; TOOL gaps -truth http://198.19.0.11:9002/positions 2>&1 | tail -2 | head -1 | cut -c1-330; TOOL sizes 2>&1 | tail -1; }
drop "interval anchors name batches, compaction anchors name batches" 1 list
drop "interval anchors name batches, compaction anchors hold a digest" 1 digest
drop "interval anchors hold a digest only, compaction anchors hold a digest" 0 digest
echo done > raw/i4b-done
