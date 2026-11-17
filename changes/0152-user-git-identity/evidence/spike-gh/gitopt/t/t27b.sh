#!/bin/sh
# t27b.sh <phase>: one push, with the runtime's helper still holding the old value, so the only change is netagent's data.
. /t/env.sh; . /t/lib.sh
export GIT_CONFIG_GLOBAL=$HOME/t27.gitconfig; cp /t/cfg/Tb.gitconfig $GIT_CONFIG_GLOBAL; H=ghes.example.test; TL=2; STEPT=25
printf 'https://x:USER-FILE-OLD-HELD@%s\n' $H > $HOME/.git-credentials
cd /tmp; [ -d live ] || git clone -q https://$H/acme/widgets.git live; cd live; git checkout -q lenny/live 2>/dev/null || git checkout -q -b lenny/live
git commit -q --allow-empty -m "$1"
step live-$1 "git -c credential.helper=store push -q origin HEAD:refs/heads/lenny/live-$$ 2>&1 | tail -1"
