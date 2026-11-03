#!/bin/bash
# b9.sh: scan rerun after fixing the prefilter (the first prefilter runs in b7 searched for a trailing comma that this field does not have, so they found nothing).
. "$(dirname "$0")/b-lib.sh"
H=2026110300; Q="-hour $H -addr 203.0.113.7 -port 43210 -target 192.0.2.77 -from 2026-11-03T00:30:00Z -to 2026-11-03T00:31:00Z"
echo "### compacted hour: address + port + target + time"
for P in 1 4 8; do TOOLCPUS=$P "${TOOL[@]}" scan $Q -par $P 2>&1 | tail -2 | cut -c1-260; done
echo "### compacted hour: port only"; TOOLCPUS=4 "${TOOL[@]}" scan -hour $H -port 43210 -par 4 -join=false 2>&1 | tail -1 | cut -c1-260
H2=2026110301; Q2="-hour $H2 -addr 203.0.113.7 -port 43210 -target 192.0.2.77 -from 2026-11-03T01:30:00Z -to 2026-11-03T01:31:00Z"
echo; echo "### a second hour, uncompacted (one object per tenant-stream-minute)"
TOOLCPUS=8 "${TOOL[@]}" gen -hour $H2 -tenants 2500 -rate 2000 -compacted=false -par 8 2>&1 | tail -1
for P in 1 4 8; do TOOLCPUS=$P "${TOOL[@]}" scan $Q2 -par $P 2>&1 | tail -2 | cut -c1-260; done
TOOLCPUS=4 "${TOOL[@]}" scan $Q2 -par 4 -prefilter=false -join=false 2>&1 | tail -1 | cut -c1-260
