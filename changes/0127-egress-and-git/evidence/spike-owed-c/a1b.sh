#!/bin/bash
# a1b.sh: behaviour of the design's text, one check per statement.
O=$(cd "$(dirname "$0")" && pwd); cd "$O"; SP=$(dirname "$O")
ENV=(-e HTTPS_PROXY=http://127.0.0.1:3128 -e HTTP_PROXY=http://127.0.0.1:3128 -e http_proxy=http://127.0.0.1:3128 -e CURL_CA_BUNDLE=/s/trust.pem)
C=(docker run --rm --network container:oc-na -v $O/run/trust:/s:ro "${ENV[@]}" curlimages/curl:latest)
PY=(docker run --rm --network container:oc-na -v $O/run/trust:/s:ro -v $O:/w:ro oc-py python -I /w/a1b.py)
upl(){ docker logs --since "$1" oc-up-srv 2>&1 | grep REQ | sed 's/^REQ //' | python3 -c "
import sys,json
for l in sys.stdin:
    m=json.loads(l); h=m['headers']
    print('  upstream saw host=%s proto=%s uri=%s Authorization=%s Proxy-Connection=%s X-Hop=%s' % (m['host'],m['proto'],m['uri'],h.get('Authorization'),h.get('Proxy-Connection'),h.get('X-Hop')))"; }
echo "### authority, hop-by-hop, WebSocket"; S=$(date +%s); "${PY[@]}" 2>&1 | cut -c1-170; upl $S
echo; echo "### HTTP/2: :authority differs on the same connection"; S=$(date +%s)
"${C[@]}" -sS --http2 -o /dev/null -w 'h2 Host attacker.example: %{http_version} %{http_code}\n' -H 'Host: attacker.example' -H 'Authorization: Bearer lenny-injected' https://api.example.test/echo; "${C[@]}" -sS --http2 -o /dev/null -w 'h2 bound: %{http_version} %{http_code}\n' -H 'Authorization: Bearer lenny-injected' https://api.example.test/echo; upl $S
echo; echo "### placeholder in a query or a body is never replaced"; S=$(date +%s); "${C[@]}" -sS 'https://api.example.test/echo?api_key=lenny-injected' -o /dev/null; upl $S
echo; echo "### scrub table: curl --compressed (advertises deflate, gzip, br, zstd); counts of secret-bearing lines the client saw"
for enc in none gzip auto deflate split zstd-auto zstd b64 hex; do
 out=$("${C[@]}" -sS --compressed -D - "https://api.example.test/echo?enc=$enc"); rc=$?
 real=$(printf '%s' "$out" | grep -c ACME-REAL); dec=""; stat=$(printf '%s' "$out" | grep '^HTTP' | tail -1 | tr -d '\r')
 echo "enc=$enc rc=$rc status=[$stat] lines-with-secret=$real masked-lines=$(printf '%s' "$out" | grep -c 'xxxxxxxx') x-echo=[$(printf '%s' "$out" | grep -i '^x-echo-authorization' | tr -d '\r' | cut -c1-60)]"
done
for enc in b64 hex; do body=$("${C[@]}" -sS "https://api.example.test/echo?enc=$enc" | tail -1); case $enc in b64) d=$(printf '%s' "$body" | base64 -d 2>/dev/null | grep -c ACME-REAL);; hex) d=$(printf '%s' "$body" | python3 -c "import sys,binascii;print(binascii.unhexlify(sys.stdin.read().strip()).decode())" | grep -c ACME-REAL);; esac; echo "enc=$enc decoded-body-lines-with-secret=$d (stated residual)"; done
echo; echo "### without the Accept-Encoding rewrite and the refusal of other encodings (-lax-encoding), which the design does not state"; ./a-na.sh -- -lax-encoding >/dev/null
for enc in zstd-auto zstd; do out=$("${C[@]}" -sS --compressed -D - "https://api.example.test/echo?enc=$enc"); echo "enc=$enc rc=$? lines-with-secret=$(printf '%s' "$out" | grep -v -i '^x-echo' | grep -c ACME-REAL) (header masked: $(printf '%s' "$out" | grep -ci '^x-echo.*xxxx'))"; done; ./a-na.sh >/dev/null
echo; echo "### a Basic attach template: the reflected rendered header holds the secret in base64"
"${C[@]}" -sS -D - -u pypiuser:lenny-injected "https://pypi.example.test/echo" | grep -i 'x-echo-authorization\|"Authorization"' -A1 | cut -c1-90 | tr -d '\r'
echo "--- with scrubbing of the exact secret only (-scrub-rendered=false)"; ./a-na.sh -- -scrub-rendered=false >/dev/null
"${C[@]}" -sS -D - -u pypiuser:lenny-injected "https://pypi.example.test/echo" | grep -i 'x-echo-authorization' | tr -d '\r' | cut -c1-90 | tee /tmp/oc-basic.txt; awk '{print $NF}' /tmp/oc-basic.txt | base64 -d 2>/dev/null; echo; rm -f /tmp/oc-basic.txt; ./a-na.sh >/dev/null
echo; echo "### truncated upstream body through the scrubber"; "${C[@]}" -sS --http1.1 -o /dev/null -w 'h1 rc=%{exitcode} code=%{http_code} bytes=%{size_download}\n' https://api.example.test/cut 2>&1 | tail -2 | cut -c1-120; "${C[@]}" -sS --http2 -o /dev/null -w 'h2 rc=%{exitcode} code=%{http_code} bytes=%{size_download}\n' https://api.example.test/cut 2>&1 | tail -2 | cut -c1-120
echo; echo "### upstream certificate signed by the per-pod CA is refused"
GOOS= bash -c "cd go && GOCACHE=$O/.gocache GOPROXY=off GOFLAGS=-mod=mod go run ./cmd/ocmk leaf $O/run/priv/ca.pem $O/run/priv/ca-key.pem $O/run/hostile hostile.example.test evil.example.test"
docker rm -f oc-hostile >/dev/null 2>&1; docker run -d --name oc-hostile --network oc-up --ip 198.19.0.22 --network-alias hostile.example.test --network-alias evil.example.test -v $O/bin:/b:ro -v $O/run/hostile:/h:ro alpine:3.22 /b/ocup -cert /h/tls.crt -key /h/tls.key >/dev/null; /bin/sleep 1
"${C[@]}" -sS -w ' status=%{http_code}\n' https://hostile.example.test/echo 2>&1 | tail -2 | cut -c1-120; docker logs oc-na 2>&1 | grep 'hostile' | tail -1 | cut -c1-200; echo "  requests the hostile upstream received: $(docker logs oc-hostile 2>&1 | grep -c REQ)"
echo; echo "### a leaf from the leaked per-pod CA for a name outside the constraints, reached through an opaque tunnel"
"${C[@]}" -sS https://evil.example.test/small 2>&1 | head -2 | cut -c1-200
openssl x509 -in run/priv/ca.pem -noout -ext nameConstraints,basicConstraints 2>&1 | head -30 | cut -c1-100
echo; echo "### wrong trust on I2: netagent with the public CA as its egress bundle, then a wrong server name"
./a-na.sh -- -proxy-ca /certs/pub-ca.pem >/dev/null; "${C[@]}" -sS -o /dev/null -w 'wrong CA, bound host: %{http_code}\n' https://api.example.test/small 2>&1 | tail -1 | cut -c1-120; "${C[@]}" -sS -o /dev/null -w 'wrong CA, opaque: %{http_code}\n' https://other.example.test/small 2>&1 | tail -1 | cut -c1-120; docker logs oc-na 2>&1 | grep -m1 upstream | cut -c1-170
docker rm -f oc-na >/dev/null; mkdir -p run; docker run -d --name oc-na --network oc-pod --ip 198.19.1.10 -v $O/bin:/b:ro -v $O/certs:/certs:ro -v $O/cfg-bindings.json:/cfg/bindings.json:ro -v $O/run/netagent:/run/netagent -v $O/run/priv:/var/netagent alpine:3.22 /b/netagent -proxy 198.19.1.41:3128 -proxy-ca /certs/egress-ca.pem -proxy-sni wrong.lenny-system.svc >/dev/null; /bin/sleep 1; "${C[@]}" -sS -o /dev/null -w 'wrong server name, bound: %{http_code}\n' https://api.example.test/small 2>&1 | tail -1 | cut -c1-120; docker logs oc-na 2>&1 | grep -m1 upstream | cut -c1-170
echo; echo "### CA persistence across a container restart, private directory modes"; ./a-na.sh >/dev/null; a=$(docker exec oc-na sha256sum /run/netagent/ca.pem | cut -c1-16); docker restart oc-na >/dev/null; /bin/sleep 1; b=$(docker exec oc-na sha256sum /run/netagent/ca.pem | cut -c1-16); echo "before=$a after=$b"; docker exec oc-na ls -la /var/netagent | cut -c1-70
docker rm -f oc-hostile >/dev/null
echo; echo "### counters"; docker exec oc-na wget -qO- http://127.0.0.1:9100/stats | cut -c1-400
