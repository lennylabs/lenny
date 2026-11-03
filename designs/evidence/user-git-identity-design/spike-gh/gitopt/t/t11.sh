#!/bin/sh
# t11.sh <config>: a token the runtime holds itself, delivered as a file, on git push and on gh.
. /t/env.sh; . /t/lib.sh
CFG=$1; export GIT_CONFIG_GLOBAL=$HOME/t11.gitconfig; cp /t/cfg/$CFG.gitconfig $GIT_CONFIG_GLOBAL; [ "$CAINFO" = design ] && export GIT_SSL_CAINFO=/s/bundle.pem
W=$(mktemp -d); cd $W; TL=2; TOK=USER-FILE-TOKEN; H=ghes.example.test; TAG=$(date +%s)
printf 'https://x:%s@%s\nhttps://x:%s@github.com\nhttp://x:%s@%s\nhttp://x:%s@github.com\n' $TOK $H $TOK $TOK $H $TOK > $HOME/.git-credentials
step setup "git clone -q https://$H/acme/widgets.git c 2>&1; cd c; git checkout -q -b lenny/f-$TAG; git commit -q --allow-empty -m e; git log --oneline | wc -l"
cd c
step cred-store-push "git -c credential.helper=store push -q origin HEAD 2>&1 | tail -1"
step extraheader-push "git commit -q --allow-empty -m e; git -c 'http.extraHeader=Authorization: Bearer $TOK' push -q origin HEAD 2>&1 | tail -1"
step url-token-push "git commit -q --allow-empty -m e; git push -q https://x:$TOK@$H/acme/widgets.git HEAD 2>&1 | tail -1"
step gh-credential-helper-push "git commit -q --allow-empty -m e; GH_ENTERPRISE_TOKEN=$TOK GH_HOST=$H git -c 'credential.helper=!gh auth git-credential' push -q origin HEAD 2>&1 | tail -1"
step push-main-with-own-token "git -c 'http.extraHeader=Authorization: Bearer $TOK' -c credential.helper=store push -q origin HEAD:refs/heads/main 2>&1 | tail -1"
step push-outside-with-own-token "git -c 'http.extraHeader=Authorization: Bearer $TOK' -c credential.helper=store push -q https://$H/acme/secret.git HEAD:refs/heads/lenny/x 2>&1 | tail -1"
step no-rewrite-push-main "GIT_CONFIG_GLOBAL=/dev/null GIT_SSL_CAINFO=/s/bundle.pem git -c user.email=a@b -c 'http.extraHeader=Authorization: Bearer $TOK' push -q https://$H/acme/widgets.git HEAD:refs/heads/main 2>&1 | tail -1"
step no-rewrite-push-main-github "GIT_CONFIG_GLOBAL=/dev/null GIT_SSL_CAINFO=/s/bundle.pem git -c 'http.extraHeader=Authorization: Bearer $TOK' push -q https://github.com/acme/widgets.git HEAD:refs/heads/main 2>&1 | tail -1"
step gh-list-ghes "GH_HOST=$H GH_ENTERPRISE_TOKEN=$TOK gh pr list 2>&1 | tail -1"
step gh-create-ghes "git push -q origin HEAD 2>&1; GH_HOST=$H GH_ENTERPRISE_TOKEN=$TOK gh pr create --title t --body b 2>&1 | tail -1"
step gh-merge-ghes "GH_HOST=$H GH_ENTERPRISE_TOKEN=$TOK gh pr merge 7 --merge 2>&1 | tail -1 | cut -c1-150"
step gh-api-merge-ghes "GH_HOST=$H GH_ENTERPRISE_TOKEN=$TOK gh api -X PUT repos/acme/widgets/pulls/7/merge 2>&1 | tail -1 | cut -c1-150"
cd $W; git clone -q https://github.com/acme/widgets.git d 2>/dev/null; cd d
step gh-list-com "GH_TOKEN=$TOK gh pr list 2>&1 | tail -1"
step gh-merge-com "GH_TOKEN=$TOK gh pr merge 7 --merge 2>&1 | tail -1 | cut -c1-150"
cd /; rm -rf $W
