#!/bin/bash
# t1.sh LABEL RUNTIMECLASS HOSTUSERS: per-slot attribution in one pod with adapter, netagent, and three slots
L=$1 RC=$2 HU=$3
P=t1-$L
R=/data/results/t1-$L
mkdir -p $R
exec > >(tee -a $R/run.log) 2>&1
echo "=== t1 $L rc=$RC hostUsers=$HU $(date -u +%FT%TZ)"
ctr() { # name uid cmd
  cat <<Y
  - name: $1
    image: docker.io/library/aqspike:1
    imagePullPolicy: Never
    command: [$3]
    securityContext: {runAsUser: $2, runAsGroup: $2, runAsNonRoot: true, allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}, seccompProfile: {type: RuntimeDefault}}
    resources: {requests: {cpu: 50m, memory: 32Mi}, limits: {memory: 512Mi}}
Y
}
RCL=""; [ "$RC" != runc ] && RCL="runtimeClassName: $RC"
{
cat <<Y
apiVersion: v1
kind: Pod
metadata: {name: $P, namespace: aq}
spec:
  $RCL
  hostUsers: $HU
  terminationGracePeriodSeconds: 1
  containers:
Y
ctr netagent 10001 '"aq","listen","-out","/tmp/l.jsonl"'
ctr adapter 10000 '"aq","sleep"'
ctr slot0 20000 '"aq","sleep"'
ctr slot1 20001 '"aq","sleep"'
ctr slot2 20002 '"aq","sleep"'
} > $R/pod.yaml
kubectl delete pod -n aq --all --wait=true --timeout=120s >/dev/null 2>&1
kubectl apply -f $R/pod.yaml
kubectl -n aq wait --for=condition=Ready pod/$P --timeout=300s || { kubectl -n aq describe pod $P | tail -20; exit 1; }
ex() { local c=$1; shift; kubectl -n aq exec $P -c $c -- "$@"; }
echo "--- ids"; for c in netagent adapter slot0 slot1 slot2; do echo "$c $(ex $c aq ids)"; done
echo "--- kernel"; ex netagent uname -a; ex netagent sh -c 'ls -la /proc/net/tcp /proc/net/tcp6; head -3 /proc/net/tcp'
sleep 1
echo "--- basic"
for c in slot0 slot1 slot2 adapter; do ex $c aq conn -label basic-$c -n 1 -hold 500ms; ex $c aq conn -addr '[::1]:9000' -label basic6-$c -n 1 -hold 500ms; done
sleep 1; ex netagent aq ctl summary > $R/basic.json; cat $R/basic.json; ex netagent aq ctl reset
echo "--- many (3 x 334 concurrent, held)"
PIDS=""; for c in slot0 slot1 slot2; do ex $c aq conn -label many-$c -n 334 -par 64 -hold 45s > $R/many-$c.out 2>&1 & PIDS="$PIDS $!"; done
sleep 20
echo "held $(ex netagent aq ctl held)"
ex netagent aq ctl bench > $R/bench.json; cat $R/bench.json
echo "--- ss in netagent (iproute2 sock_diag)"
ex netagent sh -c "ss -tneH state established '( dport = :9000 )' 2>&1 | grep -o 'uid:[0-9]*' | sort | uniq -c; ss -tneH '( dport = :9000 )' 2>&1 | head -2"
echo "--- host view of the pod netns (root, init userns)"
NPID=$(pgrep -f 'aq listen -out /tmp/l.jsonl' | head -1)
if [ -n "$NPID" ]; then nsenter -t $NPID -n ss -tneH state established '( dport = :9000 )' | grep -o 'uid:[0-9]*' | sort | uniq -c; grep -h '' /proc/$NPID/uid_map; else echo "no host pid (sandboxed)"; fi
wait $PIDS
cat $R/many-*.out
ex netagent aq ctl summary > $R/many.json; cat $R/many.json; ex netagent aq ctl reset
echo "--- passing a socket over an abstract Unix socket, slot0 -> slot1"
ex slot1 aq passsrv -name p1 -label passU-into-slot1 -hold 2s & W1=$!; sleep 2; ex slot0 aq passcli -name p1; wait $W1
ex slot1 aq passsrv -name p2 -label passC-into-slot1 -hold 2s & W1=$!; sleep 2; ex slot0 aq passcli -name p2 -connected; wait $W1
echo "--- forked child and a nested user namespace in slot0"
ex slot0 sh -c 'aq conn -label fork-slot0 -n 1 -hold 300ms & wait $!'
ex slot0 unshare -U -r sh -c 'id; aq conn -label unshareU-slot0 -n 1 -hold 300ms'; echo "unshare exit $?"
ex slot0 unshare -U --map-user=20001 sh -c 'id; aq conn -label unshareMap-slot0 -n 1 -hold 300ms'; echo "unshare map exit $?"
sleep 1; ex netagent aq ctl summary > $R/pass.json; cat $R/pass.json; ex netagent aq ctl reset
echo "--- close before lookup (lookup delayed 100 ms)"
ex netagent aq ctl delay 100
ex slot0 aq conn -label close-slot0 -n 20 -mode close -hold 0s
sleep 2; ex netagent aq ctl summary > $R/close.json; cat $R/close.json; ex netagent aq ctl reset
ex slot0 aq conn -label rst-slot0 -n 20 -mode rst -hold 0s
sleep 2; ex netagent aq ctl summary > $R/rst.json; cat $R/rst.json; ex netagent aq ctl reset
ex netagent aq ctl delay 0
echo "--- many over v6 (1 x 1000)"
ex slot1 aq conn -addr '[::1]:9000' -label many6-slot1 -n 1000 -par 64 -hold 30s > $R/many6.out 2>&1 & W1=$!
sleep 18; ex netagent aq ctl bench > $R/bench6.json; cat $R/bench6.json; wait $W1; cat $R/many6.out
ex netagent aq ctl summary > $R/many6.json; cat $R/many6.json
ex netagent cat /tmp/l.jsonl > $R/l.jsonl
kubectl delete pod -n aq $P --wait=true --timeout=120s
echo "=== t1 $L done $(date -u +%FT%TZ)"
