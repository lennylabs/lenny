#!/bin/sh
# t38.sh: git and API requests on one host, under a session with a route digest. Each request is its own client connection, so only netagent's upstream pools decide reuse.
. /t/env.sh
H=ghes.example.test; export GIT_CONFIG_GLOBAL=$HOME/t.gitconfig; cp /t/cfg/Tb.gitconfig $GIT_CONFIG_GLOBAL
api(){ curl -sS -m 10 -o /dev/null -w "api http=%{http_code}\n" --cacert /s/bundle.pem -x 127.0.0.1:3128 -H 'Authorization: token USER-FILE-CURRENT' "$@" https://$H/api/v3/repos/acme/widgets; }
git(){ curl -sS -m 10 -o /dev/null -w "git http=%{http_code}\n" --cacert /s/bundle.pem -x 127.0.0.1:3128 "$@" "https://$H/acme/widgets.git/info/refs?service=git-upload-pack"; }
case $1 in
 mix) for i in 1 2 3; do git; api; done;;
 git) git;;
 api) api;;
 forged) git --proxy-header 'X-Lenny-Route-Assert: git forged' ; api --proxy-header 'X-Lenny-Route-Assert: egress forged';;
esac
