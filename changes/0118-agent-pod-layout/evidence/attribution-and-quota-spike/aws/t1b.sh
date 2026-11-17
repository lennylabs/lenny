#!/bin/bash
# t1b.sh LABEL: at-accept latency under a 3 x 334 burst with one reader at a time (pod from t1)
L=$1; P=t1-$L; R=/data/results/t1-$L
exec > >(tee -a $R/burst.log) 2>&1
echo "=== t1b $L $(date -u +%FT%TZ)"
kubectl delete pod -n aq --all --wait=true --timeout=120s >/dev/null 2>&1
kubectl apply -f $R/pod.yaml >/dev/null; kubectl -n aq wait --for=condition=Ready pod/$P --timeout=300s >/dev/null || exit 1
ex() { local c=$1; shift; kubectl -n aq exec $P -c $c -- "$@"; }
sleep 1
for rd in diag proc; do
  ex netagent aq ctl readers $rd >/dev/null; ex netagent aq ctl reset >/dev/null
  PIDS=""; for c in slot0 slot1 slot2; do ex $c aq conn -label b$rd-$c -n 334 -par 64 -hold 8s > /dev/null 2>&1 & PIDS="$PIDS $!"; done
  wait $PIDS; sleep 1
  echo "readers=$rd"; ex netagent aq ctl summary
done
kubectl delete pod -n aq $P --wait=true --timeout=120s >/dev/null
echo "=== t1b $L done"
