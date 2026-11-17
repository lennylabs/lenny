#!/bin/bash
# a-matrix.sh plain|tls : one line per feature.
cd "$(dirname "$0")"; D=$PWD; M=$1
X=""; [ "$M" = tls ] && X="-proxy-tls -proxy-ca /c/ca1.pem"
cl(){ printf "%-44s " "$1"; shift; docker run --rm --network sr3-pub -v $D/bin:/b:ro -v $D/certs:/c:ro alpine:3.22 /b/snicl $X "$@" 2>&1 | tail -1 | sed -E 's/[0-9.]+(µs|ms|s)\b/T/g' | cut -c1-100; }
cl "direct allow, inner TLS"            -cred agent:good -target target.spike:443 -sni target.spike
cl "deny token"                          -cred agent:bad -target target.spike:443 -sni target.spike
cl "no credential"                       -cred "" -target target.spike:443 -sni target.spike
cl "blocked host"                        -cred agent:good -target blocked.example.com:443 -sni blocked.example.com
cl "chain forwarded, cred injected"      -cred agent:chain-m -target target.spike:443 -sni target.spike
cl "chain upstream 407"                  -cred agent:chain-407 -target target.spike:443 -sni target.spike
cl "chain upstream 403"                  -cred agent:chain-403 -target target.spike:443 -sni target.spike
cl "server-name check, match"            -cred agent:sni -target target.spike:443 -sni target.spike
cl "server-name check, mismatch"         -cred agent:sni -target target.spike:443 -sni evil.spike
cl "server-name check, no name"          -cred agent:sni -target target.spike:443 -sni ""
cl "server-name check, plaintext"        -cred agent:sni -target target.spike:80 -mode plain
cl "SSRF name to private"                -cred agent:good -target internal.spike:443 -sni internal.spike
cl "SSRF name to loopback"               -cred agent:good -target evil.spike:443 -sni evil.spike
cl "SSRF name to metadata"               -cred agent:good -target meta.spike:443 -sni meta.spike
cl "SSRF literal loopback"               -cred agent:good -target 127.0.0.1:443 -sni x
cl "SSRF mapped literal"                 -cred agent:good -target "[::ffff:127.0.0.1]:443" -sni x
cl "IPv6 literal public"                 -cred agent:good -target "[2001:db8:2::20]:443" -sni x
docker stop -t0 r3-authz >/dev/null; /bin/sleep 1
cl "decision service stopped"            -cred agent:good -target target.spike:443 -sni target.spike
docker start r3-authz >/dev/null; /bin/sleep 2
cl "decision service back"               -cred agent:good -target target.spike:443 -sni target.spike
echo "-- chained upstream saw (last):"; docker logs r3-proxyb 2>&1 | grep -i upstream | tail -1 | cut -c1-140
echo "-- access log peer of last direct allow:"; docker logs r3-envoy 2>&1 | grep '^{' | grep target.spike | tail -1 | python3 -c "import json,sys
for l in sys.stdin:
  d=json.loads(l); print({k:d.get(k) for k in ('peer','downstream','code','tenant','up_host') if k in d})" 2>/dev/null
