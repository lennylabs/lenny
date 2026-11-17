#!/bin/sh
# t7.sh <config> <url> <tag> <runs>: wall time of a 64 MiB clone, a small ls-remote, and a 64 MiB push.
. /t/env.sh
CFG=$1; U=$2; TAG=$3; N=${4:-3}; export GIT_CONFIG_GLOBAL=/t/cfg/$CFG.gitconfig; [ "$CAINFO" = design ] && export GIT_SSL_CAINFO=/s/bundle.pem
ms(){ echo $(( $(date +%s%N) / 1000000 )); }
W=$(mktemp -d); cd $W; i=0
while [ $i -lt $N ]; do i=$((i+1))
 s=$(ms); git clone -q $U c$i >/tmp/o 2>&1; r=$?; e=$(ms); echo "$TAG clone64 run=$i rc=$r ms=$((e-s)) $(tail -1 /tmp/o | cut -c1-100)"
 rm -rf c$i
done
s=$(ms); for k in 1 2 3 4 5 6 7 8 9 10; do git ls-remote ${U%/big.git}/${SMALL:-widgets}.git >/dev/null 2>&1; done; e=$(ms); echo "$TAG lsremote10 ms=$((e-s))"
if [ "$PUSH" = 1 ]; then git clone -q --single-branch -b main $U pc 2>/dev/null; cd pc; git config user.email a@b; git config user.name a; git checkout -q -b lenny/cost-$TAG-$$; head -c 67108864 /dev/urandom > n; git add n; git commit -qm n; s=$(ms); git push -q origin HEAD >/tmp/o 2>&1; r=$?; e=$(ms); echo "$TAG push64 rc=$r ms=$((e-s)) $(tail -1 /tmp/o | cut -c1-100)"; cd ..; fi
cd /; rm -rf $W
