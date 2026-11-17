#!/bin/sh
# t6d.sh <config> <tag> <host>: a warm fill or a routed fetch, then what the origin saw.
. /t/env.sh; . /t/lib.sh
CFG=$1; TAG=$2; H=$3; export GIT_CONFIG_GLOBAL=/t/cfg/$CFG.gitconfig; [ "$CAINFO" = design ] && export GIT_SSL_CAINFO=/s/bundle.pem
W=$(mktemp -d); cd $W; TL=2
step "$TAG-clone" "git clone -q https://$H/acme/widgets.git c 2>&1; git -C c log --oneline 2>/dev/null | wc -l"
if [ -n "$PUSHTOO" ]; then cd c; git checkout -q -b lenny/m-\$\$ 2>/dev/null; git commit -q --allow-empty -m m; step "$TAG-push" "git push -q origin HEAD 2>&1"; cd ..; fi
cd /; rm -rf $W
