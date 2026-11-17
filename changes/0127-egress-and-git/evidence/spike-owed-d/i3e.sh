#!/bin/bash
# i3e.sh: end to end through the real netagent, once nothing else uses its networks.
. "$(dirname "$0")/lib-a.sh"
OUT=raw/i3-ca.txt; exec > >(tee -a $OUT) 2>&1
echo; echo "##### the end-to-end block again, alone on its networks, $(date)"
echo "### End to end: the real netagent's per-pod CA, Firefox and .NET through its proxy to a bound host, and a leaf from its key for a name outside"
a_up; srv -quiet; na --; docker logs od-na 2>&1 | grep -m1 per-pod | cut -c1-240
docker run --rm --name od-mk -v $O/bin:/b:ro -v $O/run:/run2 -v $O/i3:/i3 alpine:3.22 /b/ocmk leaf /run2/netagent/ca.pem /run2/priv/ca-key.pem /i3/certs/evil evil.example.test 2>&1 | tail -1
docker run -d --name od-evil --network od-up --ip 198.19.0.25 --network-alias evil.example.test -v $O/bin:/b:ro -v $O/i3:/i3:ro -v $O/certs:/certs:ro alpine:3.22 /b/ocup -quiet -cert /i3/certs/evil/tls.crt -key /i3/certs/evil/tls.key >/dev/null
/bin/sleep 1
T="https://api.example.test/echo https://evil.example.test/small"
echo "--- Firefox, CA through policy, the proxy set in preferences"
docker run --rm --name od-ff --network container:od-na -v $O/i3:/i3:ro -v $O/run/netagent:/run/netagent:ro -e CAS=/run/netagent/ca.pem -e PROXYPREF=1 -e SHOWSRC=1 od-firefox bash /i3/ff.sh policy $T 2>&1 | cut -c1-420
echo "--- Firefox, the design's profile only: HTTPS_PROXY, https_proxy, and SSL_CERT_FILE, with no policy"
docker run --rm --name od-ff --network container:od-na -v $O/i3:/i3:ro -v $O/run/netagent:/run/netagent:ro -e HTTPS_PROXY=http://127.0.0.1:3128 -e https_proxy=http://127.0.0.1:3128 -e SSL_CERT_FILE=/run/netagent/ca.pem -e SHOWSRC=1 od-firefox bash /i3/ff.sh env https://api.example.test/echo 2>&1 | cut -c1-420
for V in 8.0 10.0; do echo "--- .NET $V, the design's profile: HTTPS_PROXY and SSL_CERT_FILE at a bundle"
docker run --rm --name od-dn --network container:od-na -v $O/run/netagent:/run/netagent:ro -v od-dn$V:/app -e HTTPS_PROXY=http://127.0.0.1:3128 -e DOTNET_CLI_TELEMETRY_OPTOUT=1 mcr.microsoft.com/dotnet/sdk:$V sh -c "cat /etc/ssl/certs/ca-certificates.crt /run/netagent/ca.pem > /tmp/b.pem; export SSL_CERT_FILE=/tmp/b.pem; exec dotnet /app/t.dll observe '' $T" 2>&1 | cut -c1-420; done
docker rm -f od-evil od-na od-envoy od-authz1 od-authz2 od-up-srv >/dev/null 2>&1; docker network rm od-up od-pod >/dev/null 2>&1
echo done > raw/i3-done
