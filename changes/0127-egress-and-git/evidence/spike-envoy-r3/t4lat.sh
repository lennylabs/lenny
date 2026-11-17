#!/bin/bash
# t4lat.sh NAME SCHEDULE... : run loadgen (200/s, 50 s) while changing the authz delay on a schedule "sec:ms".
cd "$(dirname "$0")"; N=$1; shift
dly() { docker exec r3-client curl -s "198.18.0.11:9002/delay?ms=$1" >/dev/null; echo "   [delay -> ${1}ms at $(date +%T)]"; }
dly 0
( s0=$(date +%s); for e in "$@"; do t=${e%%:*}; v=${e#*:}; while [ $(( $(date +%s) - s0 )) -lt $t ]; do sleep 0.2; done; dly $v; done ) &
./ld.sh $N -rate ${RATE:-200} -n 0 -dur ${DUR:-50s} -hold 3s 2>&1 | grep -v "Terminated\|^    \|^done"
wait
