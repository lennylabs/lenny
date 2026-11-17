#!/bin/bash
# i9.sh: does a gap record that lands first make a high-water cursor skip records that land later? A replica frozen 10 s, export running throughout, then export and check.
. "$(dirname "$0")/lib-b.sh"
exec >> raw/i9-highwater.txt 2>&1
b_up
for run in 1 2; do echo "--- run $run"; reset_store; ev_start 1 0 -e FLUSH=5s; ev_start 2 1 -e FLUSH=5s; dec_start; envoy_start als; /bin/sleep 1; rm -f work/consume.txt work/cursor.json work/export.txt; psqld "insert into evidence_consumer_reg(consumer) values('siem')"
(/bin/sleep 20; docker pause od-ev2 >/dev/null; /bin/sleep 10; docker unpause od-ev2 >/dev/null) &
(for i in 1 2 3 4 5; do /bin/sleep 8; TOOL export 2>&1 | tail -1 | cut -c1-110; done) &
load -rate 1000 -for 45s -tenants 300 -busy 3 -warm 0s | sumfmt; wait; wait_queue; flush_all; wait_empty; /bin/sleep 3; TOOL export 2>&1 | tail -1 | cut -c1-110
echo "   high-water cursor: $(TOOL exportcheck -in /w/export.txt 2>&1 | tail -4 | cut -c1-190)"
TOOL gaps -truth http://198.19.0.11:9002/positions 2>&1 | tail -2 | cut -c1-380
for s in egress.decision egress.tunnel egress.accesslog; do TOOL consume -urls https://198.19.0.31:8443 -consumer siem -stream $s -tenants t:0:300 -limit 5000 2>&1 | tail -1 | cut -c1-140; done; echo "   ranges cursor, every stream: $(TOOL exportcheck -in /w/consume.txt 2>&1 | tail -1 | cut -c1-190)"; done
docker rm -f od-envoy od-dec1 od-dec2 od-ev1 od-ev2 od-mc od-client od-target od-pgb od-pg od-minio >/dev/null 2>&1; docker network rm od-ev >/dev/null 2>&1; docker volume rm od-pgdata od-miniodata >/dev/null 2>&1; echo done > raw/i9-done
