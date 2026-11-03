#!/bin/sh
# t37.sh <label>: a formula host on a restricted pool: fetch, push, and raw requests, with the client's own token.
. /t/env.sh; . /t/lib.sh
L=$1; H=assets.example.test; export GIT_CONFIG_GLOBAL=$HOME/t.gitconfig; cp /t/cfg/Tb.gitconfig $GIT_CONFIG_GLOBAL; TL=2; STEPT=20; W=$(mktemp -d); cd $W; T=USER-FILE-CURRENT
step $L-fetch-public "(git -c 'http.extraHeader=Authorization: Bearer $T' ls-remote https://$H/pub/lib.git 2>&1; echo git-exit=\$?) | tail -2 | cut -c1-110"
step $L-fetch-private "(git -c 'http.extraHeader=Authorization: Bearer $T' ls-remote https://$H/acme/widgets.git 2>&1; echo git-exit=\$?) | tail -2 | cut -c1-110"
git init -q l; cd l; git commit -q --allow-empty -m e
step $L-push-header "(git -c 'http.extraHeader=Authorization: Bearer $T' push -q https://$H/pub/lib.git HEAD:refs/heads/x 2>&1; echo git-exit=\$?) | tail -2 | cut -c1-110"
step $L-push-url "(git push -q https://x:$T@$H/pub/lib.git HEAD:refs/heads/x 2>&1; echo git-exit=\$?) | tail -2 | cut -c1-110"
step $L-push-main "(git -c 'http.extraHeader=Authorization: Bearer $T' push -q https://$H/acme/widgets.git HEAD:refs/heads/main 2>&1; echo git-exit=\$?) | tail -2 | cut -c1-110"
step $L-curl-receive-advert "curl -sS -m 10 -o /dev/null -w http=%{http_code} --cacert /s/bundle.pem -x 127.0.0.1:3128 -H 'Authorization: Bearer $T' 'https://$H/acme/widgets.git/info/refs?service=git-receive-pack'"
step $L-other-request "curl -sS -m 10 -o /dev/null -w http=%{http_code} --cacert /s/bundle.pem -x 127.0.0.1:3128 https://$H/plain/path"
cd /; rm -rf $W
