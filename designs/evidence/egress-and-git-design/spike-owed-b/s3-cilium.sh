#!/bin/bash
R=spike-owed-b; cd $R; . ./kc.sh cilium
/opt/homebrew/bin/kind create cluster --name ob-cilium --config k8s/kind-2node-nokp.yaml --kubeconfig $R/kc-cilium --wait 0s 2>&1 | tail -1
CP=$($K get node ob-cilium-control-plane -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')
helm --kubeconfig $R/kc-cilium install cilium cilium --repo https://helm.cilium.io/ --version 1.20.4 --namespace kube-system --set kubeProxyReplacement=true --set k8sServiceHost=$CP --set k8sServicePort=6443 --set ipam.mode=kubernetes --set image.pullPolicy=IfNotPresent --set operator.replicas=1 2>&1 | grep -E "^(STATUS|Error)"
$K -n kube-system rollout status ds/cilium --timeout=300s; $K -n kube-system rollout status deploy/cilium-operator --timeout=180s
$K wait node --all --for=condition=Ready --timeout=180s
/opt/homebrew/bin/kind load docker-image ob-tools:3 --name ob-cilium 2>&1 | tail -1 | cut -c1-120
./s3-run.sh cilium
$K -n kube-system exec ds/cilium -c cilium-agent -- cilium-dbg identity list 2>&1 | grep -c -E "egress-group" | sed 's/^/identities carrying the label: /'
$K get ciliumidentities 2>&1 | wc -l | sed 's/^/ciliumidentities lines: /'
$K -n kube-system get cm cilium-config -o jsonpath='{.data.identity-allocation-mode}{" labels="}{.data.labels}{" ces="}{.data.enable-cilium-endpoint-slice}{"\n"}'
/opt/homebrew/bin/kind delete cluster --name ob-cilium --kubeconfig $R/kc-cilium 2>&1 | tail -1 | cut -c1-90
