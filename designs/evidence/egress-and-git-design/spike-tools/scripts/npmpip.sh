#!/bin/bash
cd "$(dirname "$0")/.."
N=(docker run --rm --network spk-int -u 1000:1000 -e HOME=/tmp -v $PWD/shared/npm:/cfg:ro -v $PWD/certs:/certs:ro -e NPM_CONFIG_USERCONFIG=/cfg/npmrc -e NPM_CONFIG_UPDATE_NOTIFIER=false)
w() { printf "registry=https://npm.example.test/\n//npm.example.test/:_authToken=$1\ncafile=/certs/ca.pem\n" > shared/npm/npmrc; }
"${N[@]}" node:20 sh -c 'node -v; npm -v'
w tok-N1; echo "== N1"; "${N[@]}" node:20 npm view spkpkg version; echo " exit=$?"
w tok-N2; echo "== N2 (re-read)"; "${N[@]}" node:20 npm view spkpkg version; echo " exit=$?"
echo "== N3 HTTPS_PROXY env"; "${N[@]}" -e HTTPS_PROXY=http://proxy:3128 node:20 npm view spkpkg version; echo " exit=$?"
echo "== N4 no cafile, NODE_EXTRA_CA_CERTS"; printf "registry=https://npm.example.test/\n//npm.example.test/:_authToken=tok-N4\n" > shared/npm/npmrc; "${N[@]}" -e NODE_EXTRA_CA_CERTS=/certs/ca.pem node:20 npm view spkpkg version; echo " exit=$?"
echo "== N5 no cafile, SSL_CERT_FILE only"; "${N[@]}" -e SSL_CERT_FILE=/certs/ca.pem node:20 npm view spkpkg version 2>&1 | grep -iE 'error|cert|^1' | head -3; echo
P=(docker run --rm --network spk-int -u 1000:1000 -e HOME=/tmp -v $PWD/shared/pip:/cfg:ro -v $PWD/certs:/certs:ro -e PIP_CONFIG_FILE=/cfg/pip.conf -e PIP_DISABLE_PIP_VERSION_CHECK=1 -e PIP_NO_CACHE_DIR=1)
wp() { printf "[global]\nindex-url = https://user:$1@pypi.example.test/simple\ncert = /certs/ca.pem\n" > shared/pip/pip.conf; }
"${P[@]}" python:3.13-alpine pip --version
wp tok-P1; echo "== P1"; "${P[@]}" python:3.13-alpine pip download --no-deps -d /tmp/d spkpkg 2>&1 | tail -2; echo
wp tok-P2; echo "== P2 (re-read)"; "${P[@]}" python:3.13-alpine pip download --no-deps -d /tmp/d spkpkg 2>&1 | tail -2; echo
echo "== P3 HTTPS_PROXY env"; "${P[@]}" -e HTTPS_PROXY=http://proxy:3128 python:3.13-alpine pip download --no-deps -d /tmp/d spkpkg 2>&1 | tail -1; echo
