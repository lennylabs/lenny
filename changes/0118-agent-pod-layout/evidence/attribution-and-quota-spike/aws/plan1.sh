#!/bin/bash
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
X=/data/scripts/t1.sh
bash $X runc runc true
bash $X runcns runc false
bash $X gvisor gvisor true
bash $X gvisorhost gvisor-host true
bash $X kata kata true
bash $X katablock kata-block true
echo PLAN1-DONE
