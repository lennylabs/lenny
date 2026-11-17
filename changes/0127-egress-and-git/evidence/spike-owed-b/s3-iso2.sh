#!/bin/bash
R=spike-owed-b; cd $R; . ./kc.sh cilium
for pod in $($K -n kube-system get pod -l k8s-app=cilium --field-selector spec.nodeName=ob-cilium-worker -o name); do
  $K -n kube-system logs $pod -c cilium-agent > out/s3-cilium-iso-grace0/agent-worker.log
done
grep -E "agents/b0" out/s3-cilium-iso-grace0/agent-worker.log | sed -E 's/level=info //' | cut -c1-120 | tail -12
python3 -c "
import datetime,re
s=open('out/s3-cilium-iso-grace0/b0.stamp').read();print('b0 stamp end',datetime.datetime.fromtimestamp(int(re.search(r'end=(\d+)',s)[1])/1e9,datetime.timezone.utc).isoformat())"
sed -n '1,200p' out/s3-cilium-iso-grace0/agent-worker.log | grep -c . 
$K -n kube-system get cm cilium-config -o yaml | grep -E "k8s-client|identity-|enable-cilium-endpoint-slice|policy-trigger|endpoint-queue" | head
grep -n -i -E "rate.?limit|throttl|retry|backoff|fail" out/s3-cilium-iso-grace0/agent-worker.log | tail -5 | cut -c1-200
