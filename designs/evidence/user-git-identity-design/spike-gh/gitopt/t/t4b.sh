#!/bin/sh
# t4b.sh <mode> <config>: https:// to a VCS-covered host with no API binding, with the rewrite removed, under one CONNECT rule.
. /t/env.sh; . /t/lib.sh
M=$1; export GIT_SSL_CAINFO=/s/bundle.pem GIT_CONFIG_GLOBAL=/dev/null; TL=2; W=$(mktemp -d); cd $W
step $M-public "git ls-remote https://github.com/pub/lib.git 2>&1 | tail -1 | cut -c1-140"
step $M-private "git ls-remote https://github.com/acme/widgets.git 2>&1 | tail -1 | cut -c1-140"
step $M-outside "git ls-remote https://github.com/acme/secret.git 2>&1 | tail -1 | cut -c1-140"
step $M-sslverify-false "git -c http.sslVerify=false ls-remote https://github.com/pub/lib.git 2>&1 | tail -1 | cut -c1-140"
git init -q p; cd p; git -c user.email=a@b -c user.name=a commit -q --allow-empty -m x
step $M-own-token-push-main "git -c 'http.extraHeader=Authorization: Bearer AGENT-OWN-TOKEN' push -q https://github.com/agent/own.git HEAD:refs/heads/main 2>&1 | tail -1 | cut -c1-140"
cd ..
step $M-release-download "curl -sS -m 8 -o /tmp/b -w http=%{http_code} --cacert /s/bundle.pem -x 127.0.0.1:3128 https://github.com/acme/widgets/releases/download/v1/x 2>&1"
step $M-web-page "curl -sS -m 8 -o /tmp/b -w http=%{http_code} --cacert /s/bundle.pem -x 127.0.0.1:3128 https://github.com/acme/widgets 2>&1"
step $M-gh-pr-list "git init -q z; cd z; git remote add origin https://github.com/acme/widgets.git; GH_TOKEN=lenny-injected gh pr list 2>&1 | tail -1 | cut -c1-120"
cd /; rm -rf $W
