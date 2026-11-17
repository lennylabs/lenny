#!/bin/bash
# b8.sh: why commits exceed the bound at 2,000/s; a slower WAL flush; a cursor miss in b2.
. "$(dirname "$0")/b-lib.sh"
run(){ L=$1; R=$2; S=$3; shift 3; echo; echo "### $L: $R/s for ${S}s, all regulated"
  reset_store; ev_start 1 0 "$@"; ev_start 2 1 "$@"; dec_start -e REG_ALL=1; envoy_start als; /bin/sleep 1
  ( for i in $(seq 1 $((S/2))); do /bin/sleep 2; x=$(docker exec oc-client wget -qO- http://198.19.0.11:9002/stats | grep -o '"refused":[0-9]*' | cut -d: -f2); w=$(psqld "select coalesce(wait_event_type||':'||wait_event,'cpu') , count(*) from pg_stat_activity where state='active' and pid<>pg_backend_pid() and backend_type='client backend' group by 1 order by 2 desc limit 3" | tr '\n' ' '); echo "$i refused=$x $w"; done > /tmp/oc-w.$$ ) &
  load -rate $R -for ${S}s | sumfmt; wait
  d=$(dec_stats); echo "$d" | python3 -c 'import sys,json; d=json.load(sys.stdin); print("   commits=%d p50=%.1f p95=%.1f p99=%.1f p99.9=%.1f max=%.1f ms over bound=%d refused=%d (%.3f%%)" % (d["commits"],d["commitP50us"]/1000,d["commitP95us"]/1000,d["commitP99us"]/1000,d["commitP999us"]/1000,d["commitMaxUs"]/1000,d["commitsOverBound"],d["refused"],100*d["refused"]/max(1,d["commits"])))'
  for n in 1 2; do ev_stats $n | python3 -c 'import sys,json; d=json.load(sys.stdin); print("   replica groups=%d size p50=%d p99=%d commit us p50=%d p99=%d" % (d["groups"],d["groupSizeP50"],d["groupSizeP99"],d["groupUsP50"],d["groupUsP99"]))'; done
  echo "   wait events in the seconds when refusals rose:"; awk 'BEGIN{p=0}{split($2,a,"="); if (a[2]>p) print "    ", $0; p=a[2]}' /tmp/oc-w.$$ | head -12 | cut -c1-120; rm -f /tmp/oc-w.$$
}
run "Baseline repeat" 2000 180
run "Drain off: the commit path alone" 2000 180 -e DRAIN_EVERY=1h
EVCPUS=4 run "Evidence with 4 CPUs" 2000 180
psqld "alter system set commit_delay=3000"; psqld "alter system set commit_siblings=0"; psqld "select pg_reload_conf()" >/dev/null
run "A slower WAL flush, emulated with commit_delay=3 ms (a stand-in for a networked disk)" 2000 180
run "commit_delay=3 ms, 500/s" 500 120
psqld "alter system reset commit_delay"; psqld "alter system reset commit_siblings"; psqld "select pg_reload_conf()" >/dev/null
echo; echo "### Cursor miss: is a sequence out of order across batches?"
reset_store; ev_start 1 0 -e FLUSH=5s; ev_start 2 1 -e FLUSH=5s; dec_start; envoy_start als; /bin/sleep 1; load -rate 2000 -for 60s | sumfmt; /bin/sleep 3; for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null; done; /bin/sleep 8
echo "   batches whose first sequence is at or below an earlier batch's last, per stream: $(psqld "with x as (select stream, seq_first, lag(seq_last) over (partition by tenant_id,stream,producer,incarnation order by id) prev from manifest) select stream, count(*) from x where seq_first <= prev group by 1" | tr '\n' ' ')"
rm -f work/cursor.json work/export.txt; "${TOOL[@]}" export | tail -1; "${TOOL[@]}" exportcheck | tail -6 | cut -c1-200; "${TOOL[@]}" gaps | tail -1 | cut -c1-200
echo; echo "### SIGTERM with intake stopped first (the b5 run's handler kept accepting, so it never emptied and docker killed it at 30 s), restart at +15 s: 2,000/s, 120 s"
reset_store; ev_start 1 0; ev_start 2 1; dec_start -e REG_MOD=100; envoy_start als; /bin/sleep 1
(/bin/sleep 70; S=$(date +%s); docker stop -t 30 oc-ev2 >/dev/null; echo "   stop took $(( $(date +%s)-S )) s; $(docker logs oc-ev2 2>&1 | tail -1 | cut -c1-100)"; /bin/sleep 3; ev_start 2 1) &
load -rate 2000 -for 120s -warm 0s | sumfmt; wait; for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null; done; /bin/sleep 15; "${TOOL[@]}" gaps -truth http://198.19.0.11:9002/positions 2>&1 | tail -2 | cut -c1-260; d=$(dec_stats); echo "   producer: emitted=$(echo $d | grep -o '"emitted":[0-9]*') dropped=$(echo $d | grep -o '"prodDropped":[0-9]*') sendFail=$(echo $d | grep -o '"sendFail":[0-9]*') refused=$(echo $d | grep -o '"refused":[0-9]*')"; echo "   writers: $(psqld "select writer, closed_at is not null from evidence_writers" | tr '\n' ' ')"
