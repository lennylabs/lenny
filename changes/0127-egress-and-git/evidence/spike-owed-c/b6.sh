#!/bin/bash
# b6.sh: per-session keys through payloadKeyRef; erasure by destroying a key.
. "$(dirname "$0")/b-lib.sh"
reset_store; ev_start 1 0; ev_start 2 1; dec_start; /bin/sleep 1
act(){ docker exec oc-client wget -qO- "http://198.19.0.11:9002/activity?tenant=$1&session=$2&n=$3" | tr -d '\n'; echo; }
act tA s1 2000; act tA s2 2000; act tB s9 1000; /bin/sleep 2
for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null; done; /bin/sleep 3; act tA s1 500; /bin/sleep 2; for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null; done; /bin/sleep 3; for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/anchor; done
echo "   compression of the encrypted stream: $(psqld "select stream, sum(records), round(1.0*sum(raw_bytes)/sum(records)), round(100.0*sum(bytes)/sum(raw_bytes),1)||'%' from manifest group by 1")"
docker exec oc-mc sh -c 'mc ls -r m/evidence | head -2' | cut -c1-120
"${TOOL[@]}" erase; "${TOOL[@]}" verify | tail -1
"${TOOL[@]}" compact -par 4 | tail -1; "${TOOL[@]}" verify | tail -1; "${TOOL[@]}" erase
echo "### destroy session s1's key"; "${TOOL[@]}" erase -destroy -tenant tA -session s1; "${TOOL[@]}" erase; "${TOOL[@]}" verify | tail -1
echo "### new records for s1 after destruction, with ingest still running: it holds a cached key"
act tA s1 100; /bin/sleep 1; for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null; done; /bin/sleep 3; "${TOOL[@]}" erase

echo; echo "### sequenceEnd: chunk mode (CHUNK=1): one session's payloads compress, then encrypt, per batch"
reset_store; ev_start 1 0 -e CHUNK=1; ev_start 2 1 -e CHUNK=1; dec_start; /bin/sleep 1
act tA s1 2000; act tA s2 2000; act tB s9 1000; /bin/sleep 2
for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null; done; /bin/sleep 3; for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/anchor; done
echo "   stored bytes per 5,000 payloads: $(psqld "select stream, sum(records), sum(raw_bytes), sum(bytes), round(100.0*sum(bytes)/sum(raw_bytes),1)||'%' from manifest group by 1")"
"${TOOL[@]}" erase -destroy -tenant tA -session s1; "${TOOL[@]}" erase; "${TOOL[@]}" verify | tail -1; "${TOOL[@]}" compact -par 4 | tail -1; "${TOOL[@]}" verify | tail -1; "${TOOL[@]}" gaps | tail -1
echo; echo "### sequenceEnd with two sessions interleaved in one tenant's sequence: ranges overlap"
reset_store; ev_start 1 0 -e CHUNK=1; ev_start 2 1 -e CHUNK=1; dec_start; /bin/sleep 1
act tA s3,s4 2000; /bin/sleep 2; for n in 1 2; do docker exec oc-client wget -qO- http://198.19.0.3$n:9100/flush >/dev/null; done; /bin/sleep 3; "${TOOL[@]}" erase
