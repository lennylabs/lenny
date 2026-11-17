#!/bin/bash
# b2.sh: on the dataset of b1b: gaps, digests, export cursors across compaction, held rows, late batches, tampering.
. "$(dirname "$0")/b-lib.sh"
for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null; done; /bin/sleep 12
for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/anchor; done
echo "   buffered after flush: $(ev_stats 1 | grep -o '"buffered":[0-9]*') $(ev_stats 2 | grep -o '"buffered":[0-9]*')"
echo "### gaps, against the producer's truth"; "${TOOL[@]}" gaps -truth http://198.19.0.11:9002/positions 2>&1 | tail -3
echo "### before compaction"; "${TOOL[@]}" verify 2>&1 | tail -3
rm -f work/cursor.json work/export.txt
TOTAL=$(psqld "select sum(records) from manifest"); echo "### export half ($((TOTAL/2)) of $TOTAL records), then compact, then resume"; "${TOOL[@]}" export -max $((TOTAL/2)) 2>&1 | tail -1
H=$(psqld "select min(hour) from manifest"); echo "   hour labels: $(psqld "select string_agg(distinct hour, ',') from manifest")"
psqlt "update manifest set held=true where tenant_id='t7'"; echo "   held rows for t7: $(psqld "select count(*) from manifest where tenant_id='t7'")"
R0=$(psqld "select count(*) from manifest"); S0=$(psqld "select pg_total_relation_size('manifest')")
echo "### compaction"; TOOLCPUS=4 "${TOOL[@]}" compact -par 8 2>&1 | tail -3
R1=$(psqld "select count(*) from manifest"); echo "   rows $R0 -> $R1, tenant-stream-hours: $(psqld "select count(*) from (select distinct tenant_id,stream,hour from manifest) x")"; echo "   held t7 rows after: $(psqld "select count(*) from manifest where tenant_id='t7' and held and not compacted")"
psqld "vacuum full manifest"; echo "   table: before $S0 bytes, after vacuum full $(psqld "select pg_total_relation_size('manifest')")"; echo "   row size: $(psqld "select 'mean row (pg_column_size)='||round(avg(pg_column_size(m.*)))||' parts='||round(avg(jsonb_array_length(parts)),1)||' max parts='||max(jsonb_array_length(parts)) from manifest m where compacted")"
psqlt "update manifest set held=false where tenant_id='t7'"
echo "### after compaction: digests, anchors"; "${TOOL[@]}" verify 2>&1 | tail -3
echo "### export resumes on stable positions"; "${TOOL[@]}" export 2>&1 | tail -1; "${TOOL[@]}" exportcheck 2>&1 | tail -1; ls -la work/cursor.json | awk '{print "   cursor file bytes:", $5}'
echo "### gaps after compaction"; "${TOOL[@]}" gaps -truth http://198.19.0.11:9002/positions 2>&1 | tail -2
echo "### a late batch into a compacted hour, a second pass"
load -rate 200 -for 20s -warm 0s | sumfmt; for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null; done; /bin/sleep 8; "${TOOL[@]}" compact -par 8 2>&1 | tail -1; echo "   rows now: $(psqld "select count(*) from manifest"), tenant-stream-hours: $(psqld "select count(*) from (select distinct tenant_id,stream,hour from manifest) x")"; for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/anchor; done; "${TOOL[@]}" verify 2>&1 | tail -2; "${TOOL[@]}" export 2>&1 | tail -1; "${TOOL[@]}" exportcheck 2>&1 | tail -1
echo "### tamper: a flipped byte in a compacted object"; "${TOOL[@]}" tamper flip; "${TOOL[@]}" verify 2>&1 | tail -3 | cut -c1-300
echo "### tamper: a compactor that drops a batch and rewrites the object and the row"; "${TOOL[@]}" tamper droppart; "${TOOL[@]}" verify 2>&1 | tail -4 | cut -c1-300
