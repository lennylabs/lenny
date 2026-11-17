#!/bin/bash
# a2cpu.sh: CPU per GiB and throughput at CPU limits. netagent, client, and Envoy are separate cgroups; only netagent's is limited.
. "$(dirname "$0")/a2lib.sh"
bulk(){ # label kind url cpus [netagent flags] [conc]
  L=$1; K=$2; U=$3; CP=$4; FL=${5:-}; CONC=${6:-1}
  docker rm -f oc-nl >/dev/null 2>&1
  if [ "$CP" = none ]; then ./a-na.sh -- $FL >/dev/null; else ./a-na.sh --cpus $CP -- $FL >/dev/null; fi
  /bin/sleep 0.5; c0=$(cpuu); r=$("${NL[@]}" bulk -kind $K -url "$U" -for ${DUR:-20s} -conc $CONC 2>&1 | tail -1); c1=$(cpuu); t=$(thr); pk=$(docker exec oc-na cat /sys/fs/cgroup/memory.peak)
  docker rm -f oc-nl >/dev/null 2>&1
  python3 - "$L" "$CP" "$r" "$c0" "$c1" "$pk" "$t" <<'PY'
import sys,re
L,CP,r,c0,c1,pk,t=sys.argv[1:8]
m=re.search(r'bytes=(\d+) secs=([\d.]+) MBps=([\d.]+) errors=(\d+)',r)
if not m: print(L,CP,'FAILED',r[:200]); sys.exit()
b=int(m[1]); s=float(m[2]); cpu=(int(c1)-int(c0))/1e6
print(f"{L} | limit={CP} | {float(m[3]):.1f} MB/s | cpu {cpu:.1f}s over {s:.0f}s = {cpu/s*1000:.0f}m | {cpu/(b/2**30):.2f} cpu-s/GiB | peak {int(pk)/2**20:.0f} MiB | errors {m[4]} | {t}")
PY
}
O1=https://other.example.test/bulk?mb=204800
I1=https://api.example.test/bulk?mb=204800
echo "### upstream HTTP/2"; ./a-srv.sh -quiet
for rep in 1 2 3; do for CP in none 1 0.25 0.05; do
  bulk "opaque" h1 "$O1" $CP
  bulk "intercepted client-h1 up-h2 scrub" h1 "$I1" $CP
  bulk "intercepted client-h2 up-h2 scrub" h2 "$I1" $CP
done; done
echo "### upstream HTTP/1.1"; ./a-srv.sh -quiet -h1only
for rep in 1 2; do for CP in none 1 0.25 0.05; do bulk "intercepted client-h1 up-h1 scrub" h1 "$I1" $CP; done; done
echo "### cost of each part, no limit"; ./a-srv.sh -quiet
for rep in 1 2; do
 bulk "intercepted h1, scrubbing off" h1 "$I1" none "-window=off"
 bulk "intercepted h1, prefix window" h1 "$I1" none "-window=prefix"
 bulk "intercepted h1, fixed window (design text)" h1 "$I1" none "-window=fixed"
 bulk "intercepted h1, 99-byte secret" h1 "https://llm.example.test/bulk?mb=204800" none
 bulk "intercepted h1, gzip body decoded and scrubbed" h1 "https://api.example.test/bulktext?gz=1&mb=204800" none
 bulk "intercepted h1, same text uncompressed" h1 "https://api.example.test/bulktext?mb=204800" none
 bulk "opaque, 4 KiB copy buffer" h1 "$O1" none "-copy-buf=4096"
 bulk "opaque, 4 KiB copy buffer, limit 250m" h1 "$O1" 0.25 "-copy-buf=4096"
done
echo "### ten concurrent downloads"; bulk "opaque x10" h1 "$O1" none "" 10; bulk "intercepted h1 x10" h1 "$I1" none "" 10; bulk "intercepted h2 x10 one connection" h2 "$I1" none "" 10
echo "### requests: latency and CPU at limits, a new connection per request (client TLS handshake against the per-pod CA, upstream reused)"
rps(){ L=$1; K=$2; U=$3; CP=$4; R=$5; X=$6
  docker rm -f oc-nl >/dev/null 2>&1
  if [ "$CP" = none ]; then ./a-na.sh >/dev/null; else ./a-na.sh --cpus $CP >/dev/null; fi
  /bin/sleep 0.5; c0=$(cpuu); r=$("${NL[@]}" rps -kind $K -url "$U" -for 15s -rate $R $X 2>&1 | tail -1); c1=$(cpuu); docker rm -f oc-nl >/dev/null 2>&1
  echo "$L limit=$CP rate=$R: $r | netagent $(( (c1-c0)/15000 ))m"
}
for CP in none 1 0.25 0.05; do for R in 5 20 100; do
  rps "intercepted, new connection" h1 "https://api.example.test/small" $CP $R -new
  rps "intercepted, keep-alive" h1 "https://api.example.test/small" $CP $R
  rps "opaque, new connection" h1 "https://other.example.test/small" $CP $R -new
done; done
echo "### idle and session profiles, no limit: what a 50m request has to cover"
./a-na.sh >/dev/null; /bin/sleep 2; c0=$(cpuu); /bin/sleep 60; c1=$(cpuu); echo "idle warm pod, 60 s: $(( (c1-c0)/60000 ))m, $(memr | cut -d' ' -f1,3 | awk '{printf "current %.1f MiB anon %.1f MiB", $1/1048576, $2/1048576}')"
./a-na.sh >/dev/null; c0=$(cpuu); for i in 1 2 3 4 5; do docker run -d --name oc-nl$i --network container:oc-na -v $O/bin:/b:ro -v $O/certs:/certs:ro -v $O/run/netagent:/run/netagent:ro alpine:3.22 /b/naload sse -kind h2 -url "https://llm.example.test/stream?n=600&gap=50ms&size=30" >/dev/null; done; /bin/sleep 32; c1=$(cpuu); echo "five LLM streams at 20 events/s for 30 s: $(( (c1-c0)/32000 ))m, $(memr | cut -d' ' -f1,3 | awk '{printf "current %.1f MiB anon %.1f MiB", $1/1048576, $2/1048576}')"; for i in 1 2 3 4 5; do docker logs oc-nl$i 2>&1 | tail -1; docker rm -f oc-nl$i >/dev/null; done
./a-na.sh >/dev/null; c0=$(cpuu); r=$("${NL[@]}" rps -kind h1 -url "https://registry.example.test/small" -for 20s -rate 50 -new 2>&1 | tail -1); c1=$(cpuu); docker rm -f oc-nl >/dev/null; echo "package install stand-in, 50 new connections/s for 20 s: $(( (c1-c0)/20000 ))m, peak $(docker exec oc-na cat /sys/fs/cgroup/memory.peak | awk '{printf "%.1f MiB", $1/1048576}') | $r"
echo "### tunnels at a hard 64 MiB limit, before the container dies"
oomrun(){ L=$1; K=$2; U=$3; shift 3
  docker rm -f oc-nl >/dev/null 2>&1; ./a-na.sh --memory 64m --memory-swap 64m "$@" -- $NAFL >/dev/null; /bin/sleep 1
  "${NL[@]}" hold -kind $K -n 3000 -url "$U" -ramp 50 >/dev/null 2>&1 &
  last=""; for i in $(seq 1 75); do /bin/sleep 1; st=$(docker inspect -f '{{.State.Status}} oom={{.State.OOMKilled}}' oc-na); [ "$st" != "running oom=false" ] && break; last=$(docker exec oc-na wget -qO- http://127.0.0.1:9100/stats 2>/dev/null | python3 -c 'import sys,json;d=json.load(sys.stdin);print("active",d["active"],"goroutines",d["goroutines"],"heapInuse",d["heapInuse"]//1048576,"MiB")' 2>/dev/null); done
  echo "$L $NAFL: state=[$st] last stats before the end: $last"; docker rm -f oc-nl >/dev/null 2>&1; wait 2>/dev/null
}
./a-srv.sh -quiet -h1only
oomrun "opaque, GOMEMLIMIT unset" h1 "https://other.example.test/hold"
oomrun "intercepted h1, GOMEMLIMIT unset" h1 "https://api.example.test/hold"
oomrun "opaque, GOMEMLIMIT=50MiB" h1 "https://other.example.test/hold" -e GOMEMLIMIT=50MiB
oomrun "intercepted h1, GOMEMLIMIT=50MiB" h1 "https://api.example.test/hold" -e GOMEMLIMIT=50MiB
NAFL="-copy-buf=4096" oomrun "opaque" h1 "https://other.example.test/hold"
echo "### 20 stalled consumers at a hard 64 MiB limit, and with socket buffers bounded"
stall(){ L=$1; shift; docker rm -f oc-nl >/dev/null 2>&1; ./a-na.sh --memory 64m --memory-swap 64m -- "$@" >/dev/null; /bin/sleep 1; "${NL[@]}" stall -kind h1 -n 20 -url "https://api.example.test/bulk?mb=4096" >/dev/null 2>&1 & /bin/sleep 25; echo "$L: state=$(docker inspect -f '{{.State.Status}} oom={{.State.OOMKilled}}' oc-na) client=$(docker logs oc-nl 2>&1 | tail -1) $(memr 2>/dev/null | cut -d' ' -f1-4 | awk '{printf "current %.1f peak %.1f anon %.1f sock %.1f MiB", $1/1048576,$2/1048576,$3/1048576,$4/1048576}')"; docker rm -f oc-nl >/dev/null 2>&1; wait 2>/dev/null; }
./a-srv.sh -quiet; stall "upstream h2, defaults"; stall "upstream h2, h2-stream-buf=256k" -h2-stream-buf=262144; stall "upstream h2, h2-stream-buf=256k sockbuf=256k" -h2-stream-buf=262144 -sockbuf=262144
./a-srv.sh -quiet -h1only; stall "upstream h1, defaults"; stall "upstream h1, sockbuf=256k" -sockbuf=262144
./a-srv.sh -quiet
