#!/bin/sh
# t27.sh <state>: every git client form, under one session state. netagent decides the credential whatever the client sends.
. /t/env.sh; . /t/lib.sh
SL=$1; export GIT_CONFIG_GLOBAL=$HOME/t27.gitconfig; cp /t/cfg/Tb.gitconfig $GIT_CONFIG_GLOBAL
W=$(mktemp -d); cd $W; TL=2; STEPT=30; TOK=USER-FILE-OLD-HELD; OT=OTHER-TOKEN-x9; H=ghes.example.test; TAG=$SL-$(date +%s)
printf 'https://x:%s@%s\nhttps://x:%s@github.com\n' $TOK $H $TOK > $HOME/.git-credentials
PH="-c credential.helper=store"
XH="-c 'http.extraHeader=Authorization: Bearer $TOK'"; XO="-c 'http.extraHeader=Authorization: Bearer $OT'"
for f in none helper url header other; do
 case $f in none) A=""; U=https://$H/acme/widgets.git;; helper) A="$PH"; U=https://$H/acme/widgets.git;; url) A=""; U=https://x:$TOK@$H/acme/widgets.git;; header) A="$XH"; U=https://$H/acme/widgets.git;; other) A="$XO"; U=https://$H/acme/widgets.git;; esac
 step $SL-clone-$f "(git $A clone -q $U c-$f 2>&1; echo git-exit=\$?) | tail -2 | cut -c1-130"
done
if [ -f c-none/s ] || [ -d c-none/.git ] && git -C c-none rev-parse -q HEAD >/dev/null 2>&1; then cd c-none; else rm -rf local; git init -q local; cd local; git remote add origin https://$H/acme/widgets.git; git checkout -q -b lenny/seed; git commit -q --allow-empty -m seed; fi
git checkout -q -b lenny/f-$TAG
for f in none helper url header other; do
 case $f in none) A=""; U=origin;; helper) A="$PH"; U=origin;; url) A=""; U=https://x:$TOK@$H/acme/widgets.git;; header) A="$XH"; U=origin;; other) A="$XO"; U=origin;; esac
 git commit -q --allow-empty -m e
 step $SL-push-$f "(git $A push -q $U HEAD:refs/heads/lenny/f-$TAG 2>&1; echo git-exit=\$?) | tail -2 | cut -c1-130"
done
git commit -q --allow-empty -m e
step $SL-push-gh-helper "GH_ENTERPRISE_TOKEN=$OT GH_HOST=$H git -c credential.helper= -c 'credential.helper=!/g/gh auth git-credential' push -q origin HEAD:refs/heads/lenny/f-$TAG 2>&1 | tail -1"
step $SL-push-main "git push -q origin HEAD:refs/heads/main 2>&1 | tail -1"
step $SL-push-main-other "git $XO push -q origin HEAD:refs/heads/main 2>&1 | tail -1"
step $SL-push-mixed "git push -q origin HEAD:refs/heads/lenny/m-$TAG HEAD:refs/heads/main 2>&1 | tail -1"
step $SL-push-delete "git push -q origin :refs/heads/feature 2>&1 | tail -1"
step $SL-push-option "git push -q -o ci.skip origin HEAD:refs/heads/lenny/o-$TAG 2>&1 | tail -1"
step $SL-push-outside "git push -q https://$H/acme/secret.git HEAD:refs/heads/lenny/x 2>&1 | tail -1"
step $SL-push-readonly "git push -q https://$H/acme/lib.git HEAD:refs/heads/lenny/x 2>&1 | tail -1"
step $SL-read-outside "git ls-remote https://$H/acme/secret.git 2>&1 | tail -1"
step $SL-reflect "curl -sS -m 10 -D /tmp/hd -o /tmp/b -w http=%{http_code} --cacert /s/bundle.pem -x 127.0.0.1:3128 -H 'Authorization: Bearer $OT' 'https://$H/acme/reflect.git/info/refs?service=git-upload-pack'; cut -c1-60 /tmp/b; grep -i x-reflect /tmp/hd | cut -c1-60"
step $SL-redirect-in-scope "git ls-remote https://$H/acme/moved.git 2>&1 | tail -1 | cut -c1-60"
step $SL-redirect-out-of-scope "git ls-remote https://$H/acme/evilredir.git 2>&1 | tail -1 | cut -c1-100"
step $SL-github-push "git push -q https://github.com/acme/widgets.git HEAD:refs/heads/lenny/gh-$TAG 2>&1 | tail -1"
step $SL-github-push-main "git push -q https://github.com/acme/widgets.git HEAD:refs/heads/main 2>&1 | tail -1"
cd $W
step $SL-lfs-store-host "git clone -q https://$H/acme/lfsrepo.git l 2>&1 | tail -1; cd l && git lfs install --local >/dev/null 2>&1; git lfs track '*.bin' >/dev/null; head -c 1024 /dev/urandom > a.bin; git add .gitattributes a.bin; git commit -qm lfs; git push -q origin HEAD:refs/heads/lenny/lfs-$TAG 2>&1 | tail -1; cd ..; rm -rf l2; git clone -q -b lenny/lfs-$TAG https://$H/acme/lfsrepo.git l2 2>&1 | tail -1; cmp l/a.bin l2/a.bin && echo same"
cd /; rm -rf $W
