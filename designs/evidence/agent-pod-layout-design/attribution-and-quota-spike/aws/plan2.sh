#!/bin/bash
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
X=/data/scripts/t2.sh
while pgrep -f plan1.sh >/dev/null; do sleep 10; done
for p in runc runcns gvisor gvisorps kata katablock ext4; do bash $X $p; done
echo "=== enabling LocalStorageCapacityIsolationFSQuotaMonitoring $(date -u +%FT%TZ)"
sed -i 's|--write-kubeconfig-mode 644$|--write-kubeconfig-mode 644 --kubelet-arg=feature-gates=LocalStorageCapacityIsolationFSQuotaMonitoring=true|' /etc/systemd/system/k3s.service
grep ExecStart /etc/systemd/system/k3s.service
systemctl daemon-reload; systemctl restart k3s; sleep 20
for i in $(seq 1 40); do kubectl get nodes 2>/dev/null | grep -q ' Ready' && break; sleep 5; done; kubectl get nodes
journalctl -u k3s --since "-2min" | grep -i -E 'quota|feature' | tail -5
bash $X gate
bash $X gateevict
echo PLAN2-DONE
