#!/bin/bash
# t4.sh: Kata block-plain statfs accounting with fsGroup and no host quota
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
R=/data/results/t2-katablock3; mkdir -p $R
exec > >(tee -a $R/run.log) 2>&1
echo "=== t4 $(date -u +%FT%TZ)"
. <(sed -n '/^sx()/p;/^evwait()/,/^}/p' /data/scripts/t2.sh)
N=q-katablock3
sed -e 's/name: q-katablock2/name: q-katablock3/' /data/results/t2-gvisorps2/q-katablock2.yaml > $R/$N.yaml
kubectl apply -f $R/$N.yaml >/dev/null; kubectl -n aq wait --for=condition=Ready pod/$N --timeout=300s >/dev/null
PUID=$(kubectl -n aq get pod $N -o jsonpath='{.metadata.uid}'); HP=/var/lib/kubelet/pods/$PUID/volumes/kubernetes.io~empty-dir/ws
st() { echo "$1 | guest: $(sx aq probe /workspace | jq -c '.statfs|{size_mb:(.size_b/1048576),used_mb:(.used_b/1048576)}') | host disk.img allocated: $(( $(stat -c %b $HP/disk.img) * 512 / 1048576 )) MiB | kubelet: $(kubectl get --raw /api/v1/nodes/$(hostname)/proxy/stats/summary | jq -c ".pods[]|select(.podRef.name==\"$N\")|.volume[]?|select(.name==\"ws\")|.usedBytes/1048576")"; }
st start
sx aq fill -path /workspace -mb 64 -name a64 >/dev/null; sleep 12; st "after 64 MiB"
sx aq hide -path /workspace -mb 50 >/dev/null; sleep 12; st "after 50 MiB under a mode-000 dir"
sx rm -f /workspace/a64; sleep 12; st "after rm of 64 MiB (discard mount option)"
sx aq fill -path /workspace -mb 1200 -name over; sleep 2; st "after 1200 MiB (sizeLimit 1Gi)"
evwait 90
kubectl -n aq delete pod $N --wait=true --timeout=120s >/dev/null 2>&1
echo "=== t4 done"
