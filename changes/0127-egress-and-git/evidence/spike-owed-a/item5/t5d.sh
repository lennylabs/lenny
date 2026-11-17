#!/bin/bash
. "$(dirname "$0")/lib.sh"
echo "=== H3. the check in front of Squid, after fixing the stand-in's half-close on the TLS hop"
infra 300; SQMEM=1g squid correct-n 2
docker rm -f oa-agent oa-agentn >/dev/null 2>&1
docker run -d --name oa-agent --network oa-net --ip 198.19.50.70 --cpus 1 -m 128m -v $O/bin:/b:ro -v $O/certs:/c:ro alpine:3.22 /b/sniagent -quiet -proxy 198.19.50.50:3128 -proxy-ca /c/ca.pem -cred p1-acme.g1:acme-pw-1 >/dev/null
docker run -d --name oa-agentn --network oa-net --ip 198.19.50.71 --cpus 1 -m 128m -v $O/bin:/b:ro -v $O/certs:/c:ro alpine:3.22 /b/sniagent -quiet -check=false -proxy 198.19.50.50:3128 -proxy-ca /c/ca.pem -cred p1-acme.g1:acme-pw-1 >/dev/null
/bin/sleep 1
mem(){ docker stats --no-stream --format '{{.MemUsage}}' oa-squid | cut -d/ -f1; }
echo "idle: $(mem)"
for r in 1 2 3 4 5; do printf 'through Squid check    '; docker exec oa-hc /b/hc -proxy 198.19.50.70:3128 -bench 500 -sni target.oa.test; printf 'through Squid nocheck  '; docker exec oa-hc /b/hc -proxy 198.19.50.71:3128 -bench 500 -sni target.oa.test; echo "squid memory after $((r*1000)) tunnels: $(mem), open fds $(docker exec oa-squid sh -c 'ls /proc/1/fd | wc -l')"; done
echo "=== I. what real clients send for an upper-case or trailing-dot host: the agent compares the raw strings, then normalized"
docker rm -f oa-agent oa-agentn >/dev/null 2>&1
docker run -d --name oa-agent --network oa-net --ip 198.19.50.70 --cpus 1 -m 128m --add-host target.oa.test:198.19.50.20 --add-host target.oa.test.:198.19.50.20 -v $O/bin:/b:ro alpine:3.22 /b/sniagent -raw-compare >/dev/null
docker run -d --name oa-agentn --network oa-net --ip 198.19.50.71 --cpus 1 -m 128m --add-host target.oa.test:198.19.50.20 --add-host target.oa.test.:198.19.50.20 -v $O/bin:/b:ro alpine:3.22 /b/sniagent >/dev/null
/bin/sleep 1
for a in 70:raw 71:normalized; do ip=${a%:*}; for u in https://TARGET.OA.TEST/ https://target.oa.test./; do printf '%-10s %-28s ' ${a#*:} $u; docker exec oa-hc curl -sS --max-time 8 --cacert /c/ca.pem -x http://198.19.50.$ip:3128 -o /dev/null -w 'http=%{http_code}\n' $u 2>&1 | tail -1 | cut -c1-80; docker logs --tail 1 oa-agent$( [ $ip = 71 ] && echo n ) 2>&1 | jq -c '{authority,sni,result,reason}'; done; done
docker run --rm --name oa-c-py5 --network oa-net --add-host target.oa.test.:198.19.50.20 -v $O/certs:/c:ro -e HTTPS_PROXY=http://198.19.50.70:3128 oa-py python3 -c "
import requests
for u in ['https://TARGET.OA.TEST/','https://target.oa.test./']:
    try: print('requests raw',u,requests.get(u,verify='/c/ca.pem',timeout=8).status_code)
    except Exception as e: print('requests raw',u,'failed',repr(e)[:140])
" 2>&1 | cut -c1-200; docker logs --tail 2 oa-agent 2>&1 | jq -c '{authority,sni,result,reason}'
