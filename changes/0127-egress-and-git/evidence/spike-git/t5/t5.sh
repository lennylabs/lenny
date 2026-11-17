#!/bin/sh
# Test 5: where git-lfs sends batch requests when the remote URL is rewritten by insteadOf/pushInsteadOf.
cd /spike/t5; : > req.log
python3 lfssrv.py 7077 /spike/t5/req.log & S1=$!
python3 lfssrv.py 7078 /spike/t5/req.log & S2=$!   # stands in for the egress proxy (HTTPS_PROXY)
sleep 0.5
export HOME=/tmp/h5 GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0 GIT_CONFIG_GLOBAL=/tmp/h5/.gitconfig
rm -rf $HOME /tmp/w5; mkdir -p $HOME
git config --global user.name alice; git config --global user.email alice@acme.com; git config --global credential.helper ''
git lfs install --skip-repo >/dev/null
git config --global url."http://127.0.0.1:7077/https/".insteadOf https://
git config --global url."http://127.0.0.1:7077/scp/".insteadOf git@
git init -q -b main /tmp/w5; cd /tmp/w5
git lfs track '*.bin' >/dev/null; head -c 1000 /dev/urandom > a.bin; git add .gitattributes a.bin; git commit -qm lfs
env() { git lfs env 2>/dev/null | grep -E '^(Endpoint|PushEndpoint|  SSH)' | sed 's/^/   /'; }
echo "## A: origin=https://github.com/org/lfsrepo, insteadOf only"; git remote add origin https://github.com/org/lfsrepo; env
echo "## B: + pushInsteadOf to /P/"; git config --global url."http://127.0.0.1:7077/P/".pushInsteadOf https://; env
echo "## C: + remote.origin.pushurl"; git config remote.origin.pushurl https://github.com/org/lfsrepo; env; git config --unset remote.origin.pushurl
echo "## D: scp remote git@github.com:org/lfsrepo.git"; git remote set-url origin git@github.com:org/lfsrepo.git; env
echo "## E: explicit lfs.url=https://lfs.example.com/org/repo"; git remote set-url origin https://github.com/org/lfsrepo; git config lfs.url https://lfs.example.com/org/repo; env; git config --unset lfs.url
echo "## F: git lfs push (upload) and fetch (download), HTTPS_PROXY=logger:7078"
export HTTPS_PROXY=http://127.0.0.1:7078 NO_PROXY=127.0.0.1
GIT_TRACE=0 git lfs push origin main 2>&1 | tail -2
rm -rf .git/lfs/objects; git lfs fetch origin main 2>&1 | tail -2
echo "## G: lfs push with scp remote"
git remote set-url origin git@github.com:org/lfsrepo.git; git lfs push origin main 2>&1 | tail -1
kill $S1 $S2
echo "== request log"; python3 -c 'import json;[print(j["port"],j["m"],j["path"],"op=",j.get("op"),"ref=",j.get("ref")) for j in map(json.loads,open("/spike/t5/req.log"))]'
