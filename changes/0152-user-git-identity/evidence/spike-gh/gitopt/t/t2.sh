#!/bin/sh
# t2.sh <config>: gh and glab in the clone git made, end to end, on GHES and github.com flavours and GitLab.
. /t/env.sh; . /t/lib.sh
CFG=$1; export GIT_CONFIG_GLOBAL=/t/cfg/$CFG.gitconfig; [ "$CAINFO" = design ] && export GIT_SSL_CAINFO=/s/bundle.pem
W=$(mktemp -d); TAG=$(date +%s); TL=7
flavour(){ # prefix host apihost-flavour
 P=$1; H=$2
 step $P-clone "cd $W && git clone -q https://$H/acme/widgets.git $P && cd $P && git checkout -q -b feature-$CFG-$TAG-$P && echo x > n && git add n && git commit -qm n && git push -q -u origin HEAD 2>&1 && git remote -v && git config --get remote.origin.url"
 cd $W/$P
 step $P-repo-view "gh repo view --json url,nameWithOwner,sshUrl 2>&1"
 step $P-pr-create "gh pr create --title t --body b 2>&1"
 step $P-pr-view "gh pr view --json number,headRefName,state,url,headRepositoryOwner,isCrossRepository 2>&1"
 step $P-pr-view-n "gh pr view 7 --json number,state 2>&1"
 step $P-pr-list "gh pr list 2>&1"
 step $P-pr-status "gh pr status 2>&1"
 step $P-pr-checks "gh pr checks 7 2>&1"
 step $P-browse "gh browse -n 2>&1"
 step $P-reference-noconfig "GIT_CONFIG_GLOBAL=/dev/null gh pr view --json number,headRefName 2>&1"
 step $P-set-default "gh repo set-default acme/widgets 2>&1; git config --get remote.origin.gh-resolved"
 step $P-set-default-view "gh repo set-default --view 2>&1"
 step $P-pr-checkout "gh pr checkout 7 2>&1; git rev-parse --abbrev-ref HEAD; git config --get-regexp '^branch\.feature\.'"
 git checkout -q feature-$CFG-$TAG-$P 2>/dev/null
 step $P-pr-checkout-cross "gh pr checkout 13 2>&1; git rev-parse --abbrev-ref HEAD; git config --get-regexp '^branch\.cross-branch\.'; git remote -v | head -4"
 git checkout -q feature-$CFG-$TAG-$P 2>/dev/null; git branch -qD cross-branch 2>/dev/null
 step $P-pr-checkout-cross-maintainer "gh pr checkout 14 2>&1; git rev-parse --abbrev-ref HEAD; git config --get-regexp '^branch\.cross-branch\.'; git remote -v | tail -2"
 step $P-pull-after-cross "git pull 2>&1 | tail -1"
 git checkout -q feature-$CFG-$TAG-$P 2>/dev/null
 step $P-repo-clone "cd $W && gh repo clone acme/widgets $P-rc 2>&1; git -C $P-rc remote -v; git -C $P-rc config --get remote.origin.url"
 step $P-repo-clone-fork "cd $W && gh repo clone alice/widgets $P-rf 2>&1; git -C $P-rf remote -v; git -C $P-rf config --get-regexp '^remote\..*\.url'"
 git remote add fork https://$H/alice/widgets.git; git checkout -q -b featurefork-$CFG-$TAG-$P; echo y > m; git add m; git commit -qm m
 step $P-fork-push "git push -q -u fork HEAD 2>&1 && git remote -v && git rev-parse --abbrev-ref @{u}"
 step $P-fork-pr-create "gh pr create --title t --body b 2>&1"
 step $P-fork-pr-view "gh pr view --json number,headRefName,isCrossRepository,headRepositoryOwner 2>&1"
 git checkout -q -b feature-unp-$CFG-$TAG-$P; git remote remove fork; git config --unset remote.origin.gh-resolved
 step $P-unpushed-noninteractive "gh pr create --title t --body b 2>&1"
 step $P-interactive-push "python3 -I /t/ptyrun.py 'gh pr create --title t --body b' 2>&1; printf '\n--config--\n'; git config --get-regexp '^branch\.feature-unp'; git remote -v"
 cd $W
}
export GH_HOST=ghes.example.test GH_ENTERPRISE_TOKEN=lenny-injected
flavour ghes ghes.example.test
unset GH_HOST GH_ENTERPRISE_TOKEN
export GH_TOKEN=lenny-injected
flavour com github.com
unset GH_TOKEN
export GITLAB_HOST=gitlab.example.test GITLAB_TOKEN=lenny-injected
step gl-clone "cd $W && git clone -q https://gitlab.example.test/acme/widgets.git l && cd l && git checkout -q -b feature-$CFG-$TAG-l && echo x > n && git add n && git commit -qm n && git push -q -u origin HEAD 2>&1 && git remote -v"
cd $W/l
step gl-mr-create "glab mr create --title t --description d --yes 2>&1"
step gl-mr-view "glab mr view 7 2>&1 | head -4"
step gl-mr-list "glab mr list 2>&1"
step gl-repo-view "glab repo view 2>&1 | head -3"
cd /; rm -rf $W
