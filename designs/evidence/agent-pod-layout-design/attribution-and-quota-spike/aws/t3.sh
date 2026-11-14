#!/bin/bash
# t3.sh: gVisor pod-share filestore under a project quota (the backing file moved into the project)
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
R=/data/results/t2-gvisorps2; mkdir -p $R
exec > >(tee -a $R/run.log) 2>&1
echo "=== t3 gvisorps2 $(date -u +%FT%TZ)"
. <(sed -n '/^qpod()/,/^}/p;/^sx()/p;/^hrep()/p;/^evwait()/,/^}/p' /data/scripts/t2.sh)
MiB=1048576
N=q-gvisorps2; qpod $N gvisor true yes 1Gi || exit 1
FS=$(ls $HP/.gvisor.filestore.* | head -1); ID=55001
aq qset -path $HP -id $ID -hard $((256*MiB))
aq qset -path $FS -id $ID -no-inherit -hard $((256*MiB))
hrep $ID; ls -las $HP
sx aq fill -path /workspace -mb 200 -name a200; hrep $ID
sx aq fill -path /workspace -mb 400 -name b400; hrep $ID
sx aq fill -path /workspace -mb 100 -fsync-each -name c100; hrep $ID
sx sh -c 'ls -la /workspace; for f in a200 b400 c100; do echo "$f tail:"; tail -c 4096 /workspace/$f 2>&1 | od -An -tx1 | head -1; done; md5sum /workspace/b400 2>&1'
sx aq probe /workspace
kubectl -n aq get pod $N -o wide; evwait 20
journalctl --since "-5min" 2>/dev/null | grep -i -E 'runsc|gvisor|filestore|ENOSPC|no space' | tail -5
kubectl -n aq delete pod $N --wait=true --timeout=120s >/dev/null 2>&1
echo "=== t3 katablock2 (fsGroup 29000, disk.img moved into a 128 MiB project) $(date -u +%FT%TZ)"
N=q-katablock2
sed -e 's/name: q-gvisorps2/name: q-katablock2/' -e 's/runtimeClassName: gvisor/runtimeClassName: kata-block/' -e '/dev.gvisor/d' -e 's/^  hostUsers: true/  hostUsers: true\n  securityContext: {fsGroup: 29000}/' $R/q-gvisorps2.yaml > $R/$N.yaml
grep -n -E 'runtimeClass|fsGroup|annot|name: q' $R/$N.yaml
kubectl apply -f $R/$N.yaml >/dev/null; kubectl -n aq wait --for=condition=Ready pod/$N --timeout=300s >/dev/null || kubectl -n aq describe pod $N | tail -5
PUID=$(kubectl -n aq get pod $N -o jsonpath='{.metadata.uid}'); HP=/var/lib/kubelet/pods/$PUID/volumes/kubernetes.io~empty-dir/ws
ls -las $HP; sx aq probe /workspace
ID=55002; aq qset -path $HP -id $ID -hard $((128*MiB)); aq qset -path $HP/disk.img -id $ID -no-inherit -hard $((128*MiB)); hrep $ID
sx aq fill -path /workspace -mb 64 -name a64; hrep $ID
sx aq fill -path /workspace -mb 300 -fsync-each -name b300; hrep $ID
sx sh -c 'dmesg 2>&1 | tail -8; grep " /workspace " /proc/mounts; ls -la /workspace; touch /workspace/after && echo touch-ok'
sx aq probe /workspace; evwait 20
kubectl -n aq delete pod $N --wait=true --timeout=120s >/dev/null 2>&1
echo "=== t3 done"
