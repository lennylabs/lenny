#!/bin/bash
# b4.sh: a noisy tenant against quiet tenants (P7).
. "$(dirname "$0")/b-lib.sh"
res(){ for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null; done; /bin/sleep 15
  echo "   $("${TOOL[@]}" gaps -noisy t7 -truth http://198.19.0.11:9002/positions 2>&1 | tail -3 | tr '\n' ' ' | cut -c1-420)"
  d=$(dec_stats); echo "   producer: $(echo $d | python3 -c 'import sys,json; d=json.load(sys.stdin); print("emitted=%d prodDropped=%d sendFail=%d queued=%d commits p50=%.1f p99=%.1f max=%.1f ms over150=%d refused=%d" % (d["emitted"],d["prodDropped"],d["sendFail"],d["queuedBytes"],d["commitP50us"]/1000,d["commitP99us"]/1000,d["commitMaxUs"]/1000,d["commitsOverBound"],d["refused"]))')"
  for n in 1 2; do echo "   replica $n: $(ev_stats $n | python3 -c 'import sys,json; d=json.load(sys.stdin); print("records=%d droppedTenant=%d droppedGlobal=%d gapRecords=%d aggregatedIn=%d aggregatesOut=%d buffered=%d" % (d["records"],d["droppedTenant"],d["droppedGlobal"],d["gapRecords"],d["aggregatedIn"],d["aggregatesOut"],d["buffered"]))')"; done
  echo "   quiet tenants' time to durable s (p50, p99, max), noisy tenant: $(psqld "select case when tenant_id='t7' then 'noisy' else 'quiet' end, count(*), round(percentile_cont(0.5) within group (order by extract(epoch from created_at-time_first))::numeric,1), round(percentile_cont(0.99) within group (order by extract(epoch from created_at-time_first))::numeric,1), round(max(extract(epoch from created_at-time_first))::numeric,1), sum(records) from manifest group by 1" | tr '\n' ' ')"
}
s(){ L=$1; shift; echo; echo "### $L"; reset_store; ev_start 1 0; ev_start 2 1; dec_start -e REG_MOD=100; envoy_start als; /bin/sleep 1; }
B="-rate 500 -for 120s -tenants 2500"
C="oc-envoy oc-dec oc-ev1 oc-ev2 oc-pg oc-minio"
s "a. Baseline: 500/s across 2,500 tenants, 25 of them regulated, no noisy tenant"; X=$(cg $C); load $B | sumfmt; Y=$(cg $C); cgdiff "$X" "$Y" 120; res
s "b. t7 adds 3,000 denied CONNECTs/s to one host"; X=$(cg $C); load $B -noisy t7 -noisyrate 3000 | sumfmt; Y=$(cg $C); cgdiff "$X" "$Y" 120; res
s "c. t7 adds 3,000 denied CONNECTs/s, a new host each time"; X=$(cg $C); load $B -noisy t7 -noisyrate 3000 -noisyunique | sumfmt; Y=$(cg $C); cgdiff "$X" "$Y" 120; res
echo; echo "### d. As c, evidence off and no access log: what the same flood costs in Envoy and the decision service alone"; docker rm -f oc-ev1 oc-ev2 >/dev/null 2>&1; dec_start -e EVIDENCE=off; envoy_start base; /bin/sleep 1; load $B -noisy t7 -noisyrate 3000 -noisyunique | sumfmt
s "e. t7 floods the producer directly at 100,000 records/s, one host each, past every bound, beside the 500/s base load"; (/bin/sleep 5; docker exec oc-client wget -qO- 'http://198.19.0.11:9002/flood?tenant=t7&rate=100000&for=110s&unique=1' >/dev/null) & X=$(cg $C); load $B | sumfmt; Y=$(cg $C); wait; cgdiff "$X" "$Y" 120; res
s "f. Same flood, one host: aggregation applies"; (/bin/sleep 5; docker exec oc-client wget -qO- 'http://198.19.0.11:9002/flood?tenant=t7&rate=100000&for=110s' >/dev/null) & X=$(cg $C); load $B | sumfmt; Y=$(cg $C); wait; cgdiff "$X" "$Y" 120; res
