#!/bin/bash
# a1.sh: the client matrix against the Go netagent, with a name-constrained per-pod CA.
O=$(cd "$(dirname "$0")" && pwd); cd "$O"; SP=$(dirname "$O"); CL=$SP/spike-inject/clients
ENV=(-e HTTPS_PROXY=http://127.0.0.1:3128 -e https_proxy=http://127.0.0.1:3128 -e HTTP_PROXY=http://127.0.0.1:3128 -e http_proxy=http://127.0.0.1:3128 -e NO_PROXY= -e ACME_API_KEY=lenny-injected)
B=(docker run --rm --network container:oc-na -v $O/run/trust:/s:ro -v $CL:/c:ro -v $O/bin:/b:ro "${ENV[@]}")
sr(){ echo; echo "=== $1"; shift; "$@" 2>&1 | tail -${TL:-4}; echo "rc=${PIPESTATUS[0]}"; }
for img in oc-gitimg oc-py; do
 lib="OpenSSL (Alpine)"; [ $img = oc-py ] && lib="GnuTLS (Debian)"
 G=("${B[@]}" -e GIT_SSL_CAINFO=/s/trust.pem -e GIT_TERMINAL_PROMPT=0 $img)
 sr "git $lib: clone through an opaque tunnel, unbound host" "${G[@]}" sh -c 'git clone -q https://gitopen.example.test/r.git /tmp/x && git -C /tmp/x log --oneline | wc -l'
 TL=3 sr "git $lib: clone through termination, bound non-VCS host, then a 64 MiB chunked push" "${G[@]}" sh -c 'git clone -q https://gitplain.example.test/r.git /tmp/x && cd /tmp/x && git log --oneline | wc -l && git -c http.version=HTTP/1.1 config user.email a@b; git config user.name a; git checkout -q -b b'"$RANDOM"' && head -c 67108864 /dev/urandom > blob && git add blob && git commit -qm blob && git -c http.version=HTTP/1.1 push -q origin HEAD 2>&1 | tail -2; echo push-rc=$?; git ls-remote origin | wc -l'
 TL=3 sr "git $lib: clone against a VCS-covered host through HTTPS_PROXY (design: refused)" "${G[@]}" sh -c 'git clone -q https://vcs.example.test/r.git /tmp/x'
 TL=3 sr "git $lib: push over the plain HTTP upgrade to a VCS-covered host (design: refused)" "${G[@]}" sh -c 'git -c http.proxy=http://127.0.0.1:3128 ls-remote http://vcs.example.test/r.git'
done
