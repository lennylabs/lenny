#!/bin/sh
# t21.sh <session-label>: a token the runtime holds, under T, with the session state netagent was started with.
. /t/env.sh; . /t/lib.sh
SL=$1; export GIT_CONFIG_GLOBAL=$HOME/t21.gitconfig; cp /t/cfg/Tb.gitconfig $GIT_CONFIG_GLOBAL
W=$(mktemp -d); cd $W; TL=2; STEPT=40; TOK=USER-FILE-TOKEN; H=ghes.example.test; TAG=$SL-$(date +%s)
printf 'https://x:%s@%s\nhttps://x:%s@github.com\n' $TOK $H $TOK > $HOME/.git-credentials
PH="-c credential.helper=store"; EH="-c http.extraHeader=Authorization:\ Bearer\ $TOK"
step $SL-clone-no-credential "git clone -q https://$H/acme/widgets.git n 2>&1 | tail -1; ls n >/dev/null && echo cloned"
step $SL-clone-helper "git $PH clone -q https://$H/acme/widgets.git c 2>&1 | tail -1; ls c >/dev/null && echo cloned"
cd c || exit 0
git checkout -q -b lenny/f-$TAG; git commit -q --allow-empty -m e
step $SL-push-no-credential "git push -q origin HEAD 2>&1 | tail -1; echo rc=\$?"
git commit -q --allow-empty -m e
step $SL-push-helper "git $PH push -q origin HEAD 2>&1 | tail -1"
git commit -q --allow-empty -m e
step $SL-push-extraheader "git -c 'http.extraHeader=Authorization: Bearer $TOK' push -q origin HEAD 2>&1 | tail -1"
git commit -q --allow-empty -m e
step $SL-push-extraheader-token "git -c 'http.extraHeader=Authorization: token $TOK' push -q origin HEAD 2>&1 | tail -1"
git commit -q --allow-empty -m e
step $SL-push-url "git push -q https://x:$TOK@$H/acme/widgets.git HEAD 2>&1 | tail -1"
git commit -q --allow-empty -m e
step $SL-push-gh-helper "GH_ENTERPRISE_TOKEN=$TOK GH_HOST=$H git -c credential.helper= -c 'credential.helper=!/g/gh auth git-credential' push -q origin HEAD 2>&1 | tail -1"
git commit -q --allow-empty -m e
step $SL-push-other-token "git -c 'http.extraHeader=Authorization: Bearer OTHER-TOKEN-x9' push -q origin HEAD 2>&1 | tail -1"
git commit -q --allow-empty -m e
step $SL-push-empty-token "git -c 'http.extraHeader=Authorization: Bearer ' push -q origin HEAD 2>&1 | tail -1"
step $SL-push-main-held "git -c 'http.extraHeader=Authorization: Bearer $TOK' push -q origin HEAD:refs/heads/main 2>&1 | tail -1"
step $SL-push-main-other "git -c 'http.extraHeader=Authorization: Bearer OTHER-TOKEN-x9' push -q origin HEAD:refs/heads/main 2>&1 | tail -1"
step $SL-push-mixed-held "git -c 'http.extraHeader=Authorization: Bearer $TOK' push -q origin HEAD:refs/heads/lenny/m-$TAG HEAD:refs/heads/main 2>&1 | tail -1"
step $SL-push-delete-held "git -c 'http.extraHeader=Authorization: Bearer $TOK' push -q origin :refs/heads/feature 2>&1 | tail -1"
step $SL-push-option-held "git -c 'http.extraHeader=Authorization: Bearer $TOK' push -q -o ci.skip origin HEAD:refs/heads/lenny/o-$TAG 2>&1 | tail -1"
step $SL-push-outside-held "git -c 'http.extraHeader=Authorization: Bearer $TOK' push -q https://$H/acme/secret.git HEAD:refs/heads/lenny/x 2>&1 | tail -1"
step $SL-push-readonly-held "git -c 'http.extraHeader=Authorization: Bearer $TOK' push -q https://$H/acme/lib.git HEAD:refs/heads/lenny/x 2>&1 | tail -1"
step $SL-read-outside-held "git -c 'http.extraHeader=Authorization: Bearer $TOK' ls-remote https://$H/acme/secret.git 2>&1 | tail -1"
step $SL-read-held "git -c 'http.extraHeader=Authorization: Bearer $TOK' ls-remote https://$H/acme/widgets.git 2>&1 | tail -1 | cut -c1-60"
python3 -I -c "l=b'0'*40+b' '+b'1'*40+b' refs/heads/main\\0 report-status\\n'; open('/tmp/rp','wb').write(b'%04x'%(len(l)+4)+l+b'0000')"
step $SL-curl-held-post-main "curl -sS -m 10 -o /tmp/b -w http=%{http_code} --cacert /s/bundle.pem -x 127.0.0.1:3128 -H 'Authorization: Bearer $TOK' -H 'Content-Type: application/x-git-receive-pack-request' --data-binary @/tmp/rp https://$H/acme/widgets.git/git-receive-pack; cut -c1-80 /tmp/b"
step $SL-reflect "curl -sS -m 10 -D /tmp/hd -o /tmp/b -w http=%{http_code} --cacert /s/bundle.pem -x 127.0.0.1:3128 -H 'Authorization: Bearer $TOK' 'https://$H/acme/reflect.git/info/refs?service=git-upload-pack'; cat /tmp/b | cut -c1-60; grep -i x-reflect /tmp/hd | cut -c1-60"
step $SL-redirect-held "git -c 'http.extraHeader=Authorization: Bearer $TOK' ls-remote https://$H/acme/moved.git 2>&1 | tail -1 | cut -c1-60"
step $SL-github-push-held "git -c 'http.extraHeader=Authorization: Bearer $TOK' push -q https://github.com/acme/widgets.git HEAD:refs/heads/lenny/gh-$TAG 2>&1 | tail -1"
step $SL-github-push-main-held "git -c 'http.extraHeader=Authorization: Bearer $TOK' push -q https://github.com/acme/widgets.git HEAD:refs/heads/main 2>&1 | tail -1"
cd $W
step $SL-lfs-held "git $PH clone -q https://$H/acme/lfsrepo.git l 2>&1 | tail -1; cd l && git lfs install --local >/dev/null 2>&1; git lfs track '*.bin' >/dev/null; head -c 1024 /dev/urandom > a.bin; git add .gitattributes a.bin; git commit -qm lfs; git $PH push -q origin HEAD:refs/heads/lenny/lfs-$TAG 2>&1 | tail -1"
step $SL-gh-list-file-token "cd c; GH_HOST=$H GH_ENTERPRISE_TOKEN=$TOK gh pr list 2>&1 | tail -1"
step $SL-gh-merge-file-token "cd c; GH_HOST=$H GH_ENTERPRISE_TOKEN=$TOK gh pr merge 7 --merge 2>&1 | tail -1 | cut -c1-120"
cd /; rm -rf $W
