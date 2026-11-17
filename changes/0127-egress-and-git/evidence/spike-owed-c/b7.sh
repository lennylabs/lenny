#!/bin/bash
# b7.sh: a busy hour at realistic size: scan cost uncompacted and compacted, compaction time, manifest size.
. "$(dirname "$0")/b-lib.sh"
H=2026110300
docker rm -f oc-ev1 oc-ev2 oc-dec oc-envoy >/dev/null 2>&1; reset_store
echo "### generate: 2,000 CONNECTs/s for an hour, 2,500 tenants, three records per tunnel, one batch per tenant-stream-minute, the running system's payload code"
TOOLCPUS=8 "${TOOL[@]}" gen -hour $H -tenants 2500 -rate 2000 -compacted=false -par 8 2>&1 | tail -1
echo "   rows=$(psqld "select count(*) from manifest") table=$(psqld "select pg_size_pretty(pg_total_relation_size('manifest'))")  objects=$(docker exec oc-mc sh -c 'mc ls -r m/evidence | wc -l')  store=$(docker exec oc-mc sh -c 'mc du m/evidence' | cut -c1-40)"
Q="-hour $H -addr 203.0.113.7 -port 43210 -target 192.0.2.77 -from 2026-11-03T00:30:00Z -to 2026-11-03T00:31:00Z"
echo; echo "### scan, uncompacted hour, address + port + target + time"
for P in 1 4 8; do TOOLCPUS=$P "${TOOL[@]}" scan $Q -par $P 2>&1 | tail -3 | cut -c1-260; done
TOOLCPUS=4 "${TOOL[@]}" scan $Q -par 4 -prefilter=false -join=false 2>&1 | tail -1 | cut -c1-260
echo; echo "### compaction of the busy hour"; TOOLCPUS=8 "${TOOL[@]}" compact -hour $H -par 8 2>&1 | tail -2
echo "   rows=$(psqld "select count(*) from manifest") after vacuum: $(psqld "vacuum full manifest" ; psqld "select pg_size_pretty(pg_total_relation_size('manifest'))")  $(psqld "select 'mean row='||round(avg(pg_column_size(m.*)))||' bytes, parts jsonb text='||round(avg(length(parts::text)))||' bytes, parts='||round(avg(jsonb_array_length(parts)),1) from manifest m where compacted")  objects=$(docker exec oc-mc sh -c 'mc ls -r m/evidence | wc -l')"
echo "   chain events: $(psqld "select kind, count(*), pg_size_pretty(sum(pg_column_size(detail))::bigint) from audit_chain group by 1")"
echo "   object sizes: $(psqld "select 'stream '||stream||' p50='||pg_size_pretty(percentile_cont(0.5) within group (order by bytes)::bigint)||' max='||pg_size_pretty(max(bytes)) from manifest group by stream")"
echo; echo "### verify after compaction"; TOOLCPUS=8 "${TOOL[@]}" verify -par 8 2>&1 | tail -1 | cut -c1-300
echo; echo "### scan, compacted hour"
for P in 1 4 8; do TOOLCPUS=$P "${TOOL[@]}" scan $Q -par $P 2>&1 | tail -3 | cut -c1-260; done
TOOLCPUS=4 "${TOOL[@]}" scan $Q -par 4 -prefilter=false -join=false 2>&1 | tail -1 | cut -c1-260
echo; echo "### the same query with only a port: reused ports"; TOOLCPUS=4 "${TOOL[@]}" scan -hour $H -port 43210 -par 4 -join=false 2>&1 | tail -1 | cut -c1-260
