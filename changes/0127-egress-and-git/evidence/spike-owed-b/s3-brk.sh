#!/bin/bash
# Timeline for one stamp on Cilium at grace 5 s, from the agent logs of both nodes, before the next run replaces the workload.
R=spike-owed-b; cd $R; . ./kc.sh cilium
for pod in $($K -n kube-system get pod -l k8s-app=cilium -o name); do n=$($K -n kube-system get $pod -o jsonpath='{.spec.nodeName}'); $K -n kube-system logs $pod -c cilium-agent > out/s3-cilium-default/agent-$n.log; echo "$n $(grep -c 'Identity of endpoint changed' out/s3-cilium-default/agent-$n.log) identity changes; regen lines $(grep -c -i regenerat out/s3-cilium-default/agent-$n.log)"; done
grep -E "agents/x3|agents/s3" out/s3-cilium-default/agent-ob-cilium-worker.log | grep -i -E "identity|Resolving|Assigned|Reserved|regenerat" | cut -c1-230 | tail -8
cat out/s3-cilium-default/x3.stamp out/s3-cilium-default/s3.stamp | cut -c1-90
grep STATE out/s3-cilium-default/x3.log | cut -c1-60
python3 -c "
import datetime
for f in ('x3','s3'):
    s=open('out/s3-cilium-default/%s.stamp'%f).read(); import re
    print(f, datetime.datetime.fromtimestamp(int(re.search(r'end=(\d+)',s)[1])/1e9,datetime.timezone.utc).isoformat())"
$K -n kube-system exec ds/cilium -c cilium-agent -- cilium-dbg metrics list 2>/dev/null | grep -E "cilium_endpoint_regeneration_time_stats_seconds_(sum|count).*scope=\"total\"|cilium_policy_regeneration_total|cilium_identity" | cut -c1-120 | head -6
docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}}' | grep ob-cilium
