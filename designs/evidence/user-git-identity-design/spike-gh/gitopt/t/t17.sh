#!/bin/sh
# t17.sh: gh with GH_HOST at a ghe.com tenancy host, through the preset.
. /t/env.sh; . /t/lib.sh
export GIT_CONFIG_GLOBAL=/t/cfg/Tb.gitconfig GH_HOST=acme.ghe.com GH_ENTERPRISE_TOKEN=lenny-injected; TL=2; STEPT=30; W=$(mktemp -d); cd $W; TAG=$(date +%s)
step ghe-clone "git clone -q https://acme.ghe.com/acme/widgets.git c 2>&1; cd c && git checkout -q -b feature-ghe-$TAG && git commit -q --allow-empty -m e && git push -q -u origin HEAD 2>&1; git remote -v | head -1"
cd c
step ghe-auth-status "gh auth status 2>&1 | head -3"
step ghe-pr-create "gh pr create --title t --body b --reviewer bob --assignee bob 2>&1"
step ghe-pr-view "gh pr view 7 --json number,url 2>&1"
step ghe-pr-list "gh pr list 2>&1"
step ghe-pr-comment "gh pr comment 7 --body c 2>&1"
step ghe-D-pr-review "gh pr review 7 --approve 2>&1 | cut -c1-140"
step ghe-D-pr-merge "gh pr merge 7 --merge 2>&1 | cut -c1-140"
step ghe-D-api-merge "gh api -X PUT repos/acme/widgets/pulls/7/merge 2>&1 | cut -c1-140"
cd /; rm -rf $W
