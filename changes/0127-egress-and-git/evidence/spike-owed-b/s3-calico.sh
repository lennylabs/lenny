#!/bin/bash
R=spike-owed-b; cd $R; . ./kc.sh calico
/opt/homebrew/bin/kind create cluster --name ob-calico --config k8s/kind-2node.yaml --kubeconfig $R/kc-calico --wait 0s 2>&1 | tail -1
$K apply -f ../spike-cni/dl/calico-v3.33.0.yaml >/dev/null
$K -n kube-system rollout status ds/calico-node --timeout=300s; $K -n kube-system rollout status deploy/calico-kube-controllers --timeout=180s
$K wait node --all --for=condition=Ready --timeout=120s
/opt/homebrew/bin/kind load docker-image ob-tools:3 --name ob-calico 2>&1 | tail -1 | cut -c1-120
$K -n kube-system get ds calico-node -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
./s3-run.sh calico
/opt/homebrew/bin/kind delete cluster --name ob-calico --kubeconfig $R/kc-calico 2>&1 | tail -1 | cut -c1-90
