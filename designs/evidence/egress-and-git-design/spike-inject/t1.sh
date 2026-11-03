#!/bin/bash
D=$(cd "$(dirname "$0")" && pwd)
C=(docker run --rm --network inj-pod -v "$D/shared":/s:ro -e HTTPS_PROXY=http://netagent:8080 -e HTTP_PROXY=http://netagent:8080 -e CURL_CA_BUNDLE=/s/trust.pem curlimages/curl:latest)
echo "== T1 bound host, placeholder header"; "${C[@]}" -sS -H "Authorization: Bearer lenny-ph-acme" https://api.example.test/acme/check
echo "== T1b bound host, no header at all"; "${C[@]}" -sS https://api.example.test/acme/check
echo "== T2 plain-HTTP upgrade"; "${C[@]}" -sS http://api.example.test/acme/check
echo "== T3 unbound host: header as sent, issuer"; "${C[@]}" -sS -H "Authorization: Bearer lenny-ph-acme" https://other.example.test/echo | grep -A1 '"Authorization"'; "${C[@]}" -sv -o /dev/null https://other.example.test/small 2>&1 | grep -i "issuer:"
echo "== T3b bound host issuer"; "${C[@]}" -sv -o /dev/null https://api.example.test/small 2>&1 | grep -i "issuer:"
echo "== T4 placeholder in query to bound host is not replaced"; "${C[@]}" -sS "https://api.example.test/echo?api_key=lenny-ph-acme" | grep '"uri"'
echo "== T5 HTTP/2 client side"; "${C[@]}" -sS --http2 -o /dev/null -w "%{http_version}\n" https://api.example.test/small
