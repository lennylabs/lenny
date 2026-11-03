#!/bin/sh
# t33.sh <repo> <label> [limit]: git-lfs 3.6.0 against an object the batch response marks with an error, on a clone and on a push. The step timeout is a limit on the client, and rc=124 means it did not exit.
. /t/env.sh; . /t/lib.sh
RP=$1; L=$2; H=gitlab.example.test; export GIT_CONFIG_GLOBAL=$HOME/t.gitconfig; cp /t/cfg/Tb.gitconfig $GIT_CONFIG_GLOBAL; TL=${3:-30}; export TL=8; STEPT=${3:-30}; W=$(mktemp -d); cd $W; TAG=$L-$$
git lfs install --skip-repo >/dev/null 2>&1
[ "$ONLY" = push ] || step $L-clone "(git clone -q https://$H/$RP.git c 2>&1; echo git-exit=\$?) | tail -7 | cut -c1-200"
GIT_LFS_SKIP_SMUDGE=1 git clone -q https://$H/$RP.git p 2>/dev/null; cd p || exit 0
git lfs install --local >/dev/null 2>&1; head -c 2048 /dev/urandom > a.bin; git add a.bin; git commit -qm lfs
[ "$ONLY" = clone ] || step $L-push "(git push origin HEAD:refs/heads/lenny/$TAG 2>&1; echo git-exit=\$?) | tail -7 | cut -c1-200"
cd /; rm -rf $W
