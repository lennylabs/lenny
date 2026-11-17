#!/bin/bash
# t4kill.sh NAME KILL_AT START_AT : 200 CONNECT/s for 40 s; docker kill r3-authz at KILL_AT s, docker start at START_AT s.
cd "$(dirname "$0")"; N=$1; K=$2; S=$3
( sleep $K; docker kill r3-authz >/dev/null; echo "   [authz killed at $(date +%T)]"; sleep $((S-K)); docker start r3-authz >/dev/null; echo "   [authz started at $(date +%T)]" ) &
./ld.sh $N -rate ${RATE:-200} -n 0 -dur ${DUR:-40s} -hold 3s 2>&1 | grep -v "Terminated\|^    \|^done"
wait
