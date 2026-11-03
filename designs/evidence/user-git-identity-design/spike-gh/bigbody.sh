#!/bin/bash
# bigbody.sh: real gh with large PR bodies through the strict filter. 65,536 characters is GitHub's stated limit; a stub cannot confirm it.
O=$(cd "$(dirname "$0")" && pwd); cd "$O"
docker exec gh-cli sh -c 'python3 -c "
import sys
for n,c,k in ((\"x\",\"x\",65536),(\"lt\",\"<\",65536),(\"quote\",chr(34),65536),(\"e\",chr(233),65536),(\"nl\",chr(10),65536),(\"emoji\",chr(0x1F600),65536),(\"lt-under\",\"<\",43000),(\"lt-over\",\"<\",44000)):
    open(\"/tmp/body-\"+n,\"w\",encoding=\"utf-8\").write(c*k)
"'
for n in x lt quote e nl emoji lt-under lt-over; do
 docker exec gh-cli sh -c "rm -rf /root/.cache/gh; curl -s http://gh-stub:9000/drain >/dev/null; curl -s 'http://gh-stub:9000/mode?existing=0' >/dev/null; cd /tmp/repo && GH_HOST=ghes.example.test GH_ENTERPRISE_TOKEN=lenny-injected gh pr create --title t --body-file /tmp/body-$n --head feature --base main" >/tmp/o.$$ 2>/tmp/e.$$; rc=$?
 s=$(docker exec gh-cli curl -s http://gh-stub:9000/drain | docker exec -i gh-cli python3 -c "
import sys,json
m=[json.loads(l) for l in sys.stdin]
print(' '.join(str(r['bodyLen']) for r in m if any(o['type']=='mutation' for g in r.get('gql',[]) for o in g.get('ops',[]))) or 'none')" 2>/dev/null)
 printf 'body-%-9s rc=%s mutationBodyBytesAtStub=%s  %s\n' $n $rc "$s" "$(tail -1 /tmp/e.$$ | cut -c1-150)"
done
rm -f /tmp/o.$$ /tmp/e.$$
