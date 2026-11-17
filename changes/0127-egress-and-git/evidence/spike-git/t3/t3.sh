#!/bin/sh
# Test 3: forwarding prototype. Arg 1: upstream mode (cgi|direct).
MODE=${1:-cgi}; cd /spike/t3; L=/spike/t3/logs-$MODE; rm -rf $L; mkdir -p $L
pkill -f bin/upstream; pkill -f bin/proxy; sleep 0.3
/spike/t3/bin/upstream -mode $MODE 2> $L/upstream.log &
/spike/t3/bin/proxy -routes 'github.com=http://localhost:8080,moved301.test=http://127.0.0.1:8080/moved301,moved307.test=http://127.0.0.1:8080/moved307' \
   -allow 'github.com/org/repo,moved301.test/org/repo,moved307.test/org/repo' 2> $L/proxy.log &
sleep 0.5; PPID_=$(pgrep -f 'bin/proxy -routes')
( while kill -0 $PPID_ 2>/dev/null; do awk '/VmRSS/{print $2}' /proc/$PPID_/status; sleep 0.1; done ) > $L/rss.txt &
export HOME=/tmp/h3; rm -rf $HOME /tmp/w3; mkdir -p $HOME /tmp/w3; cd /tmp/w3
export GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0 GIT_CONFIG_GLOBAL=$HOME/.gitconfig
cat > $GIT_CONFIG_GLOBAL <<'C'
[user]
    name = alice
    email = alice@acme.com
[url "http://127.0.0.1:7073/https/"]
    insteadOf = https://
[credential]
    helper =
[protocol]
    version = 2
C
t() { s=$(date +%s.%N); "$@" > /tmp/w3/out 2>&1; rc=$?; e=$(date +%s.%N); printf '%-60s rc=%d %.2fs %s\n' "$*" $rc $(echo "$e - $s" | bc) "$(grep -E 'fatal|error|rejected|->' /tmp/w3/out | head -1)"; }
mark() { echo "--- $1" >> $L/proxy.log; echo "--- $1" >> $L/upstream.log; }
mark clone;   t git clone -q https://github.com/org/repo full
mark fetch;   (cd full && t git fetch -q origin)
mark partial; t git clone -q --no-checkout --filter=blob:none https://github.com/org/repo partial
echo "   partial: missing blobs=$(git -C partial rev-list --objects --missing=print --all | grep -c "^?") size=$(du -sk partial/.git | cut -f1)KB promisor=$(git -C partial config remote.origin.promisor) filter=$(git -C partial config remote.origin.partialclonefilter)"
mark shallow; t git clone -q --depth 1 https://github.com/org/repo shallow
echo "   shallow: $(git -C shallow rev-list --count HEAD) commit(s), shallow file=$(test -f shallow/.git/shallow && echo yes)"
mark pushsmall; (cd full && echo hi > s.txt && git add s.txt && git commit -qm small && t git push -q origin HEAD:main)
mark pushlarge; (cd full && head -c 52428800 /dev/urandom > l.bin && git add l.bin && git commit -qm large && t git push -q origin HEAD:main)
echo "   upstream main has l.bin: $(git --git-dir=/srv/git/org/repo.git ls-tree --name-only main | grep -c l.bin)"
mark refused; t git clone -q https://github.com/org/other other
mark dumb;    echo "   dumb GET HEAD: $(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:7073/https/github.com/org/repo.git/HEAD)  traversal: $(curl -s --path-as-is -o /dev/null -w '%{http_code}' 'http://127.0.0.1:7073/https/github.com/org/repo/../other.git/info/refs?service=git-upload-pack')  encoded: $(curl -s -o /dev/null -w '%{http_code}' 'http://127.0.0.1:7073/https/github.com/org%2Fother.git/info/refs?service=git-upload-pack')"
mark bigclone; t git clone -q https://github.com/org/repo full2
mark redirect301; t git ls-remote https://moved301.test/org/repo
mark redirect307; t git ls-remote https://moved307.test/org/repo
sleep 0.3
echo "proxy RSS KB: start=$(head -1 $L/rss.txt) max=$(sort -n $L/rss.txt | tail -1) samples=$(wc -l < $L/rss.txt) VmHWM=$(awk '/VmHWM/{print $2}' /proc/$PPID_/status)"
pkill -f bin/upstream; pkill -f bin/proxy
