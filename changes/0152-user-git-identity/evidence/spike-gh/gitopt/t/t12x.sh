#!/bin/sh
# t12x.sh <flavour>: gh commands the matrix does not hold and the final preset's new families touch.
. /t/env.sh; . /t/lib.sh
FL=$1; export GIT_CONFIG_GLOBAL=/t/cfg/Tb.gitconfig; TL=2; STEPT=30; W=$(mktemp -d); cd $W; TAG=$(date +%s)
if [ $FL = ghes ]; then H=ghes.example.test; export GH_HOST=$H GH_ENTERPRISE_TOKEN=lenny-injected; else H=github.com; export GH_TOKEN=lenny-injected; fi
git clone -q https://$H/acme/widgets.git c; cd c; git checkout -q -b feature-x-$FL-$TAG; echo x > n; git add n; git commit -qm n; git push -q -u origin HEAD
s(){ step "$FL-$1" "$2"; }
python3 -I -c "open('/tmp/lt','w').write('<'*65536); open('/tmp/emoji','w').write('\U0001F600'*65536); open('/tmp/big','w').write('x'*500000)"
s auth-status "gh auth status"
s repo-view "gh repo view --json name"
s ssh-key-add "printf 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIF x' > /tmp/k2.pub; gh ssh-key add /tmp/k2.pub --title k"
s ssh-key-delete "gh ssh-key delete 1 --yes"
s gpg-key-add "printf x > /tmp/g.asc; gh gpg-key add /tmp/g.asc"
s gpg-key-delete "gh gpg-key delete 1 --yes"
s secret-set-env "gh secret set S --env prod --body v"
s secret-delete-env "gh secret delete S --env prod"
s variable-set-env "gh variable set V --env prod --body v"
s variable-delete-env "gh variable delete V --env prod"
s secret-set-org "gh secret set S --org acme --body v"
s repo-edit-automerge "gh repo edit --enable-auto-merge"
s pr-create-65536-lt "git checkout -q -b feature-x2-$FL-$TAG; git commit -q --allow-empty -m e; git push -q -u origin HEAD; gh pr create --title t --body-file /tmp/lt"
s pr-comment-65536-lt "gh pr comment 7 --body-file /tmp/lt"
s pr-comment-65536-emoji "gh pr comment 7 --body-file /tmp/emoji"
s pr-comment-500k "gh pr comment 7 --body-file /tmp/big"
s pr-edit-20-reviewers "gh pr edit 7 --add-reviewer a1,a2,a3,a4,a5,a6,a7,a8,a9,a10,a11,a12,a13,a14,a15,a16,a17,a18,a19,a20"
s api-gql-17-root "gh api graphql -f query='mutation{a0:addComment(input:{}){clientMutationId} a1:addComment(input:{}){clientMutationId} a2:addComment(input:{}){clientMutationId} a3:addComment(input:{}){clientMutationId} a4:addComment(input:{}){clientMutationId} a5:addComment(input:{}){clientMutationId} a6:addComment(input:{}){clientMutationId} a7:addComment(input:{}){clientMutationId} a8:addComment(input:{}){clientMutationId} a9:addComment(input:{}){clientMutationId} a10:addComment(input:{}){clientMutationId} a11:addComment(input:{}){clientMutationId} a12:addComment(input:{}){clientMutationId} a13:addComment(input:{}){clientMutationId} a14:addComment(input:{}){clientMutationId} a15:addComment(input:{}){clientMutationId} a16:addComment(input:{}){clientMutationId}}'"
s api-post-query "gh api -X POST 'repos/acme/widgets/issues?x=1' -f title=t"
s api-form-input "printf 'a=b' > /tmp/f; gh api -X POST repos/acme/widgets/issues --input /tmp/f -H 'Content-Type: application/x-www-form-urlencoded'"
s api-repo-graphql-tools "gh api repos/acme/graphql-tools"
s api-repo-graphql-tools-issue "gh api -X POST repos/acme/graphql-tools/issues -f title=t"
s api-repositories-alias-merge "gh api -X PUT repositories/123/pulls/7/merge"
cd /; rm -rf $W
