#!/bin/bash
R=spike-owed-b; cd $R; . ./kc.sh pki
$K -n lenny-system create configmap envoy-boot --from-file=nomcd.json=k8s/envoy-nomcd.json --from-file=mcd.json=k8s/envoy-mcd.json
$K -n lenny-system create configmap envoy-sds --from-file=sds.yaml=k8s/sds.yaml --from-file=sds-ca.yaml=k8s/sds-ca.yaml
$K apply -f k8s/core.yaml
sed -e 's/NAME/envoy/;s/APP/envoy/;s/BOOT/nomcd.json/;s/SECRET/lenny-egress-tls/;s/OPTIONAL/false/' k8s/envoy.tmpl | $K apply -f -
$K -n lenny-system wait pod --all --for=condition=Ready --timeout=120s
$K -n lenny-system get pods -o wide
$K -n lenny-system logs ctrl | tail -3
$K -n lenny-system logs envoy -c envoy | grep -i -E "warn|error|sds|secret" | head -12 | cut -c1-220
$K -n lenny-system run -i --rm oneoff --image=ob-tools:1 --image-pull-policy=Never --restart=Never --overrides='{"spec":{"volumes":[{"name":"ca","configMap":{"name":"lenny-egress-ca"}}],"containers":[{"name":"oneoff","image":"ob-tools:1","imagePullPolicy":"Never","command":["tcl","-proxy","lenny-egress.lenny-system.svc:3128","-target","target.lenny-system.svc.cluster.local:7000","-cred","agent:good","-rate","10","-n","10","-hold","100ms","-bucket","60s","-proxy-tls","-proxy-ca","/ca/ca.crt"],"volumeMounts":[{"name":"ca","mountPath":"/ca"}]}]}}' 2>&1 | tail -3 | cut -c1-200
