#!/bin/bash
R=spike-owed-b; cd $R; . ./kc.sh pki
$K delete validatingwebhookconfiguration ob-egress-group lenny-label-immutability lenny-tenant-label-immutability --ignore-not-found >/dev/null; $K delete -f k8s/vap.yaml --ignore-not-found >/dev/null
/opt/homebrew/bin/kind load docker-image ob-webhook:1 --name ob-pki 2>&1 | tail -1 | cut -c1-70
$K -n agents delete pod --all --wait=true >/dev/null
$K apply -f k8s/web.yaml >/dev/null
$K -n lenny-system wait pod/ob-webhook --for=condition=Ready --timeout=90s
$K label ns agents lenny.dev/agent-namespace=true --overwrite >/dev/null
sed 's/EGRESSRES/["pods","pods\/status"]/' k8s/webcfg.tmpl | $K apply -f - >/dev/null
sleep 8
for w in lenny-label-immutability lenny-tenant-label-immutability ob-egress-group; do echo "$w caBundle bytes: $($K get validatingwebhookconfiguration $w -o jsonpath='{.webhooks[0].clientConfig.caBundle}' | wc -c | tr -d ' ')"; done
echo "=================== WEBHOOK PROTOTYPE, with the repository's two label deciders beside it"
./s4-matrix.sh 2>&1
sleep 20
echo "=================== status subresource with the rule set the chart uses today: resources [pods] only"
sed 's/EGRESSRES/["pods"]/' k8s/webcfg.tmpl | $K apply -f - >/dev/null; sleep 8
. ./kc.sh pki
A="$K -n agents"; L=lenny.dev/egress-group
podj(){ echo "{\"apiVersion\":\"v1\",\"kind\":\"Pod\",\"metadata\":{\"name\":\"$1\",\"namespace\":\"agents\",\"labels\":{\"app\":\"x\"}},\"spec\":{\"nodeName\":\"ob-pki-control-plane\",\"containers\":[{\"name\":\"c\",\"image\":\"ob-tools:1\",\"imagePullPolicy\":\"Never\",\"command\":[\"ob\",\"sleep\"]}]}}"; }
podj st1 | $A apply -f - >/dev/null
for u in system:serviceaccount:agents:agent "system:node:ob-pki-control-plane --as-group=system:nodes" system:serviceaccount:lenny-system:lenny-controller; do
  o=$($A --as=$u patch pod st1 --subresource=status --type=merge -p "{\"metadata\":{\"labels\":{\"$L\":\"group-a\",\"lenny.dev/tenant-id\":\"evil\"}}}" 2>&1); rc=$?
  echo "as $u rc=$rc output=[$(echo $o | cut -c1-150)] labels now: $($A get pod st1 -o jsonpath='{.metadata.labels}')"
  $A delete pod st1 --wait=true >/dev/null; podj st1 | $A apply -f - >/dev/null
done
$A delete pod st1 --wait=false >/dev/null
echo "=================== VALIDATING ADMISSION POLICY in place of the webhook prototype"
$K delete validatingwebhookconfiguration ob-egress-group >/dev/null
$K apply -f k8s/vap.yaml >/dev/null; sleep 12
./s4-matrix.sh 2>&1
$K delete -f k8s/vap.yaml >/dev/null
echo "=================== DRAIN ON MISMATCH: repository code, real CRDs, real webhooks"
$K apply -f ../../../charts/lenny/crds/ >/dev/null
sleep 3
sed 's/EGRESSRES/["pods","pods\/status"]/' k8s/webcfg.tmpl | $K apply -f - >/dev/null; sleep 8
bin/drainh-host $R/kc-pki 2>&1 | cut -c1-220
$K -n agents get sandbox -o 'custom-columns=N:.metadata.name,P:.status.phase'
$K -n agents get pod -o 'custom-columns=N:.metadata.name,G:.metadata.labels.lenny\.dev/egress-group,D:.metadata.annotations.lenny\.dev/drain-request'
$K -n lenny-system logs ob-webhook | tail -3 | cut -c1-170
