#!/bin/bash
. "$(dirname "$0")/lib.sh"
PERIOD=30; WIN=15; SYNC=3
echo "=== F2. stopped sync job with the retired check aimed at a host the retired generation could reach"
infra $PERIOD; SQMEM=1g squid correct-n $SYNC
for i in $(seq 1 24); do r=$(probe -window ${WIN}s); /bin/sleep 3; done
echo "healthy, details:"; probe -window ${WIN}s | jq -c 'select(.check|tostring|test("sync"))|{check,target,pass,detail}'
docker exec oa-squid sh /l1/killsync.sh; T0=$(python3 -c 'import time;print(time.time())'); first=""
for i in $(seq 1 25); do r=$(probe -window ${WIN}s); v=$(jq -r 'select(.verdict)|.verdict' <<<"$r"); t=$(python3 -c "import time;print(round(time.time()-$T0,1))"); printf 't=%ss %s %s\n' $t $v "$(jq -c 'select(.pass==false)|[.check,.detail]' <<<"$r" | tr '\n' ' ' | cut -c1-190)"; [ $v = FAIL ] && [ -z "$first" ] && first=$t; /bin/sleep 2; done
echo "first FAIL at t=$first s"
echo "=== H2. the check in front of Squid, Squid with 1 GiB and its memory sampled"
infra 300; SQMEM=1g squid correct-n 2
docker rm -f oa-agent oa-agentn >/dev/null 2>&1
docker run -d --name oa-agent --network oa-net --ip 198.19.50.70 --cpus 1 -m 128m -v $O/bin:/b:ro -v $O/certs:/c:ro alpine:3.22 /b/sniagent -quiet -proxy 198.19.50.50:3128 -proxy-ca /c/ca.pem -cred p1-acme.g1:acme-pw-1 >/dev/null
docker run -d --name oa-agentn --network oa-net --ip 198.19.50.71 --cpus 1 -m 128m -v $O/bin:/b:ro -v $O/certs:/c:ro alpine:3.22 /b/sniagent -quiet -check=false -proxy 198.19.50.50:3128 -proxy-ca /c/ca.pem -cred p1-acme.g1:acme-pw-1 >/dev/null
/bin/sleep 1
mem(){ docker stats --no-stream --format '{{.MemUsage}}' oa-squid | cut -d/ -f1; }
echo "idle: $(mem)"
for r in 1 2 3 4 5; do printf 'through Squid check    '; docker exec oa-hc /b/hc -proxy 198.19.50.70:3128 -bench 500 -sni target.oa.test; printf 'through Squid nocheck  '; docker exec oa-hc /b/hc -proxy 198.19.50.71:3128 -bench 500 -sni target.oa.test; echo "squid memory after $((r*1000)) tunnels: $(mem)"; done
/bin/sleep 20; echo "squid memory 20 s idle: $(mem)"; docker exec oa-squid sh -c 'cat /sys/fs/cgroup/memory.stat | grep -E "^(anon|file|sock|slab) "' | tr '\n' ' '; echo
docker exec oa-squid sh -c 'ls /proc/1/fd | wc -l; grep "open files" /proc/1/limits'; docker exec oa-squid tail -2 /tmp/access.log | cut -c1-120
