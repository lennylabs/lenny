#!/bin/sh
# Test 2b: submodules (absolute and relative URLs) and go get through the rewrite.
cd /spike/t2; : > req2.log
python3 /spike/t1/logsrv.py 7072 /spike/t2/req2.log & SRV=$!; sleep 0.5
export HOME=/tmp/h2b; rm -rf $HOME; mkdir -p $HOME; export GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/tmp/gS; printf '[user]\n    name = alice\n    email = alice@acme.com\n[protocol "file"]\n    allow = always\n[url "http://127.0.0.1:7072/https/"]\n    insteadOf = https://\n[url "http://127.0.0.1:7072/scp/"]\n    insteadOf = git@\n' > $GIT_CONFIG_GLOBAL
rm -rf /tmp/s; mkdir /tmp/s; cd /tmp/s
git init -q sub && git -C sub commit -q --allow-empty -m s
git init -q super && cd super && git commit -q --allow-empty -m s
git -c protocol.file.allow=always submodule -q add /tmp/s/sub abs >/dev/null 2>&1
git -c protocol.file.allow=always submodule -q add /tmp/s/sub rel >/dev/null 2>&1
git -c protocol.file.allow=always submodule -q add /tmp/s/sub scp >/dev/null 2>&1
git config -f .gitmodules submodule.abs.url https://github.com/org/absmod
git config -f .gitmodules submodule.rel.url ../relmod.git
git config -f .gitmodules submodule.scp.url git@gitlab.com:grp/scpmod.git
git commit -qam mods
cd /tmp/s && git clone -q --no-recurse-submodules /tmp/s/super clone && cd clone
git remote set-url origin https://github.com/org/super.git
git submodule init >/dev/null 2>&1
echo "stored submodule URLs:"; git config --get-regexp 'submodule\..*\.url'
git submodule update 2>&1 | grep -E "fatal|Cloning" | head -6
echo "### go get with GOPRIVATE, GOPROXY=direct"
cd /tmp && mkdir -p gm && cd gm && go mod init example.com/m >/dev/null 2>&1
GOFLAGS=-mod=mod GOPRIVATE='github.com/acme/*' GOPROXY=direct GONOSUMDB='*' go get github.com/acme/widget@latest 2>&1 | head -3
kill $SRV
echo "== request log"; python3 -c 'import sys,json;[print(j["m"],j["path"]) for j in map(json.loads,open("/spike/t2/req2.log"))]'
