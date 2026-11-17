#!/bin/sh
# Test 1: generic insteadOf rewrite, observed against a logging listener.
cd /spike/t1; rm -f req.log; : > req.log
python3 logsrv.py 7070 /spike/t1/req.log & SRV=$!; sleep 0.5
export HOME=/tmp/h1; rm -rf $HOME; mkdir -p $HOME
export GIT_CONFIG_GLOBAL=/spike/t1/gitconfig GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1
cat > $GIT_CONFIG_GLOBAL <<'C'
[url "http://127.0.0.1:7070/"]
    insteadOf = https://
[url "http://127.0.0.1:7070/ssh/"]
    insteadOf = ssh://
[url "http://127.0.0.1:7070/scp/"]
    insteadOf = git@
[user]
    name = alice
    email = alice@acme.com
[credential]
    helper =
[protocol]
    version = 2
C
rm -rf /tmp/w1 && git init -q /tmp/w1 && cd /tmp/w1 && git commit -q --allow-empty -m x
for u in https://github.com/org/repo https://git.kernel.org/pub/scm/git/git.git \
         https://gitlab.example.com/group/sub/repo.git https://host:8443/x.git \
         git@github.com:org/repo.git ssh://git@host/a/b.git ssh://git@host:2222/a/b.git \
         git@gitlab.example.com:group/sub/repo.git ; do
  echo "== $u"
  echo "fetch: $(git ls-remote "$u" 2>&1 | head -1)"
  echo "push : $(git push "$u" HEAD:refs/heads/main 2>&1 | head -1)"
done
echo "== resolved URLs (git config --get-urlmatch is not a rewrite; use remote get-url)"
for u in https://host:8443/x.git git@github.com:org/repo.git ssh://git@host:2222/a/b.git; do
  git remote remove r 2>/dev/null; git remote add r "$u"; echo "$u -> $(git remote get-url r) | push $(git remote get-url --push r)"
done
echo "== longest-prefix with a per-host block"
cat >> $GIT_CONFIG_GLOBAL <<'C'
[url "http://127.0.0.1:7070/special-github/"]
    insteadOf = https://github.com/
C
git remote remove r; git remote add r https://github.com/org/repo; echo "github -> $(git remote get-url r)"
git remote remove r; git remote add r https://gitlab.com/a/b; echo "gitlab -> $(git remote get-url r)"
# Order independence: put the specific block FIRST and the generic after
cat > /tmp/gc2 <<'C'
[url "http://127.0.0.1:7070/special-github/"]
    insteadOf = https://github.com/
[url "http://127.0.0.1:7070/"]
    insteadOf = https://
C
GIT_CONFIG_GLOBAL=/tmp/gc2 sh -c 'git remote remove r; git remote add r https://github.com/org/repo; echo "reordered github -> $(git remote get-url r)"'
# http:// remotes are not covered by https:// rule
git remote remove r; git remote add r http://example.com/a.git; echo "http -> $(git remote get-url r)"
# Does the rewritten URL get rewritten again (no chaining)? loopback URL starts with http:// so not https:// - n/a; test chain explicitly
cat > /tmp/gc3 <<'C'
[url "https://b/"]
    insteadOf = https://a/
[url "http://127.0.0.1:7070/"]
    insteadOf = https://
C
GIT_CONFIG_GLOBAL=/tmp/gc3 sh -c 'git remote remove r; git remote add r https://a/x; echo "chain https://a/x -> $(git remote get-url r)"'
echo "== recommended scheme-prefixed config"
cat > /tmp/gc4 <<'C'
[url "http://127.0.0.1:7070/https/"]
    insteadOf = https://
[url "http://127.0.0.1:7070/http/"]
    insteadOf = http://
[url "http://127.0.0.1:7070/ssh/"]
    insteadOf = ssh://
[url "http://127.0.0.1:7070/scp/"]
    insteadOf = git@
[url "http://127.0.0.1:7070/"]
    insteadOf = http://127.0.0.1:7070/
C
for u in https://github.com/org/repo http://example.com/a.git http://127.0.0.1:7070/https/github.com/o/r ssh://host/a.git ssh://git@[::1]:22/a.git git@[::1]:a.git alice@host:a.git host:a.git; do
  GIT_CONFIG_GLOBAL=/tmp/gc4 sh -c "git remote remove r; git remote add r '$u'; echo '$u -> '\$(git remote get-url r)"
done
kill $SRV
echo "== request log"; cat /spike/t1/req.log | python3 -c 'import sys,json;[print(j["m"],j["path"],"gp=",j["gp"]) for j in map(json.loads,sys.stdin)]'
