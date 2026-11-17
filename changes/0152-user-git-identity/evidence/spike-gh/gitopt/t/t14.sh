#!/bin/sh
# t14.sh <version> <policy-label>: one gh release against one table, on both flavours.
. /t/env.sh; . /t/lib.sh
V=$1; PL=$2; export GIT_CONFIG_GLOBAL=/t/cfg/Tb.gitconfig; TL=1; STEPT=30; W=$(mktemp -d); cd $W; TAG=$(date +%s)
gh --version | head -1
for FL in ghes com; do
 if [ $FL = ghes ]; then H=ghes.example.test; export GH_HOST=$H GH_ENTERPRISE_TOKEN=lenny-injected; unset GH_TOKEN; else H=github.com; unset GH_HOST GH_ENTERPRISE_TOKEN; export GH_TOKEN=lenny-injected; fi
 git clone -q https://$H/acme/widgets.git $FL; cd $FL; git checkout -q -b feature-$V-$PL-$FL-$TAG; echo x > n; git add n; git commit -qm n; git push -q -u origin HEAD
 step "$FL-$V-$PL-pr-create-rev-assign" "gh pr create --title t --body b --reviewer bob --assignee bob"
 step "$FL-$V-$PL-pr-edit-title" "gh pr edit 7 --title new"
 step "$FL-$V-$PL-pr-edit-meta" "gh pr edit 7 --add-label bug --add-assignee bob --add-reviewer bob"
 step "$FL-$V-$PL-pr-comment" "gh pr comment 7 --body hello"
 step "$FL-$V-$PL-pr-view" "gh pr view 7 --json number"
 step "$FL-$V-$PL-pr-merge" "gh pr merge 7 --merge"
 cd ..
done
cd /; rm -rf $W
