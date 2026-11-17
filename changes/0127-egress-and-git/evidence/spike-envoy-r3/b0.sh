#!/bin/bash
# b0.sh <tag> : baseline suite (previous spike's final.yaml) against the old authz decision table.
cd "$(dirname "$0")"
TAG=${1:-v1.40.2}
./envoy.sh $TAG envoy/final.yaml || exit 1
echo "## allow (good) https through tunnel"; ./t.sh agent:good -k https://target.spike/
echo "## deny (bad)"; ./t.sh agent:bad -k https://target.spike/
echo "## no credential"; docker exec r3-client sh -c 'printf "CONNECT target.spike:443 HTTP/1.1\r\nHost: target.spike:443\r\n\r\n" | nc -w 2 198.18.0.10 3128'
echo "## blocked host"; ./t.sh agent:good -k https://blocked.example.com/
echo "## chain-a (http_11_proxy transport socket via proxy A)"; ./t.sh agent:chain-a -k https://target.spike/
echo "## chain-b (forwarded CONNECT to proxy B, credential injected)"; ./t.sh agent:chain-b -k https://target.spike/
echo "## chain-o (ORIGINAL_DST from header)"; ./t.sh agent:chain-o -k https://target.spike/
echo "## chain-m (ORIGINAL_DST from ext_authz dynamic metadata)"; ./t.sh agent:chain-m -k https://target.spike/
echo "## proxy B log (last 6)"; docker logs r3-proxyb 2>&1 | grep UPSTREAM | tail -6
echo "## fail-closed: authz stopped"; docker stop r3-authz >/dev/null; ./t.sh agent:good -k https://target.spike/; docker start r3-authz >/dev/null; sleep 1
echo "## after authz restart"; ./t.sh agent:good -k https://target.spike/
echo "## access log"; docker logs r3-envoy 2>&1 | grep '^{' | python3 -c "
import json,sys
for l in sys.stdin:
  d=json.loads(l); print(d['authority'],d['code'],d['flags'],d['details'],'up='+d['up_host'],d['up_cluster'],'tenant='+d['tenant'],'reason='+d['deny_reason'])"
