#!/bin/bash
# Long-window Cilium label test. s3-cil2.sh TAG [helm args...]: stamps every 6 s across 12 pods per placement; probes every 100 ms for 150 s from before the first stamp.
TAG=$1; shift
R=spike-owed-b; cd $R; . ./kc.sh cilium
if ! /opt/homebrew/bin/kind get clusters 2>/dev/null | grep -q '^ob-cilium$'; then
  /opt/homebrew/bin/kind create cluster --name ob-cilium --config k8s/kind-2node-nokp.yaml --kubeconfig $R/kc-cilium --wait 0s 2>&1 | tail -1 | cut -c1-60
  /opt/homebrew/bin/kind load docker-image ob-tools:4 --name ob-cilium 2>&1 | tail -1 | cut -c1-70
fi
CP=$($K get node ob-cilium-control-plane -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')
helm --kubeconfig $R/kc-cilium upgrade --install cilium cilium --repo https://helm.cilium.io/ --version 1.20.4 --namespace kube-system --set kubeProxyReplacement=true --set k8sServiceHost=$CP --set k8sServicePort=6443 --set ipam.mode=kubernetes --set image.pullPolicy=IfNotPresent --set operator.replicas=1 "$@" 2>&1 | grep -E "^(STATUS|Error)"
$K -n kube-system rollout restart ds/cilium >/dev/null; $K -n kube-system rollout status ds/cilium --timeout=300s | tail -1; $K -n kube-system rollout status deploy/cilium-operator --timeout=180s | tail -1
$K wait node --all --for=condition=Ready --timeout=180s >/dev/null
OUT=out/s3-cilium-$TAG; mkdir -p $OUT
$K delete ns agents lenny-system --ignore-not-found --wait=true >/dev/null
python3 k8s/grp.py ob-cilium-control-plane ob-cilium-worker 12 | sed 's/for i in range(12): cli(f"b{i}")//' > $OUT/grp.yaml
$K apply -f $OUT/grp.yaml >/dev/null
$K -n agents wait pod --all --for=condition=Ready --timeout=240s >/dev/null; $K -n lenny-system wait pod --all --for=condition=Ready --timeout=120s >/dev/null
sleep 15
$K -n kube-system exec ds/cilium -c cilium-agent -- cilium-dbg config -a 2>/dev/null | grep -i -E "IdentityChangeGracePeriod|identity-change" | head -3
$K -n kube-system get cm cilium-config -o yaml | grep -i -E "grace" | head
ip(){ $K -n lenny-system get pod $1 -o jsonpath='{.status.podIP}'; }
CROSS=$(ip ga-cross); SAME=$(ip ga-same); DEF=$(ip default-envoy)
for spec in "x $CROSS" "s $SAME"; do set -- $spec; for i in $(seq 0 11); do $K -n agents exec $1$i -- ob probe $2:3128 120s 100ms > $OUT/$1$i.log 2>&1 & done; done
$K -n agents exec unset -- ob probe $DEF:3128 3s 100ms > $OUT/ctl-unset-default.log 2>&1 &
$K -n agents exec wrong -- ob probe $CROSS:3128 120s 100ms > $OUT/ctl-wrong-cross.log 2>&1 &
$K -n agents exec unset -- ob probe $CROSS:3128 120s 100ms > $OUT/ctl-unset-cross.log 2>&1 &
sleep 4
for i in $(seq 0 11); do for pfx in x s; do p=$pfx$i; $K -n lenny-system exec stamper -- ob stamp agents $p lenny.dev/egress-group=group-a > $OUT/$p.stamp 2>&1 & done; sleep 6; done
wait
echo "controls: unset->default [$(grep STATE $OUT/ctl-unset-default.log | cut -d' ' -f2,3 | tr '\n' ';')]; unset->group 120 s [$(grep -E 'STATE|REGRESS' $OUT/ctl-unset-cross.log | cut -d' ' -f2,3 | tr '\n' ';')]; wrong->group 120 s [$(grep -E 'STATE|REGRESS' $OUT/ctl-wrong-cross.log | cut -d' ' -f2,3 | tr '\n' ';')]"
python3 s3-analyze2.py $OUT
$K -n kube-system logs ds/cilium -c cilium-agent --tail=2000 2>/dev/null | grep -i -E "identity.*(change|grace)|Grace" | tail -3 | cut -c1-200
