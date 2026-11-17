#!/bin/bash
# b10.sh: does the disk stall on its own? fdatasync probe on the Postgres volume, idle and beside a 2,000/s regulated run; Postgres checkpoint log.
. "$(dirname "$0")/b-lib.sh"
P=(docker run --rm --name oc-probe -v oc-pgdata:/d -v $O/bin:/b:ro alpine:3.22 /b/fsprobe /d/oc_probe)
echo "### idle, nothing else running on Docker but idle services, 120 s"; docker rm -f oc-ev1 oc-ev2 oc-dec oc-envoy >/dev/null 2>&1; "${P[@]}" 120s | tail -8
echo; echo "### beside 2,000/s regulated, 120 s"; reset_store; ev_start 1 0; ev_start 2 1; dec_start -e REG_ALL=1; envoy_start als; /bin/sleep 1
("${P[@]}" 120s | tail -14) & load -rate 2000 -for 120s | sumfmt; wait
d=$(dec_stats); echo "$d" | python3 -c 'import sys,json; d=json.load(sys.stdin); print("   commits=%d p50=%.1f p99=%.1f max=%.1f over bound=%d refused=%d (%.3f%%)" % (d["commits"],d["commitP50us"]/1000,d["commitP99us"]/1000,d["commitMaxUs"]/1000,d["commitsOverBound"],d["refused"],100*d["refused"]/max(1,d["commits"])))'
echo; echo "### Postgres checkpoints in this run"; docker logs oc-pg 2>&1 | grep -E 'checkpoint complete' | tail -4 | cut -c1-260
