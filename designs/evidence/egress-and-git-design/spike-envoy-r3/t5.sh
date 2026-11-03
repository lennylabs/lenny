#!/bin/bash
# t5.sh TAG : what the client sees when the chained upstream proxy answers 407 or 403, per hygiene variant.
cd "$(dirname "$0")"; TAG=${1:-v1.40.2}
raw() { docker run --rm --network sr3-pub alpine:3.22 sh -c "(printf 'CONNECT target.spike:443 HTTP/1.1\r\nHost: target.spike:443\r\nProxy-Authorization: Basic $(printf agent:$1 | base64)\r\n\r\n'; sleep 1.5) | nc -w 3 198.18.0.10 3128" | sed 's/^/      /'; }
for v in "hygiene=0" "hygiene=1" "hygiene=2" ${EXTRA}; do
  python3 gen.py envoy/g-chain.json $v && ./envoy.sh $TAG envoy/g-chain.json >/dev/null || { echo "$v: envoy failed"; docker logs r3-envoy 2>&1 | grep -i "error\|critical" | tail -3; continue; }
  echo "## $v: upstream proxy B answers 407 (chain-407)"; raw chain-407
  echo "## $v: upstream proxy D answers 403 (chain-403)"; raw chain-403
done
echo "## proxy B / D last requests"; docker logs r3-proxyb 2>&1 | grep UPSTREAM | tail -2; docker logs r3-proxyd 2>&1 | tail -2
W=11 ./alog.sh code flags details up_host resp_pa
