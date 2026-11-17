#!/bin/bash
D="spike-inject"
B=(docker run --rm --network inj-pod -v "$D/shared":/s:ro -e HTTPS_PROXY=http://netagent:8080 -e https_proxy=http://netagent:8080)
echo "== gh"; "${B[@]}" -v "spike-tools/dl/gh/gh_2.103.3_linux_arm64/bin/gh":/usr/local/bin/gh:ro -e GH_HOST=ghes.example.test -e GH_ENTERPRISE_TOKEN=lenny-ph-gh -e SSL_CERT_FILE=/s/trust.pem -e GH_CONFIG_DIR=/tmp/gh alpine:3.22 gh api user 2>&1 | tail -2
echo "== npm"; "${B[@]}" -e NODE_EXTRA_CA_CERTS=/s/trust.pem node:24-slim sh -c 'mkdir /tmp/p && cd /tmp/p && printf "registry=https://registry.example.test/\n//registry.example.test/:_authToken=lenny-ph-npm\n" > .npmrc && npm install spike-leftpad --no-audit --no-fund --loglevel=error && node -e "console.log(\"npm-installed\", require(\"spike-leftpad\")(\"x\",3))"' 2>&1 | tail -3
echo "== pip"; "${B[@]}" -e PIP_CERT=/s/trust.pem inj-py sh -c 'pip install -q --disable-pip-version-check --index-url https://pypiuser:lenny-ph-pypi@pypi.example.test/simple/ spikepkg && python -c "import spikepkg;print(\"pip-installed\", spikepkg.VALUE)"' 2>&1 | tail -3
