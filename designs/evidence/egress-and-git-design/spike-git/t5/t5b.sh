#!/bin/sh
# Test 5b: git-lfs push endpoint when remote.origin.pushurl is set alongside insteadOf and pushInsteadOf.
cd /spike/t5; : > req2.log
python3 lfssrv.py 7077 /spike/t5/req2.log & S1=$!; sleep 0.4
export HOME=/tmp/h5 GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0 GIT_CONFIG_GLOBAL=/tmp/h5/.gitconfig HTTPS_PROXY=http://127.0.0.1:9 NO_PROXY=127.0.0.1
cd /tmp/w5; git remote set-url origin https://github.com/org/lfsrepo
git config remote.origin.pushurl https://github.com/org/lfsrepo
echo "git push url: $(git remote get-url --push origin)"
git lfs push origin main >/dev/null 2>&1
git config --unset remote.origin.pushurl
kill $S1; python3 -c 'import json;[print(j["m"],j["path"],"op=",j.get("op")) for j in map(json.loads,open("/spike/t5/req2.log"))]' | sort | uniq -c
