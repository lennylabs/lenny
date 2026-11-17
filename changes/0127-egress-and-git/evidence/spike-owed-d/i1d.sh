#!/bin/bash
# i1b.sh: a count that holds. Streams held for 120 s below the kill point, and a new connection's latency beside them, at the hard limit.
. "$(dirname "$0")/lib-a.sh"
OUT=raw/i1b-sustain.txt; mkdir -p raw/i1b
NL(){ docker rm -f od-nl >/dev/null 2>&1; docker run -d --name od-nl --network container:od-na --ulimit nofile=200000:200000 -v $O/bin:/b:ro -v $O/certs:/certs:ro -v $O/run/netagent:/run/netagent:ro -v $O/raw/i1b:/log alpine:3.22 /b/naload "$@" >/dev/null; }
W(){ docker rm -f od-w >/dev/null 2>&1; CID=$(docker inspect -f '{{.Id}}' od-na); docker run -d --name od-w --network container:od-na --cgroupns=host -v $O/bin:/b:ro -v $O/raw/i1b:/log alpine:3.22 /b/memwatch -cg /sys/fs/cgroup/docker/$CID -log /log/$1 >/dev/null; }
upstream=""; set_up(){ [ "$upstream" = "$1" ] && return; upstream=$1; if [ "$1" = h1 ]; then srv -quiet -h1only; else srv -quiet; fi; }
run(){ L=$1; UP=$2; K=$3; U=$4; N=$5; GM=$6; set_up $UP; T="$L.$N.${GM:-unset}.$(echo "$U" | grep -o tick | head -1)"
  EF=(); [ -n "$GM" ] && EF=(-e GOMEMLIMIT=$GM); na --memory 64m --memory-swap 64m "${EF[@]}" --; W "$T.w"; /bin/sleep 1.5; c0=$(docker exec od-na cat /sys/fs/cgroup/cpu.stat | awk '/usage_usec/{print $2}')
  NL hold -kind $K -n $N -url "$U" -ramp 50 -log /log/$T.c; t=0; st=""; while [ $t -lt $((N/50+120)) ]; do /bin/sleep 5; t=$((t+5)); st=$(docker inspect -f '{{.State.Status}}' od-na); [ "$st" = running ] || break; done
  c1=$(docker exec od-na cat /sys/fs/cgroup/cpu.stat 2>/dev/null | awk '/usage_usec/{print $2}'); lat=""; [ "$st" = running ] && lat=$(docker run --rm --name od-rps --network container:od-na -v $O/bin:/b:ro -v $O/certs:/certs:ro -v $O/run/netagent:/run/netagent:ro alpine:3.22 /b/naload rps -kind h1 -rate 5 -for 10s -new -url "https://other.example.test/small" 2>&1 | tail -1 | cut -c1-120)
  printf '%s up=%s target=%s go=%s state=[%s oom=%s] cpu over the run=%sm\n  %s\n  new connections beside them: %s\n' "$L" "$UP" "$N" "${GM:-unset}" "$st" "$(docker inspect -f '{{.State.OOMKilled}}' od-na)" "$(( (${c1:-0}-${c0:-0})/(t*1000) ))" "$(py i1-sum.py raw/i1b/$T.w raw/i1b/$T.c 2>&1 | sed '2,$s/^/  /')" "$lat" >> $OUT
  tail -5 $OUT | cut -c1-200; docker rm -f od-nl od-w od-na >/dev/null 2>&1; }
a_up; echo "# $(date) hold for 120 s after the ramp, 50 streams/s, idle tick every 10 s" >> $OUT
run int-c1-u1 h1 h1 https://api.example.test/hold 400 ""; run int-c1-u1 h1 h1 https://api.example.test/hold 300 ""
run opaque h1 h1 https://other.example.test/hold 500 30MiB; run int-c1-u1 h1 h1 https://api.example.test/hold 400 30MiB
docker rm -f od-envoy od-authz1 od-authz2 od-up-srv >/dev/null 2>&1; docker network rm od-up od-pod >/dev/null 2>&1; echo done > raw/i1d-done
