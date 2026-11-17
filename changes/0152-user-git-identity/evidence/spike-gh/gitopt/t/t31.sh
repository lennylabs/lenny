#!/bin/sh
# t31.sh <host> <repo> <state>: git-lfs itself, against GitLab's form.
. /t/env.sh; . /t/lib.sh
H=$1; RP=$2; L=$3; export GIT_CONFIG_GLOBAL=$HOME/t.gitconfig; cp /t/cfg/Tb.gitconfig $GIT_CONFIG_GLOBAL; TL=2; STEPT=${ST:-30}; W=$(mktemp -d); cd $W; TAG=$L-$(date +%s)
step $L-clone "git clone -q https://$H/$RP.git a 2>&1 | tail -1"
cd a || exit 0
git lfs install --local >/dev/null 2>&1; git lfs track '*.bin' >/dev/null; head -c 2048 /dev/urandom > a.bin; git add .gitattributes a.bin; git commit -qm lfs
step $L-lfs-push "GIT_TRACE=1 GIT_CURL_VERBOSE=1 GIT_TRANSFER_TRACE=1 git push origin HEAD:refs/heads/lenny/lfs-$TAG > /tmp/trace-$$ 2>&1; echo push-rc=\$?; grep -E 'batch|Uploading|error|LFS:' /tmp/trace-$$ | tail -2 | cut -c1-140; echo leak=\$(grep -cE 'LFSTOKEN|Z2l0bGFiLWxmczpMRlNUT0tFTi' /tmp/trace-$$)"
cd $W
step $L-lfs-smudge "GIT_TRACE=1 GIT_CURL_VERBOSE=1 git clone -q -b lenny/lfs-$TAG https://$H/$RP.git b > /tmp/trace2-$$ 2>&1; echo clone-rc=\$?; cmp a/a.bin b/a.bin && echo same; echo leak=\$(grep -cE 'LFSTOKEN|Z2l0bGFiLWxmczpMRlNUT0tFTi' /tmp/trace2-$$); grep -E 'smudge filter|error' /tmp/trace2-$$ | tail -1 | cut -c1-140"
cd /; rm -rf $W
