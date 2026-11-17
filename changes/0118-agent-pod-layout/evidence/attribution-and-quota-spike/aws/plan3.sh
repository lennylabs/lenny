#!/bin/bash
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
while pgrep -f plan1b.sh >/dev/null || pgrep -f plan2.sh >/dev/null; do sleep 10; done
bash /data/scripts/t3.sh
echo PLAN3-DONE
