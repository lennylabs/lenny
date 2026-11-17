#!/bin/bash
# b1.sh: storage character, CONNECT latency with evidence off and on, ingest cost, row and object counts.
. "$(dirname "$0")/b-lib.sh"
echo "### pg_test_fsync on the Postgres volume (characterizes the disk under every commit number below)"
docker exec oc-pg pg_test_fsync -s 2 -f /var/lib/postgresql/data/oc_fsync_test 2>&1 | grep -E 'fdatasync|fsync  |open_datasync' | head -6
echo; echo "### B1a. Evidence off: no writer, no access log. 2,000 CONNECTs/s, 2,500 tenants (5 busy with half the rate), 5% denied, 300 ms hold, 120 s"
docker rm -f oc-ev1 oc-ev2 >/dev/null 2>&1; dec_start -e EVIDENCE=off; envoy_start base
B=$(cg oc-envoy oc-dec oc-client); (/bin/sleep 60; echo "   sockets at 60 s: $(sock)") & 
load -rate 2000 -for 120s | sumfmt; wait; A=$(cg oc-envoy oc-dec oc-client); cgdiff "$B" "$A" 120
echo; echo "### B1a2. Evidence on, no direct Envoy log (dec access log only), 120 s, to separate egress.accesslog's cost"
reset_store; ev_start 1 0 -e ANCHOR=60s; ev_start 2 1 -e ANCHOR=60s; dec_start; envoy_start als1; /bin/sleep 1; B=$(cg oc-envoy oc-dec oc-ev1 oc-ev2); load -rate 2000 -for 120s | sumfmt; A=$(cg oc-envoy oc-dec oc-ev1 oc-ev2); cgdiff "$B" "$A" 120; /bin/sleep 3
echo; echo "### B1b. Evidence on, the Envoy access log direct as well (revision 37), two replicas, FLUSH=60s, same load, 330 s"
reset_store; ev_start 1 0 -e ANCHOR=60s; ev_start 2 1 -e ANCHOR=60s; dec_start; envoy_start als; /bin/sleep 1
L="oc-envoy oc-dec oc-ev1 oc-ev2 oc-pg oc-pgb oc-minio oc-client"; B=$(cg $L); T0=$(date +%s)
(/bin/sleep 60; echo "   sockets at 60 s: $(sock)"; /bin/sleep 120; echo "   sockets at 180 s: $(sock)") &
(for i in 1 2 3 4 5 6; do /bin/sleep 55; echo "   t=$(( $(date +%s)-T0 ))s manifest: $(psqld "select 'rows='||count(*)||' tenants='||count(distinct tenant_id)||' records='||coalesce(sum(records),0)||' raw='||coalesce(sum(raw_bytes),0)||' stored='||coalesce(sum(bytes),0) from manifest")"; done) &
load -rate 2000 -for 330s | sumfmt; A=$(cg $L); wait; cgdiff "$B" "$A" 330
echo "   decision service: $(dec_stats)"; echo "   replica 0: $(ev_stats 1)"; echo "   replica 1: $(ev_stats 2)"
echo "   rows per stream: $(psqld "select stream, count(*), sum(records), round(avg(records)), pg_size_pretty(sum(bytes)), round(100.0*sum(bytes)/sum(raw_bytes),1), round(1.0*sum(raw_bytes)/sum(records)) from manifest group by 1")"
echo "   time to durable (manifest created_at minus the batch's first record), small tenants and busy tenants:"
psqld "select case when tenant_id in ('t0','t1','t2','t3','t4') then 'busy' else 'small' end c, stream, count(*), round(percentile_cont(0.5) within group (order by extract(epoch from created_at-time_first))::numeric,1), round(percentile_cont(0.99) within group (order by extract(epoch from created_at-time_first))::numeric,1), round(max(extract(epoch from created_at-time_first))::numeric,1) from manifest group by 1,2 order by 1,2" | sed 's/^/     /'
echo "   rows inserted per minute in the steady window (t=120..300 s):"
psqld "with w as (select min(created_at) m from manifest) select round(count(*)/3.0) from manifest, w where created_at between w.m + interval '60 s' and w.m + interval '240 s'"
echo "   table sizes: $(psqld "select 'manifest='||pg_size_pretty(pg_total_relation_size('manifest'))||' chain='||pg_size_pretty(pg_total_relation_size('audit_chain'))||' chain rows='||(select count(*) from audit_chain)")"
echo "   objects in the bucket: $(docker exec oc-mc sh -c 'mc ls -r m/evidence | wc -l')"
