#!/bin/bash
# t1.sh NAME MODE [DISRUPT_AT RESTORE_AT] : 500 CONNECT/s for 60 s (hold 2 s) through r4-envoy-main;
# at DISRUPT_AT (default 15 s) disrupt r4-authz1 with MODE, at RESTORE_AT (default 35 s) restore it.
#   kill  : docker kill (container exits, its IP disappears) / docker start
#   pkill : kill -9 the authz3 process inside the container (RST; wrapper restarts it after 2 s) / nothing
#   stop  : docker kill -s STOP (SIGSTOP, frozen process, kernel still ACKs) / docker kill -s CONT
cd "$(dirname "$0")"; N=$1; MODE=$2; K=${3:-15}; S=${4:-35}
docker exec r4-client curl -s 198.19.0.11:9002/reset >/dev/null; docker exec r4-client curl -s 198.19.0.12:9002/reset >/dev/null
curl -s -X POST 'http://127.0.0.1:19912/reset_counters' >/dev/null
( sleep $K
  case $MODE in
    kill)  docker kill r4-authz1 >/dev/null ;;
    pkill) docker exec r4-authz1 pkill -9 authz3 ;;
    stop)  docker exec r4-authz1 pkill -STOP authz3 ;;
  esac
  echo "   [authz1 $MODE at $(date +%T)]"
  sleep $((S-K))
  case $MODE in
    kill)  docker start r4-authz1 >/dev/null ;;
    stop)  docker exec r4-authz1 pkill -CONT authz3 ;;
  esac
  echo "   [authz1 restored at $(date +%T)]" ) &
echo "start $(date +%T)"
./ld.sh $N -rate ${RATE:-500} -n 0 -dur ${DUR:-60s} -hold 2s -bucket ${BUCKET:-500ms} 2>&1 | grep -v "Terminated\|^    \|^done"
wait
./st.sh 19912 | tee logs/$N.envoystats
docker exec r4-client curl -s 198.19.0.11:9002/count; docker exec r4-client curl -s 198.19.0.12:9002/count
docker logs r4-envoy-main 2>&1 | grep -i 'outlier\|eject\|keepalive' | cut -c1-300 | tail -8
