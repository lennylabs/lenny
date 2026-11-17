#!/bin/sh
# Usage: run.sh ENVOY_TAG MSD
set -e
W=$(cd "$(dirname "$0")" && pwd); TAG=$1; MSD=${2:-5}; MODE=${3:-http}
docker rm -f oc-envoy >/dev/null 2>&1 || true
docker run --rm -v $W:/w:ro python:3.13-alpine python -I /w/gen.py /dev/stdout $MSD $MODE > $W/envoy/oc4-$MODE-msd$MSD.json
docker run -d --name oc-envoy --network oc-net --ip 198.20.0.10 -v $W/envoy:/envoy:ro -v $W/certs:/certs:ro envoyproxy/envoy:$TAG -c /envoy/oc4-$MODE-msd$MSD.json --log-level warn >/dev/null
for i in $(seq 1 30); do docker exec oc-envoy sh -c 'exit 0' 2>/dev/null && break; sleep 0.5; done
sleep 2
docker logs oc-envoy 2>&1 | grep -i -E "error|critical" | head -5 || true
docker run --rm --network oc-net -v $W:/w:ro python:3.13-alpine python -I /w/client.py 198.20.0.10 ok nogrant wrongsan foreign deny503 deny403 noagent grant407 gw403 tsm plain plainport tick
docker logs oc-envoy 2>&1 | grep '^{' > $W/raw/envoy-access-$MODE-$TAG.jsonl || true
