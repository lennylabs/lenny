#!/bin/bash
# b0.sh: mTLS and the SAN allowlist.
. "$(dirname "$0")/b-lib.sh"
reset_store; ev_start 1 0; ev_start 2 1; /bin/sleep 1
# probes hash to their tenant's replica, so each goes to the replica that owns tprobe; ingest checks do not depend on it
"${TOOL[@]}" probe https://198.19.0.31:8443 2>&1 | cut -c1-170
for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null; done; /bin/sleep 3
echo "   stored for tprobe: $(psqld "select stream, producer, records from manifest where tenant_id in ('tprobe','tmirror','tother') order by 1")"
