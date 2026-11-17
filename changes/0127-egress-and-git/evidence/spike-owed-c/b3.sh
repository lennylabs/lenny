#!/bin/bash
# b3.sh: the regulated path: group commit through PgBouncer in transaction mode, against a 150 ms bound.
. "$(dirname "$0")/b-lib.sh"
pgbstat(){ docker exec oc-pgb sh -c 'PGPASSWORD=p psql -h 127.0.0.1 -p 5432 -U lenny_evidence pgbouncer -tA -F" " -c "SHOW STATS"' 2>&1 | grep -v pgbouncer | cut -d' ' -f1-16 | head -2; }
run(){ # label rate secs env...
  L=$1; R=$2; S=$3; shift 3; echo; echo "### $L: $R decisions/s for ${S}s, all tenants regulated, 2,500 tenants"
  reset_store; ev_start 1 0 "$@"; ev_start 2 1 "$@"; dec_start -e REG_ALL=1; envoy_start als; /bin/sleep 1
  C="oc-envoy oc-dec oc-ev1 oc-ev2 oc-pg oc-pgb oc-minio"; B=$(cg $C)
  (for i in $(seq 1 $((S/30))); do /bin/sleep 30; x=$(dec_stats); docker exec oc-client wget -qO- http://198.19.0.11:9002/reset >/dev/null; echo "   window $((i*30))s: $(echo "$x" | python3 -c 'import sys,json; d=json.load(sys.stdin); print("commits=%d p50=%.1f p95=%.1f p99=%.1f p99.9=%.1f max=%.1f ms over150=%d refused=%d" % (d["commits"],d["commitP50us"]/1000,d["commitP95us"]/1000,d["commitP99us"]/1000,d["commitP999us"]/1000,d["commitMaxUs"]/1000,d["commitsOverBound"],d["refused"]))') wal rows=$(psqld 'select count(*) from evidence_wal')"; done) &
  load -rate $R -for ${S}s | sumfmt; wait; A=$(cg $C); cgdiff "$B" "$A" $S
  x=$(ev_stats 1); y=$(ev_stats 2); echo "$x" | python3 -c 'import sys,json; d=json.load(sys.stdin); print("   replica 0 groups=%d size p50=%d p99=%d commit us p50=%d p99=%d walOK=%d walFail=%d drained=%d batches=%d" % (d["groups"],d["groupSizeP50"],d["groupSizeP99"],d["groupUsP50"],d["groupUsP99"],d["walOK"],d["walFail"],d["drained"],d["batches"]))'; echo "$y" | python3 -c 'import sys,json; d=json.load(sys.stdin); print("   replica 1 groups=%d size p50=%d p99=%d commit us p50=%d p99=%d walOK=%d walFail=%d drained=%d batches=%d" % (d["groups"],d["groupSizeP50"],d["groupSizeP99"],d["groupUsP50"],d["groupUsP99"],d["walOK"],d["walFail"],d["drained"],d["batches"]))'
  echo "   pgbouncer SHOW STATS (database total_xact total_query recv sent xact_time query_time wait_time ...): $(pgbstat | head -1)"
  echo "   checkpoints: $(psqld "select 'timed='||num_timed||' req='||num_requested||' write_ms='||write_time||' sync_ms='||sync_time from pg_stat_checkpointer")"
}
run "Through PgBouncer, 500/s" 500 180
run "Through PgBouncer, 2,000/s" 2000 390
echo; EVPG=postgres://lenny_evidence:p@198.19.0.51:5432/postgres run "Direct to Postgres, no pooler, 2,000/s (the pooler's share)" 2000 120
run "Through PgBouncer, 2,000/s, one transaction per tenant with SET LOCAL semantics (RLS with a concrete tenant)" 2000 90 -e WALMODE=pertenant
run "Through PgBouncer, 500/s, one transaction per tenant" 500 90 -e WALMODE=pertenant
echo; echo "### Fail closed: 500/s, the pooler paused for 5 s at t=40; allowed against stored"
reset_store; ev_start 1 0; ev_start 2 1; dec_start -e REG_ALL=1; envoy_start als; /bin/sleep 1
(/bin/sleep 40; docker pause oc-pgb >/dev/null; /bin/sleep 5; docker unpause oc-pgb >/dev/null) &
load -rate 500 -for 90s -warm 0s | sumfmt; wait; /bin/sleep 8; for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null; done; /bin/sleep 8
d=$(dec_stats); echo "   commitOK=$(echo $d | grep -o '"commitOK":[0-9]*') refused=$(echo $d | grep -o '"refused":[0-9]*') stored decisions in manifest+wal: $(psqld "select (select coalesce(sum(records),0) from manifest where stream='egress.decision') , (select count(*) from evidence_wal)")"
echo; echo "### Fail closed: 500/s, one replica killed at t=30 and restarted at t=40; allowed against stored"
reset_store; ev_start 1 0; ev_start 2 1; dec_start -e REG_ALL=1; envoy_start als; /bin/sleep 1
(/bin/sleep 30; docker kill oc-ev2 >/dev/null; /bin/sleep 10; ev_start 2 1) &
load -rate 500 -for 80s -warm 0s | sumfmt; wait; /bin/sleep 8; for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null; done; /bin/sleep 8
d=$(dec_stats); echo "   commitOK=$(echo $d | grep -o '"commitOK":[0-9]*') refused=$(echo $d | grep -o '"refused":[0-9]*') stored decisions in manifest+wal: $(psqld "select (select coalesce(sum(records),0) from manifest where stream='egress.decision'), (select count(*) from evidence_wal)")"
"${TOOL[@]}" gaps -truth http://198.19.0.11:9002/positions 2>&1 | tail -2
