#!/bin/bash
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
while pgrep -f plan2.sh >/dev/null; do sleep 10; done
for l in runc runcns gvisor gvisorhost kata katablock; do bash /data/scripts/t1b.sh $l; done
echo PLAN1B-DONE
