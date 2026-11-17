#!/bin/bash
# a1.sh: the client matrix against the Go netagent, with a name-constrained per-pod CA.
O=$(cd "$(dirname "$0")" && pwd); cd "$O"; SP=$(dirname "$O"); CL=$SP/spike-inject/clients
ENV=(-e HTTPS_PROXY=http://127.0.0.1:3128 -e https_proxy=http://127.0.0.1:3128 -e HTTP_PROXY=http://127.0.0.1:3128 -e http_proxy=http://127.0.0.1:3128 -e NO_PROXY= -e ACME_API_KEY=lenny-injected)
B=(docker run --rm --network container:oc-na -v $O/run/trust:/s:ro -v $CL:/c:ro -v $O/bin:/b:ro "${ENV[@]}")
sr(){ echo; echo "=== $1"; shift; "$@" 2>&1 | tail -${TL:-4}; echo "rc=${PIPESTATUS[0]}"; }
sr "curl h1" "${B[@]}" -e CURL_CA_BUNDLE=/s/trust.pem curlimages/curl:latest -sS --http1.1 -w ' %{http_version} %{http_code}\n' https://api.example.test/acme/check
sr "curl h2" "${B[@]}" -e CURL_CA_BUNDLE=/s/trust.pem curlimages/curl:latest -sS --http2 -w ' %{http_version} %{http_code}\n' https://api.example.test/acme/check
sr "curl plain upgrade" "${B[@]}" curlimages/curl:latest -sS -w ' %{http_version} %{http_code}\n' http://api.example.test/acme/check
TL=8 sr "Python requests, httpx h1, httpx h2, aiohttp" "${B[@]}" -e REQUESTS_CA_BUNDLE=/s/trust.pem -e SSL_CERT_FILE=/s/trust.pem oc-py python /c/py.py
sr "pip" "${B[@]}" -e PIP_CERT=/s/trust.pem oc-py sh -c 'pip install -q --disable-pip-version-check --root-user-action=ignore --index-url https://pypiuser:lenny-injected@pypi.example.test/simple/ spikepkg && python -c "import spikepkg;print(\"pip-installed\", spikepkg.VALUE)"'
sr "Node 24 fetch" "${B[@]}" -e NODE_USE_ENV_PROXY=1 -e NODE_EXTRA_CA_CERTS=/s/trust.pem node:24-slim node /c/node.mjs
sr "npm" "${B[@]}" -e NODE_EXTRA_CA_CERTS=/s/trust.pem node:24-slim sh -c 'mkdir /tmp/p && cd /tmp/p && printf "registry=https://registry.example.test/\n//registry.example.test/:_authToken=lenny-injected\n" > .npmrc && npm install spike-leftpad --no-audit --no-fund --loglevel=error && node -e "console.log(\"npm-installed\", require(\"spike-leftpad\")(\"x\",3))"'
sr "Go net/http" "${B[@]}" -e SSL_CERT_FILE=/s/trust.pem alpine:3.22 /b/goc
docker run --rm -v $O/run/trust:/s eclipse-temurin:21-jdk sh -c 'rm -f /s/trust.p12; keytool -importcert -noprompt -alias pod -file /s/pod-ca.pem -keystore /s/trust.p12 -storepass changeit -storetype PKCS12 >/dev/null 2>&1; keytool -importcert -noprompt -alias pub -file /s/pub-ca.pem -keystore /s/trust.p12 -storepass changeit -storetype PKCS12 >/dev/null 2>&1; chmod 644 /s/trust.p12'
sr "Java 21 HttpClient" "${B[@]}" -e JAVA_TOOL_OPTIONS="-Dhttps.proxyHost=127.0.0.1 -Dhttps.proxyPort=3128 -Djavax.net.ssl.trustStore=/s/trust.p12 -Djavax.net.ssl.trustStorePassword=changeit -Djavax.net.ssl.trustStoreType=PKCS12" eclipse-temurin:21-jdk java /c/J.java
sr "gh" "${B[@]}" -v $SP/spike-tools/dl/gh/gh_2.103.3_linux_arm64/bin/gh:/usr/local/bin/gh:ro -e GH_HOST=ghes.example.test -e GH_ENTERPRISE_TOKEN=lenny-injected -e SSL_CERT_FILE=/s/trust.pem -e GH_CONFIG_DIR=/tmp/gh alpine:3.22 gh api user
sr "glab, https, placeholder in GITLAB_TOKEN" "${B[@]}" -v $SP/spike-tools/dl/glab/bin/glab:/usr/local/bin/glab:ro -e GITLAB_HOST=gitlab.example.test -e GITLAB_TOKEN=lenny-injected -e SSL_CERT_FILE=/s/trust.pem -e GLAB_NO_PROMPT=1 -e GLAB_CONFIG_DIR=/tmp/glab -e GLAB_CHECK_UPDATE=false -e GLAB_SEND_TELEMETRY=false alpine:3.22 glab api --hostname gitlab.example.test user
sr "glab, api_protocol http through HTTP_PROXY" "${B[@]}" -v $SP/spike-tools/dl/glab/bin/glab:/usr/local/bin/glab:ro -e GITLAB_TOKEN=lenny-injected -e GLAB_NO_PROMPT=1 -e GLAB_CONFIG_DIR=/tmp/glab -e GITLAB_API_PROTOCOL=http -e GLAB_CHECK_UPDATE=false -e GLAB_SEND_TELEMETRY=false alpine:3.22 sh -c 'mkdir -p /tmp/glab; printf "hosts:\n    gitlab.example.test:\n        token: lenny-injected\n        api_host: gitlab.example.test\n        api_protocol: http\n        git_protocol: https\ncheck_update: false\ntelemetry: false\n" > /tmp/glab/config.yml; chmod 600 /tmp/glab/config.yml; glab api --hostname gitlab.example.test user'
for img in oc-gitimg oc-py; do
 lib="OpenSSL (Alpine)"; [ $img = oc-py ] && lib="GnuTLS (Debian)"
 G=("${B[@]}" -e GIT_SSL_CAINFO=/s/trust.pem -e GIT_TERMINAL_PROMPT=0 $img)
 sr "git $lib: clone through an opaque tunnel, unbound host" "${G[@]}" sh -c 'git clone -q https://gitopen.example.test/r.git /tmp/x && git -C /tmp/x log --oneline | wc -l'
 TL=3 sr "git $lib: clone through termination, bound non-VCS host, then a 64 MiB chunked push" "${G[@]}" sh -c 'git clone -q https://gitplain.example.test/r.git /tmp/x && cd /tmp/x && git log --oneline | wc -l && git -c http.version=HTTP/1.1 config user.email a@b; git config user.name a; git checkout -q -b b'"$RANDOM"' && head -c 67108864 /dev/urandom > blob && git add blob && git commit -qm blob && git -c http.version=HTTP/1.1 push -q origin HEAD 2>&1 | tail -2; echo push-rc=$?; git ls-remote origin | wc -l'
 TL=3 sr "git $lib: clone against a VCS-covered host through HTTPS_PROXY (design: refused)" "${G[@]}" sh -c 'git clone -q https://vcs.example.test/r.git /tmp/x'
 TL=3 sr "git $lib: push over the plain HTTP upgrade to a VCS-covered host (design: refused)" "${G[@]}" sh -c 'git -c http.proxy=http://127.0.0.1:3128 ls-remote http://vcs.example.test/r.git'
done
sr "rustls: reqwest with the root added" "${B[@]}" -v $O/run/trust/trust.pem:/c2/bundle.pem:ro -v $O/rust-target/release/ncrust:/usr/local/bin/ncrust:ro rust:1-slim-bookworm ncrust rustls https://api.example.test/acme/check https://other.example.test/small
sr "rustls: reqwest with rustls-native-certs, SSL_CERT_FILE" "${B[@]}" -v $O/run/trust/trust.pem:/c2/bundle.pem:ro -e SSL_CERT_FILE=/s/trust.pem -v $O/rust-target/release/ncrust:/usr/local/bin/ncrust:ro rust:1-slim-bookworm ncrust rustls-native-roots https://api.example.test/acme/check https://other.example.test/small
sr "Deno (rustls) fetch" "${B[@]}" -e DENO_CERT=/s/trust.pem -e DENO_NO_UPDATE_CHECK=1 denoland/deno:2.5.0 eval 'const r=await fetch("https://api.example.test/acme/check");console.log("deno",r.status,(await r.text()).trim())'
sr "Bun (BoringSSL) fetch" "${B[@]}" -e NODE_EXTRA_CA_CERTS=/s/trust.pem oven/bun:1.2 bun -e 'const r=await fetch("https://api.example.test/acme/check");console.log("bun",r.status,(await r.text()).trim())'
echo; echo "=== netagent counters"; docker exec oc-na wget -qO- http://127.0.0.1:9100/stats
