#!/bin/bash
D=$(cd "$(dirname "$0")" && pwd)
C=(docker run --rm --network inj-pod -v "$D/shared":/s:ro -e HTTPS_PROXY=http://netagent:8080 -e CURL_CA_BUNDLE=/s/trust.pem curlimages/curl:latest)
for enc in none gzip auto deflate b64 hex split; do
  out=$("${C[@]}" -sS --compressed -D - "https://api.example.test/echo?enc=$enc")
  real=$(printf '%s' "$out" | grep -c "ACME-REAL" ); dec=""
  case $enc in b64) dec=$(printf '%s' "$out" | tail -1 | base64 -d 2>/dev/null | grep -c ACME-REAL);; hex) dec=$(printf '%s' "$out" | tail -1 | xxd -r -p 2>/dev/null | grep -c ACME-REAL);; esac
  echo "enc=$enc  lines-with-real-secret-visible=$real  decoded-reveals=${dec:-n/a}  echo-header=$(printf '%s' "$out" | grep -i '^x-echo-authorization' | tr -d '\r')"
done
