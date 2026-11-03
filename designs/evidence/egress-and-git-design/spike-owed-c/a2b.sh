#!/bin/bash
# a2b.sh: rerun of the hard-limit rows with the last good stats kept (the first run overwrote them when the last poll failed), and per-tunnel memory with a 4 KiB copy buffer.
. "$(dirname "$0")/a2lib.sh"
oomrun(){ L=$1; K=$2; U=$3; shift 3
  docker rm -f oc-nl >/dev/null 2>&1; ./a-na.sh --memory 64m --memory-swap 64m "$@" -- $NAFL >/dev/null; /bin/sleep 1
  "${NL[@]}" hold -kind $K -n 3000 -url "$U" -ramp 50 >/dev/null 2>&1 &
  last=""; for i in $(seq 1 75); do /bin/sleep 1; st=$(docker inspect -f '{{.State.Status}} oom={{.State.OOMKilled}}' oc-na); [ "$st" != "running oom=false" ] && break; x=$(docker exec oc-na wget -qO- http://127.0.0.1:9100/stats 2>/dev/null | python3 -c 'import sys,json;d=json.load(sys.stdin);print("active",d["active"],"goroutines",d["goroutines"],"heapInuse",d["heapInuse"]//1048576,"MiB")' 2>/dev/null); [ -n "$x" ] && last=$x; done
  echo "$L $NAFL: state=[$st] after ${i}s; last stats: $last"; docker rm -f oc-nl >/dev/null 2>&1; wait 2>/dev/null
}
./a-srv.sh -quiet -h1only
oomrun "opaque, GOMEMLIMIT unset" h1 "https://other.example.test/hold"
oomrun "intercepted h1 up h1, GOMEMLIMIT unset" h1 "https://api.example.test/hold"
oomrun "opaque, GOMEMLIMIT=50MiB" h1 "https://other.example.test/hold" -e GOMEMLIMIT=50MiB
oomrun "intercepted h1 up h1, GOMEMLIMIT=50MiB" h1 "https://api.example.test/hold" -e GOMEMLIMIT=50MiB
NAFL="-copy-buf=4096" oomrun "opaque" h1 "https://other.example.test/hold"
./a-srv.sh -quiet
oomrun "intercepted h2 up h2, GOMEMLIMIT unset" h2 "https://api.example.test/hold"
