#!/bin/bash
# a2mem.sh: memory per stream or tunnel for each kind.
. "$(dirname "$0")/a2lib.sh"
fmt='import sys,json
b=sys.argv[1].split(" ",5); a=sys.argv[2].split(" ",5); n=int(sys.argv[3]); ga=json.loads(a[5]); gb=json.loads(b[5])
M=1048576
print("   idle current=%.1f anon=%.1f | loaded current=%.1f anon=%.1f sock=%.1f kernel=%.1f peak=%.1f MiB | goroutines=%d heapInuse=%.1f stackInuse=%.1f MiB active=%d" % (int(b[0])/M,int(b[2])/M,int(a[0])/M,int(a[2])/M,int(a[3])/M,int(a[4])/M,int(a[1])/M,ga["goroutines"],ga["heapInuse"]/M,ga["stackInuse"]/M,ga["active"]))
print("   per stream: current %.1f KiB, anon %.1f KiB, sock %.1f KiB, goroutines %.1f" % ((int(a[0])-int(b[0]))/1024/n,(int(a[2])-int(b[2]))/1024/n,(int(a[3])-int(b[3]))/1024/n,(ga["goroutines"]-gb["goroutines"])/n))'
run(){ # label kind url n
  L=$1; K=$2; U=$3; N=$4; MODE=${5:-hold}; X=${6:-}
  docker rm -f oc-nl >/dev/null 2>&1; ./a-na.sh -- $NAFLAGS >/dev/null; /bin/sleep 1
  B=$(memr)
  "${NL[@]}" $MODE -kind $K -n $N -url "$U" -ramp 300 >/dev/null 2>&1 &
  for i in $(seq 1 120); do /bin/sleep 1; l=$(docker logs oc-nl 2>&1 | tail -1); case "$l" in *"up=$N "*) break;; esac; done
  /bin/sleep ${SETTLE:-8}; A=$(memr); echo "== $L n=$N: $(docker logs oc-nl 2>&1 | tail -1) $(docker logs oc-nl 2>&1 | grep -m2 err: | cut -c1-120)"
  python3 -c "$fmt" "$B" "$A" "$N"
  docker exec oc-na wget -qO- http://127.0.0.1:9100/gc >/dev/null; /bin/sleep 3; A2=$(memr); python3 -c "$fmt" "$B" "$A2" "$N" | sed -n 2p | sed 's/per stream/after a forced GC, per stream/'
  docker rm -f oc-nl >/dev/null 2>&1; wait 2>/dev/null
}
echo "### upstream speaks HTTP/1.1 only: each intercepted stream has its own tunnel to Envoy"
./a-srv.sh -quiet -h1only
for N in 100 1000 5000; do
  run "opaque (client TLS inside)" h1 "https://other.example.test/hold" $N
  run "intercepted, client h1, upstream h1, scrub on" h1 "https://api.example.test/hold" $N
done
run "intercepted, client h2, upstream h1, scrub on" h2 "https://api.example.test/hold" 1000
run "intercepted, client h1, upstream h1, gzip body decoded" h1 "https://api.example.test/hold?gz=1" 1000
echo "### upstream speaks HTTP/2: intercepted streams multiplex onto few tunnels"
./a-srv.sh -quiet
for N in 100 1000 5000; do
  run "intercepted, client h1, upstream h2, scrub on" h1 "https://api.example.test/hold" $N
  run "intercepted, client h2, upstream h2, scrub on" h2 "https://api.example.test/hold" $N
done
run "intercepted, client h2, upstream h2, gzip body decoded" h2 "https://api.example.test/hold?gz=1" 1000
echo "### slow consumer: 20 large responses, client stops reading"
for B in 0 262144; do
 NAFLAGS="-h2-stream-buf=$B" SETTLE=15 run "client stalls, upstream h2, h2-stream-buf=$B (0 is the net/http default)" h1 "https://api.example.test/bulk?mb=4096" 20 stall
done
./a-srv.sh -quiet -h1only
SETTLE=15 run "client stalls, upstream h1" h1 "https://api.example.test/bulk?mb=4096" 20 stall
./a-srv.sh -quiet
SETTLE=15 run "client stalls, opaque" h1 "https://other.example.test/bulk?mb=4096" 20 stall
echo "### a 64 MiB hard limit: tunnels until the container dies, GOMEMLIMIT unset and set"
oomrun(){ L=$1; K=$2; U=$3; shift 3
  docker rm -f oc-nl >/dev/null 2>&1; ./a-na.sh --memory 64m --memory-swap 64m "$@" -- >/dev/null; /bin/sleep 1
  "${NL[@]}" hold -kind $K -n 20000 -url "$U" -ramp 100 >/dev/null 2>&1 &
  last=""; for i in $(seq 1 150); do /bin/sleep 1; st=$(docker inspect -f '{{.State.Status}} oom={{.State.OOMKilled}}' oc-na); l=$(docker logs oc-nl 2>&1 | grep hold | tail -1); [ "$st" != "running oom=false" ] && break; last=$l; done
  echo "$L: state=[$st] last client line before the end: $last"; docker rm -f oc-nl >/dev/null 2>&1
}
./a-srv.sh -quiet -h1only
oomrun "opaque, GOMEMLIMIT unset" h1 "https://other.example.test/hold"
oomrun "intercepted h1, GOMEMLIMIT unset" h1 "https://api.example.test/hold"
oomrun "opaque, GOMEMLIMIT=50MiB" h1 "https://other.example.test/hold" -e GOMEMLIMIT=50MiB
oomrun "intercepted h1, GOMEMLIMIT=50MiB" h1 "https://api.example.test/hold" -e GOMEMLIMIT=50MiB
