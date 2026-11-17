#!/bin/bash
# t6.sh TAG [gen args] : SNI gate matrix.
cd "$(dirname "$0")"; TAG=${1:-v1.40.2}; shift
python3 gen.py envoy/g-sni.json sni=1 cbdfp=100000 "$@" && ./envoy.sh $TAG envoy/g-sni.json >/dev/null || exit 1
E1=$(docker logs r3-ech | awk '/ECHCONFIGLIST/{print $2}'); E2=$(docker logs r3-ech2 | awk '/ECHCONFIGLIST/{print $2}')
cl() { printf "  %-62s " "$1"; shift; docker run --rm --network sr3-pub -v $PWD/bin:/b:ro alpine:3.22 /b/snicl "$@" 2>&1 | tail -1; }
echo "## gen args: $*"
cl "authority target.spike:443, SNI target.spike"            -target target.spike:443 -sni target.spike
cl "authority target.spike:443, SNI evil.spike"              -target target.spike:443 -sni evil.spike
cl "authority target.spike:443, no SNI"                      -target target.spike:443 -sni ""
cl "authority target.spike:443, SNI TARGET.SPIKE"            -target target.spike:443 -sni TARGET.SPIKE
cl "authority TARGET.SPIKE:443, SNI target.spike"            -target TARGET.SPIKE:443 -sni target.spike
cl "authority 198.18.0.20:443, SNI target.spike"             -target 198.18.0.20:443 -sni target.spike
cl "authority internal.spike:443, SNI internal.spike (SSRF)" -target internal.spike:443 -sni internal.spike
cl "authority target.spike:80, plaintext HTTP (client-first)" -target target.spike:80 -mode plain
cl "authority target.spike:7000, server-first plaintext"     -target target.spike:7000 -mode wait
cl "ECH public=public.ech.spike, inner SNI target.spike"     -target target.spike:8443 -sni target.spike -ech $E1
cl "ECH public=target.spike, inner SNI evil.spike"           -target target.spike:8444 -sni evil.spike -ech $E2
cl "control: direct route, ECH public=public.ech.spike"      -cred agent:good -target target.spike:8443 -sni target.spike -ech $E1
curl -s localhost:19911/stats | grep -E "^sni_gate|tls_inspector\.(sni_found|sni_not_found|tls_found|tls_not_found|client_hello_too_large)|downstream_pre_cx_timeout|sni_inspect.downstream_cx_total" | grep -v ": 0$" | tr '\n' ' '; echo
W=11 ./alog.sh inner sni expect authority up_host flags dur_ms | grep sni_inspect
