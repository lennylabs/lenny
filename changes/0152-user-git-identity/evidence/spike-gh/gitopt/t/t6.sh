#!/bin/sh
# t6.sh <config> <tag>: trust and warm-state probes on one client.
. /t/env.sh; . /t/lib.sh
CFG=$1; TAG=$2; export GIT_CONFIG_GLOBAL=/t/cfg/$CFG.gitconfig; [ "$CAINFO" = design ] && export GIT_SSL_CAINFO=/s/bundle.pem
W=$(mktemp -d); cd $W; TL=2
step "$TAG-bound-host" "git ls-remote https://ghes.example.test/acme/widgets.git 2>&1 | head -1"
step "$TAG-github-host" "git ls-remote https://github.com/acme/widgets.git 2>&1 | head -1"
step "$TAG-arbitrary-public" "git ls-remote https://other.example.test/pub/lib.git 2>&1 | head -1"
step "$TAG-arbitrary-private" "git ls-remote https://other.example.test/acme/secret.git 2>&1 | head -1"
step "$TAG-arbitrary-push" "git init -q p; cd p; git commit -q --allow-empty -m x; git push -q https://other.example.test/pub/lib.git HEAD:refs/heads/x 2>&1"
step "$TAG-gh" "cd /tmp; rm -rf g6$TAG; git init -q g6$TAG; cd g6$TAG; git remote add origin https://ghes.example.test/acme/widgets.git; env GH_HOST=ghes.example.test GH_ENTERPRISE_TOKEN=lenny-injected gh pr list 2>&1"
cd /; rm -rf $W /tmp/g6$TAG
