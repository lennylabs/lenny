#!/bin/bash
D=$(cd "$(dirname "$0")" && pwd)
B=(docker run --rm --network inj-pod -v "$D/shared":/s:ro -v "$D/clients":/c:ro -e HTTPS_PROXY=http://netagent:8080 -e https_proxy=http://netagent:8080 -e NO_PROXY= -e ACME_API_KEY=lenny-ph-acme)
"${B[@]}" -e REQUESTS_CA_BUNDLE=/s/trust.pem -e SSL_CERT_FILE=/s/trust.pem inj-py python /c/py.py 2>&1 | tail -5
"${B[@]}" -e NODE_USE_ENV_PROXY=1 -e NODE_EXTRA_CA_CERTS=/s/pod-ca.pem -e NODE_EXTRA_CA_CERTS=/s/trust.pem node:24-slim node /c/node.mjs 2>&1 | tail -2
"${B[@]}" -e SSL_CERT_FILE=/s/trust.pem -e GOCACHE=/tmp/gc -e GOFLAGS=-mod=mod -w /c/goc golang:1.26 sh -c 'cp -r /c/goc /tmp/g && cd /tmp/g && go mod init g >/dev/null 2>&1; go run .' 2>&1 | tail -2
"${B[@]}" -e JAVA_TOOL_OPTIONS="-Dhttps.proxyHost=netagent -Dhttps.proxyPort=8080 -Djavax.net.ssl.trustStore=/s/trust.p12 -Djavax.net.ssl.trustStorePassword=changeit -Djavax.net.ssl.trustStoreType=PKCS12" eclipse-temurin:21-jdk java /c/J.java 2>&1 | grep -v "Picked up" | tail -2
