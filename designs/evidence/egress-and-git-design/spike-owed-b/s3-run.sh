#!/bin/bash
# s3-run.sh CNI: label path test on an installed cluster. Timestamps from one kernel: every container on a kind node shares the Docker VM's clock.
CNI=$1; R=spike-owed-b; cd $R; . ./kc.sh $CNI
OUT=out/s3-$CNI; mkdir -p $OUT; CP=ob-$CNI-control-plane; W=ob-$CNI-worker; NS=10
python3 k8s/grp.py $CP $W $NS > $OUT/grp.yaml
$K apply -f $OUT/grp.yaml >/dev/null
$K -n agents wait pod --all --for=condition=Ready --timeout=240s >/dev/null; $K -n lenny-system wait pod --all --for=condition=Ready --timeout=120s >/dev/null
sleep 10
ip(){ $K -n lenny-system get pod $1 -o jsonpath='{.status.podIP}'; }
CROSS=$(ip ga-cross); SAME=$(ip ga-same); DEF=$(ip default-envoy)
$K -n lenny-system get pod -o wide | tee $OUT/pods.txt | cut -c1-110
echo "### controls: 3 s each, from pods that stay unstamped or wrongly stamped"
$K -n agents exec unset -- ob probe $DEF:3128 3s > $OUT/ctl-unset-default.log 2>&1; echo "unset -> default Envoy (no ingress policy): $(grep -E 'STATE|REGRESS' $OUT/ctl-unset-default.log | head -2 | cut -d' ' -f2,3 | tr '\n' ';')"
$K -n agents exec unset -- ob probe $CROSS:3128 3s > $OUT/ctl-unset-cross.log 2>&1; echo "unset -> group Envoy, other node: $(grep -E 'STATE|REGRESS' $OUT/ctl-unset-cross.log | cut -d' ' -f2,3 | tr '\n' ';')"
$K -n agents exec unset -- ob probe $SAME:3128 3s > $OUT/ctl-unset-same.log 2>&1; echo "unset -> group Envoy, same node: $(grep -E 'STATE|REGRESS' $OUT/ctl-unset-same.log | cut -d' ' -f2,3 | tr '\n' ';')"
$K -n agents exec wrong -- ob probe $CROSS:3128 3s > $OUT/ctl-wrong-cross.log 2>&1; echo "group-b -> group-a Envoy, other node: $(grep -E 'STATE|REGRESS' $OUT/ctl-wrong-cross.log | cut -d' ' -f2,3 | tr '\n' ';')"
$K -n agents exec wrong -- ob probe $SAME:3128 3s > $OUT/ctl-wrong-same.log 2>&1; echo "group-b -> group-a Envoy, same node: $(grep -E 'STATE|REGRESS' $OUT/ctl-wrong-same.log | cut -d' ' -f2,3 | tr '\n' ';')"
echo "### sequential: probe from 2 s before the stamp to 10 s after"
for pfx in x s; do [ $pfx = x ] && T=$CROSS || T=$SAME
 for i in $(seq 0 $((NS-1))); do p=$pfx$i
  $K -n agents exec $p -- ob probe $T:3128 12s > $OUT/$p.log 2>&1 &
  sleep 2
  $K -n lenny-system exec stamper -- ob stamp agents $p lenny.dev/egress-group=group-a > $OUT/$p.stamp 2>&1
  wait
 done
done
echo "### burst: 12 stamps in parallel, half of each placement"
for i in $(seq 0 11); do p=b$i; [ $((i%2)) = 0 ] && T=$CROSS || T=$SAME
  $K -n agents exec $p -- ob probe $T:3128 12s > $OUT/$p.log 2>&1 &
done
sleep 3
for i in $(seq 0 11); do p=b$i; $K -n lenny-system exec stamper -- ob stamp agents $p lenny.dev/egress-group=group-a > $OUT/$p.stamp 2>&1 & done
wait
echo "### after the run: stamped-to-wrong control still dropped"
$K -n agents exec wrong -- ob probe $CROSS:3128 2s 2>&1 | grep -E 'STATE|REGRESS' | cut -d' ' -f2,3 | tr '\n' ';'; echo
python3 s3-analyze.py $OUT
