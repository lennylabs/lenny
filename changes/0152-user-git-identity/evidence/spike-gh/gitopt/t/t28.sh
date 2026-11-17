#!/bin/sh
. /t/env.sh
export GIT_CONFIG_GLOBAL=$HOME/t27.gitconfig; cp /t/cfg/Tb.gitconfig $GIT_CONFIG_GLOBAL; H=ghes.example.test; cd /tmp
timeout -k 3 20 git clone -q https://$H/acme/widgets.git c$$ 2>&1 | tail -1; test -d c$$ && echo cloned; cd c$$ 2>/dev/null || exit 0; git checkout -q -b lenny/t28-$$; git commit -q --allow-empty -m e; timeout -k 3 20 git push -q origin HEAD 2>&1 | tail -1; echo push-done
