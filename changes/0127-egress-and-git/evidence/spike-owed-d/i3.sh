#!/bin/bash
# i3.sh: the name-constrained per-pod CA on Firefox and on .NET's HttpClient.
. "$(dirname "$0")/lib-a.sh"
OUT=raw/i3-ca.txt; exec > >(tee -a $OUT) 2>&1
pyup; rm -rf $O/i3/certs; docker run --rm --name od-gen -v $O/bin:/b:ro -v $O/i3:/i3 alpine:3.22 /b/camat gen /i3/certs
docker network create --subnet 198.19.60.0/24 od-ca >/dev/null 2>&1
docker rm -f od-casrv >/dev/null 2>&1; docker run -d --name od-casrv --network od-ca --ip 198.19.60.20 -v $O/bin:/b:ro -v $O/i3:/i3:ro alpine:3.22 /b/camat serve /i3/certs >/dev/null
docker run --rm --name od-x509 -v $O/i3:/i3:ro alpine:3.22 sh -c 'apk add -q openssl >/dev/null 2>&1; openssl x509 -in /i3/certs/ca-nc.pem -noout -text | grep -A6 "Name Constraints"'
H="--add-host perm.nc.test:198.19.60.20 --add-host sub.perm.nc.test:198.19.60.20 --add-host denied.other.test:198.19.60.20 --add-host x.nc.test:198.19.60.20"
URLS="https://perm.nc.test:8441/ https://denied.other.test:8442/ https://sub.perm.nc.test:8443/ https://198.19.60.20:8444/ https://denied.other.test:8445/ https://denied.other.test:8446/ https://perm.nc.test:8447/ https://198.19.60.20:8448/ https://denied.other.test:8449/ https://x.nc.test:8450/"
CAS=/i3/certs/ca-nc.pem:/i3/certs/ca-ncp.pem:/i3/certs/ca-ncdns.pem:/i3/certs/ca-ctl.pem
reqs(){ docker logs od-casrv 2>&1 | grep -c REQUEST; }
echo "### Firefox"
docker image inspect od-firefox >/dev/null 2>&1 || docker build -q -t od-firefox -f i3/Dockerfile.firefox i3 | tail -1
docker run --rm --name od-ff --network od-ca $H -v $O/i3:/i3:ro -e CAS=$CAS od-firefox bash /i3/ff.sh policy $URLS 2>&1 | cut -c1-330
echo "server requests so far: $(reqs); by case:"; docker logs od-casrv 2>&1 | grep REQUEST | sed 's/.*case=\([^ ]*\).*/\1/' | sort | uniq -c
echo "--- CA through NSS (certutil into the profile), no policy"
docker run --rm --name od-ff --network od-ca $H -v $O/i3:/i3:ro -e CAS=$CAS od-firefox bash /i3/ff.sh nss $URLS 2>&1 | cut -c1-330
echo "--- CA only in SSL_CERT_FILE: the design's trust variables"
docker run --rm --name od-ff --network od-ca $H -v $O/i3:/i3:ro -e SSL_CERT_FILE=/i3/certs/ca-nc.pem od-firefox bash /i3/ff.sh env https://perm.nc.test:8441/ 2>&1 | cut -c1-330
echo "### .NET"
for V in 8.0 10.0; do
  echo "--- SDK $V"
  TF=net$V
  docker run --rm --name od-dn-build -v $O/i3/dotnet:/src:ro -v od-dn$V:/out -e DOTNET_CLI_TELEMETRY_OPTOUT=1 -e DOTNET_NOLOGO=1 mcr.microsoft.com/dotnet/sdk:$V sh -c "rm -rf /tmp/s; cp -R /src /tmp/s; cd /tmp/s; dotnet publish -v q -c Release -f $TF -p:TF=$TF -o /out 2>&1 | tail -2" 2>&1 | cut -c1-200
  # the bundle is the design's: public roots plus the per-pod CA, through SSL_CERT_FILE
  RUN="docker run --rm --name od-dn --network od-ca $H -v $O/i3:/i3:ro -v od-dn$V:/app -e DOTNET_CLI_TELEMETRY_OPTOUT=1 mcr.microsoft.com/dotnet/sdk:$V"
  BUNDLE="cat /etc/ssl/certs/ca-certificates.crt /i3/certs/ca-nc.pem /i3/certs/ca-ncp.pem /i3/certs/ca-ncdns.pem /i3/certs/ca-ctl.pem > /tmp/bundle.pem"
  echo "   platform trust, SSL_CERT_FILE at a bundle"
  $RUN sh -c "$BUNDLE; export SSL_CERT_FILE=/tmp/bundle.pem; exec dotnet /app/t.dll observe '' $URLS" 2>&1 | cut -c1-420
  echo "   CustomRootTrust with those CAs"
  $RUN sh -c "$BUNDLE; exec dotnet /app/t.dll custom ${CAS} $URLS" 2>&1 | cut -c1-420
  echo "   SSL_CERT_DIR alone (the design says this replaces the search path): public hosts or the CA"
  $RUN sh -c "mkdir -p /tmp/d; cp /i3/certs/ca-nc.pem /tmp/d/nc.pem; cd /tmp/d; openssl rehash . 2>/dev/null || true; export SSL_CERT_DIR=/tmp/d; exec dotnet /app/t.dll observe '' https://perm.nc.test:8441/ https://denied.other.test:8442/ https://denied.other.test:8446/" 2>&1 | cut -c1-420
done
echo "server requests by case, whole run:"; docker logs od-casrv 2>&1 | grep REQUEST | sed 's/.*case=\([^ ]*\).*/\1/' | sort | uniq -c
docker rm -f od-casrv >/dev/null; docker network rm od-ca >/dev/null 2>&1
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
