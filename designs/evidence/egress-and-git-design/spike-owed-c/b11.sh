#!/bin/bash
# b11.sh: the cursor check again, waiting for every buffer to empty before exporting, so a late flush cannot look like a missed record.
. "$(dirname "$0")/b-lib.sh"
reset_store; ev_start 1 0; ev_start 2 1; dec_start; envoy_start als; /bin/sleep 1; load -rate 2000 -for 200s | sumfmt; /bin/sleep 3
for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null; done
for i in $(seq 1 60); do a=$(ev_stats 1 | grep -o '"buffered":[0-9]*' | cut -d: -f2); b=$(ev_stats 2 | grep -o '"buffered":[0-9]*' | cut -d: -f2); [ "$a" = 0 ] && [ "$b" = 0 ] && break; /bin/sleep 2; done; echo "   buffered: $a $b after $((i*2)) s"; /bin/sleep 3
echo "   batches starting at or below an earlier batch's last, per stream: $(psqld "with x as (select stream, seq_first, lag(seq_last) over (partition by tenant_id,stream,producer,incarnation order by id) prev from manifest) select stream, count(*) from x where seq_first <= prev group by 1" | tr '\n' ' ')"
T=$(psqld "select sum(records) from manifest"); rm -f work/cursor.json work/export.txt; "${TOOL[@]}" export -max $((T/2)) | tail -1; "${TOOL[@]}" compact -par 8 | tail -1; "${TOOL[@]}" export | tail -1; "${TOOL[@]}" exportcheck | tail -6 | cut -c1-200; "${TOOL[@]}" gaps -truth http://198.19.0.11:9002/positions | tail -2 | cut -c1-220; "${TOOL[@]}" verify | tail -1 | cut -c1-260
