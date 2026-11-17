#!/bin/bash
# i1.sh: tunnels until the kill at a hard 64 MiB limit. Every reading is written as it arrives by programs outside the limited cgroup.
. "$(dirname "$0")/lib-a.sh"
OUT=raw/i1-hardlimit.txt; mkdir -p raw/i1
NL(){ docker rm -f od-nl >/dev/null 2>&1; docker run -d --name od-nl --network container:od-na --ulimit nofile=200000:200000 -v $O/bin:/b:ro -v $O/run/trust:/s:ro -v $O/certs:/certs:ro -v $O/run/netagent:/run/netagent:ro -v $O/raw/i1:/log alpine:3.22 /b/naload "$@" >/dev/null; }
W(){ docker rm -f od-w >/dev/null 2>&1; CID=$(docker inspect -f '{{.Id}}' od-na); docker run -d --name od-w --network container:od-na --cgroupns=host -v $O/bin:/b:ro -v $O/raw/i1:/log alpine:3.22 /b/memwatch -cg /sys/fs/cgroup/docker/$CID -log /log/$1 >/dev/null; }
upstream=""
set_up(){ [ "$upstream" = "$1" ] && return; upstream=$1; if [ "$1" = h1 ]; then srv -quiet -h1only; else srv -quiet; fi; }
# run LABEL UPSTREAM KIND URL REP RAMP GOMEM
run(){ L=$1; UP=$2; K=$3; U=$4; R=$5; RAMP=$6; GM=$7
  set_up $UP; T="$L.$RAMP.${GM:-unset}.$R"
  EF=(); [ -n "$GM" ] && EF=(-e GOMEMLIMIT=$GM)
  na --memory 64m --memory-swap 64m "${EF[@]}" --
  W "$T.w"; /bin/sleep 1.5
  NL hold -kind $K -n 20000 -url "$U" -ramp $RAMP -log /log/$T.c
  t0=$(date +%s); state=""; lastup=0; lastchg=$t0
  while :; do
    /bin/sleep 1
    state=$(docker inspect -f '{{.State.Status}} oom={{.State.OOMKilled}} exit={{.State.ExitCode}}' od-na)
    case "$state" in running*) ;; *) break;; esac
    cur=$(tail -1 raw/i1/$T.c 2>/dev/null | cut -d' ' -f2); [ "${cur:-0}" != "$lastup" ] && lastup=${cur:-0} && lastchg=$(date +%s)
    now=$(date +%s); [ $((now-lastchg)) -ge 45 ] && { state="$state plateau45s"; break; }
    [ $((now-t0)) -ge 600 ] && { state="$state cap600s"; break; }
  done
  /bin/sleep 1.5
  printf '%s up=%s go=%s ramp=%s rep=%s state=[%s] wall=%ss\n  %s\n' "$L" "$UP" "${GM:-unset}" "$RAMP" "$R" "$state" "$(( $(date +%s)-t0 ))" "$(py i1-sum.py raw/i1/$T.w raw/i1/$T.c 2>&1 | sed '2,$s/^/  /')" >> $OUT
  tail -3 $OUT | cut -c1-220
  docker rm -f od-nl od-w od-na >/dev/null 2>&1
}
kinds(){ # one line per kind
cat <<K
opaque h1 h1 https://other.example.test/hold
int-c1-u1 h1 h1 https://api.example.test/hold
int-c2-u1 h1 h2 https://api.example.test/hold
int-c1-u2 h2 h1 https://api.example.test/hold
int-c2-u2 h2 h2 https://api.example.test/hold
gz-c1-u1 h1 h1 https://api.example.test/hold?gz=1
gz-c2-u1 h1 h2 https://api.example.test/hold?gz=1
gz-c1-u2 h2 h1 https://api.example.test/hold?gz=1
gz-c2-u2 h2 h2 https://api.example.test/hold?gz=1
K
}
a_up
echo "# $(date) 64 MiB hard limit, memory-swap 64 MiB, no CPU limit, client and poller in other cgroups, idle tick every 10 s" >> $OUT
while read L UP K U <&3; do
  for R in 1 2 3; do run $L $UP $K "$U" $R 100 ""; done
  for R in 1 2 3; do run $L $UP $K "$U" $R 100 50MiB; done
done 3< <(kinds)
while read L UP K U <&3; do
  run $L $UP $K "$U" 1 20 ""; run $L $UP $K "$U" 2 20 ""
  run $L $UP $K "$U" 1 20 50MiB; run $L $UP $K "$U" 2 20 50MiB
done 3< <(kinds | grep -E '^(opaque|int-c1-u1|int-c2-u2) ')
echo done > raw/i1-done
