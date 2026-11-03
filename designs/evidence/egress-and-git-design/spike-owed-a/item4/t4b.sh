#!/bin/bash
# Item 4 additions: the check's own time, bridge MTU, inner Host fronting, and Encrypted Client Hello.
D=$(cd "$(dirname "$0")" && pwd); O=$D/..; S=$O/..; PX=http://198.19.50.70:3128
echo "=== the check's own time, from the agent's log"
docker logs oa-agent 2>&1 | jq -c 'select(.result=="pass") | [.check_us,.parse_us,.reads,.hello_bytes]' | python3 -c "
import sys,json
r=[json.loads(l) for l in sys.stdin]
c=sorted(x[0] for x in r if x[2]==3); p=sorted(x[1] for x in r)
print('passing tunnels',len(r),'hello delivered whole',len(c),'first byte to decision us p50',c[len(c)//2],'p99',c[int(len(c)*.99)],'parser alone us p50',p[len(p)//2],'p99',p[int(len(p)*.99)])
print('hello sizes seen, bytes',sorted(set(x[3] for x in r))[:16])"
echo "=== MTU of the client's interface"; docker exec oa-hc cat /sys/class/net/eth0/mtu
echo "=== matching SNI, inner Host naming another host (check is on the server name only)"
docker exec oa-hc curl -sS --max-time 8 --cacert /c/ca.pem -x $PX -H 'Host: other.oa.test' https://target.oa.test/
docker logs --tail 1 oa-agent 2>&1 | jq -c '{result,sni}'
echo "=== Encrypted Client Hello: outer name equals the authority, inner name differs"
docker rm -f oa-ech >/dev/null 2>&1
docker run -d --name oa-ech --network oa-net --ip 198.19.50.25 --network-alias ech-public.oa.test --cpus 0.5 -m 64m -v $S/spike-tls/bin:/b:ro alpine:3.22 /b/snicl -serve :443 -public ech-public.oa.test >/dev/null; /bin/sleep 1
docker logs oa-ech 2>&1 | head -3 | cut -c1-160
E=$(docker logs oa-ech 2>&1 | grep -oE '[A-Za-z0-9+/=]{40,}' | head -1)
docker exec oa-hc sh -c "true"; docker run --rm --name oa-c-ech --network oa-net -v $S/spike-tls/bin:/b:ro alpine:3.22 /b/snicl -proxy 198.19.50.70:3128 -mode tls -target ech-public.oa.test:443 -sni secret.oa.test -ech "$E" 2>&1 | tail -2 | cut -c1-200
docker logs --tail 1 oa-agent 2>&1 | jq -c '{authority,result,reason,sni,hello_bytes}'
docker logs oa-ech 2>&1 | tail -2 | cut -c1-200
docker rm -f oa-ech >/dev/null
