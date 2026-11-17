#!/bin/sh
# t2c.sh <config>: gh pr checkout of a fork PR that allows maintainer edits, where gh stores the head repository's URL as the branch remote.
. /t/env.sh; . /t/lib.sh
CFG=$1; export GIT_CONFIG_GLOBAL=/t/cfg/$CFG.gitconfig; [ "$CAINFO" = design ] && export GIT_SSL_CAINFO=/s/bundle.pem
W=$(mktemp -d); TL=8; export GH_HOST=ghes.example.test GH_ENTERPRISE_TOKEN=lenny-injected
cd $W; git clone -q https://ghes.example.test/acme/widgets.git g; cd g
step cross-maintainer "gh pr checkout 14 2>&1; git rev-parse --abbrev-ref HEAD; git config --get-regexp '^branch\.cross-branch\.'"
step cross-maintainer-remote-v "git remote -v"
step cross-maintainer-pull "git pull 2>&1 | tail -2"
step cross-maintainer-push "git commit -q --allow-empty -m m; git push origin HEAD:refs/heads/feature-m-\$\$ 2>&1 | tail -1; git push 2>&1 | tail -2"
cd /; rm -rf $W
