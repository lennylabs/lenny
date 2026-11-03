#!/bin/bash
R=spike-owed-b; cd $R; . ./kc.sh pki
/opt/homebrew/bin/kind create cluster --name ob-pki --kubeconfig $R/kc-pki --wait 120s 2>&1 | tail -2
for i in ob-tools:1 envoyproxy/envoy:v1.40.2; do /opt/homebrew/bin/kind load docker-image $i --name ob-pki 2>&1 | tail -1 | cut -c1-120; done
$K version 2>&1 | tail -1
$K apply -f dl/cert-manager-v1.21.3.yaml >/dev/null
$K -n cert-manager rollout status deploy/cert-manager-webhook --timeout=240s
$K -n cert-manager rollout status deploy/cert-manager --timeout=120s
$K -n cert-manager rollout status deploy/cert-manager-cainjector --timeout=120s
$K -n cert-manager get pods -o wide
docker exec ob-pki-control-plane sh -c 'containerd --version; runc --version | head -1; uname -r; df -T /var/local-path-provisioner 2>&1 | tail -1'
