#!/bin/bash
# a3.sh: long and streaming responses through termination with scrubbing on.
. "$(dirname "$0")/a2lib.sh"
sse(){ L=$1; K=$2; U=$3; echo "--- $L"; docker rm -f oc-nl >/dev/null 2>&1; "${NL[@]}" sse -kind $K -url "$U" 2>&1 | cut -c1-130 | tail -${TL:-3}; docker rm -f oc-nl >/dev/null 2>&1; }
./a-srv.sh -quiet
echo "### baseline: an opaque tunnel, no scrubbing"; ./a-na.sh >/dev/null
sse "other host, opaque, 12 events, 500 ms apart" h1 "https://other.example.test/stream?n=12&gap=500ms&size=40"
for W in fixed prefix; do
 echo; echo "### window=$W"; ./a-na.sh -- -window=$W >/dev/null
 TL=9 sse "16-byte secret, h1, 12 events, 500 ms apart, 40-byte filler" h1 "https://api.example.test/stream?n=12&gap=500ms&size=40"
 sse "16-byte secret, h2, same" h2 "https://api.example.test/stream?n=12&gap=500ms&size=40"
 sse "99-byte secret, h2, token-sized events: 200 events 50 ms apart" h2 "https://llm.example.test/stream?n=200&gap=50ms&size=10"
 sse "99-byte secret, h1, token-sized events" h1 "https://llm.example.test/stream?n=200&gap=50ms&size=10"
 TL=8 sse "16-byte secret, h2, a 10 s pause before the last event" h2 "https://api.example.test/stream?n=5&gap=200ms&size=40&stall=10s"
 sse "16-byte secret, h1, reflected secret in every event" h1 "https://api.example.test/stream?n=12&gap=200ms&size=20&reflect=1"
 sse "16-byte secret, h2, reflected, event written in 5-byte flushed chunks" h2 "https://api.example.test/stream?n=12&gap=200ms&size=20&reflect=1&chunk=5"
 sse "99-byte secret, h2, reflected, 7-byte chunks" h2 "https://llm.example.test/stream?n=12&gap=200ms&size=20&reflect=1&chunk=7"
 NAG=1 sse "gzip from upstream with a flush per event, h1 (netagent decodes, scrubs, and re-frames)" h1 "https://api.example.test/stream?n=12&gap=500ms&size=40&gz=1"
 sse "gzip from upstream, h2, reflected" h2 "https://api.example.test/stream?n=12&gap=500ms&size=40&gz=1&reflect=1"
done
echo; echo "### CPU limits: five concurrent streams at 20 events/s, prefix window, plus one bulk download at 50m"
for CP in 0.05 0.25 1; do ./a-na.sh --cpus $CP >/dev/null
 for i in 1 2 3 4; do docker run -d --name oc-nl$i --network container:oc-na -v $O/bin:/b:ro -v $O/certs:/certs:ro -v $O/run/netagent:/run/netagent:ro alpine:3.22 /b/naload sse -kind h2 -url "https://llm.example.test/stream?n=400&gap=50ms&size=30" >/dev/null; done
 docker rm -f oc-nl >/dev/null 2>&1; r=$("${NL[@]}" sse -kind h2 -url "https://llm.example.test/stream?n=400&gap=50ms&size=30" 2>&1 | tail -1); echo "limit=$CP: $r"; for i in 1 2 3 4; do docker rm -f oc-nl$i >/dev/null; done; docker rm -f oc-nl >/dev/null
 docker run -d --name oc-nlb --network container:oc-na -v $O/bin:/b:ro -v $O/certs:/certs:ro -v $O/run/netagent:/run/netagent:ro alpine:3.22 /b/naload bulk -kind h1 -url "https://other.example.test/bulk?mb=204800" -for 25s >/dev/null
 r=$("${NL[@]}" sse -kind h2 -url "https://llm.example.test/stream?n=400&gap=50ms&size=30" 2>&1 | tail -1); echo "limit=$CP with a bulk download beside it: $r"; docker logs oc-nlb 2>&1 | tail -1; docker rm -f oc-nlb oc-nl >/dev/null
done
echo; echo "### a five-minute stream through termination, prefix window, reflection every 100th event, memory over time"; ./a-na.sh >/dev/null
docker run -d --name oc-nl --network container:oc-na -v $O/bin:/b:ro -v $O/certs:/certs:ro -v $O/run/netagent:/run/netagent:ro alpine:3.22 /b/naload sse -kind h2 -url "https://llm.example.test/stream?n=6000&gap=50ms&size=200&reflect=100" >/dev/null
for t in 0 60 120 180 240 290; do /bin/sleep $([ $t = 0 ] && echo 5 || echo 58); echo "t~${t}s: $(memr | python3 -c 'import sys,json; x=sys.stdin.read().split(" ",5); g=json.loads(x[5]); print("current %.1f anon %.1f MiB goroutines %d scrubHits %d" % (int(x[0])/2**20,int(x[2])/2**20,g["goroutines"],g["scrubHits"]))')"; done
/bin/sleep 15; docker logs oc-nl 2>&1 | tail -1 | cut -c1-130; docker rm -f oc-nl >/dev/null
