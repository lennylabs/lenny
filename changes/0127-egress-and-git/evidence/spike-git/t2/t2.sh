#!/bin/sh
# Test 2: pushurl vs insteadOf/pushInsteadOf, explicit URL push, ls-remote, submodules.
cd /spike/t2; : > req.log
python3 /spike/t1/logsrv.py 7071 /spike/t2/req.log & SRV=$!; sleep 0.5
export HOME=/tmp/h2; rm -rf $HOME; mkdir -p $HOME GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1
export GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1
ID='[user]
    name = alice
    email = alice@acme.com'
mk() { rm -rf /tmp/w2; git init -q /tmp/w2; cd /tmp/w2; git commit -q --allow-empty -m x; }
show() { echo "  fetch=$(git remote get-url origin)  push=$(git remote get-url --push origin)"; }

echo "### A: single insteadOf, remote with pushurl"
export GIT_CONFIG_GLOBAL=/tmp/gA; printf '%s\n[url "http://127.0.0.1:7071/https/"]\n    insteadOf = https://\n' "$ID" > $GIT_CONFIG_GLOBAL
mk; git remote add origin https://github.com/x/y; git config remote.origin.pushurl https://github.com/x/y; show
git push origin HEAD:main 2>&1 | head -1
echo "### A2: pushurl pointing at a different host"
git config remote.origin.pushurl https://evil.example.com/x/y; show
git push origin HEAD:main 2>&1 | head -1

echo "### B: insteadOf (fetch) + pushInsteadOf (push to a different endpoint), remote WITHOUT pushurl"
export GIT_CONFIG_GLOBAL=/tmp/gB; printf '%s\n[url "http://127.0.0.1:7071/F/"]\n    insteadOf = https://\n[url "http://127.0.0.1:7071/P/"]\n    pushInsteadOf = https://\n' "$ID" > $GIT_CONFIG_GLOBAL
mk; git remote add origin https://github.com/x/y; show
echo "### B2: same config, remote WITH pushurl -> pushInsteadOf ignored, insteadOf applies"
git config remote.origin.pushurl https://github.com/x/y; show
git push origin HEAD:main 2>&1 | head -1

echo "### C: explicit URL push and ls-remote (config A)"
export GIT_CONFIG_GLOBAL=/tmp/gA
git push https://github.com/x/explicit HEAD:main 2>&1 | head -1
git ls-remote https://github.com/x/lsremote 2>&1 | head -1
echo "### C2: explicit URL push with config B (pushInsteadOf applies to explicit URL?)"
GIT_CONFIG_GLOBAL=/tmp/gB git push https://github.com/x/explicitB HEAD:main 2>&1 | head -1
echo "### C3: GIT_TRACE_CURL excerpt for a push"
GIT_TRACE_CURL=1 GIT_TRACE_CURL_NO_DATA=1 git push https://github.com/x/trace HEAD:main 2>&1 | grep -E '=> Send header: (GET|POST)|Connected to' | head -3
kill $SRV
echo "== request log"; python3 -c 'import sys,json;[print(j["m"],j["path"]) for j in map(json.loads,open("/spike/t2/req.log"))]'
