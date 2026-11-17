#!/bin/bash
# Item 2: git push through Envoy's raw tunnel to an address the decision pins.
D=$(cd "$(dirname "$0")" && pwd)
P=http://lenny:pin@198.19.50.41:3128; U=https://git.internal.oa.test
G="git -c http.proxyAuthMethod=basic -c user.name=alice -c user.email=alice@acme.com"
ce(){ docker exec -e https_proxy=$P -e GIT_SSL_CAINFO=/c/ca.pem -e GIT_TERMINAL_PROMPT=0 oa-gitc "$@"; }
ced(){ docker exec -e GIT_SSL_CAINFO=/c/ca.pem -e GIT_TERMINAL_PROMPT=0 oa-gitd "$@"; }
srv(){ docker exec oa-git "$@"; }
log(){ docker logs oa-git 2>&1 | tail -n ${1:-3} | cut -c21-230; }
tm(){ python3 -c 'import time;print(time.time())'; }
newbare(){ srv sh -c "git init -q --bare /repos/$1.git && git -C /repos/$1.git symbolic-ref HEAD refs/heads/main"; }
peak(){ docker exec oa-envoy cat /sys/fs/cgroup/memory.peak; }
echo "### 0. controls: the client has no route and no name for the origin"
ce sh -c 'unset https_proxy; curl -sS --max-time 3 -o /dev/null https://git.internal.oa.test/ ; echo "curl by name rc=$?"; curl -sS --max-time 3 -o /dev/null -k https://172.30.50.21/ ; echo "curl by address rc=$?"'
newbare small; 
echo "### 1. small push, content-length POST"
ce sh -c "mkdir -p /w/small && cd /w/small && git init -q -b main && echo one > f && git add f && $G commit -qm one && $G push -q $U/small.git main 2>&1; echo push rc=\$?; git rev-parse HEAD"
srv git -C /repos/small.git rev-parse main
log 3
echo "### 2. clone, then a second push from the clone"
ce sh -c "cd /w && $G clone -q $U/small.git c2 2>&1; cd c2 && echo two >> f && git add f && $G commit -qm two && $G push -q origin main 2>&1; echo push rc=\$?; git rev-parse HEAD"
srv git -C /repos/small.git rev-parse main
echo "### 3. 64 MiB random: over http.postBuffer, so git sends a chunked POST"
newbare mid
ce sh -c "mkdir -p /w/mid && cd /w/mid && git init -q -b main && head -c 67108864 /dev/urandom > blob && git add blob && $G commit -qm mid && git rev-parse HEAD; GIT_TRACE_CURL=1 $G push -q $U/mid.git main 2>&1 | grep -aiE 'Send header: (Transfer-Encoding|Content-Length|Content-Encoding|Expect)' | sort | uniq -c; echo pipe rc=\${PIPESTATUS}"
srv git -C /repos/mid.git rev-parse main; srv git -C /repos/mid.git fsck --full 2>&1 | tail -1
log 2
echo "### 4. same content with http.postBuffer raised, so the POST carries a content-length"
newbare midcl
ce sh -c "cd /w/mid && $G -c http.postBuffer=134217728 push -q $U/midcl.git main 2>&1; echo push rc=\$?"
srv git -C /repos/midcl.git rev-parse main; log 1
echo "### 5. 1 GiB random, through the tunnel and directly"
newbare bigt; newbare bigd
ce sh -c "mkdir -p /w/big && cd /w/big && git init -q -b main && head -c 1073741824 /dev/urandom > blob && git -c core.compression=0 add blob && $G commit -qm big && git rev-parse HEAD"
docker exec oa-gitc tar -C /w -cf - big | docker exec -i oa-gitd sh -c 'mkdir -p /w && tar -C /w -xf -'
ced sh -c "cd /w/big; git rev-parse HEAD"
p0=$(peak)
t0=$(tm); ce sh -c "cd /w/big && $G push -q $U/bigt.git main 2>&1; echo tunnel push rc=\$?"; t1=$(tm)
python3 -c "print('tunnel push seconds', round($t1-$t0,1))"
log 1
echo "envoy memory.peak before=$p0 after=$(peak)"
srv git -C /repos/bigt.git rev-parse main; srv git -C /repos/bigt.git fsck --full 2>&1 | tail -1
t0=$(tm); ced sh -c "cd /w/big && $G push -q $U/bigd.git main 2>&1; echo direct push rc=\$?"; t1=$(tm)
python3 -c "print('direct push seconds', round($t1-$t0,1))"
log 1
echo "### 6. a pre-receive rejection reaches the client, and the ref does not move"
srv sh -c 'cat > /repos/small.git/hooks/pre-receive <<H
#!/bin/sh
echo "policy: refused by hook" >&2
exit 1
H
chmod +x /repos/small.git/hooks/pre-receive'
ce sh -c "cd /w/c2 && echo three >> f && git add f && $G commit -qm three && $G push origin main 2>&1 | grep -aE 'remote:|rejected|error' ; echo local \$(git rev-parse --short HEAD)"
echo "server $(srv git -C /repos/small.git rev-parse --short main)"
srv rm /repos/small.git/hooks/pre-receive
echo "### 7. ref creation and deletion"
ce sh -c "cd /w/c2 && $G push -q origin HEAD:refs/heads/topic 2>&1; echo create rc=\$?; $G push -q origin :refs/heads/topic 2>&1; echo delete rc=\$?; $G ls-remote origin"
echo "### 8. a denied decision, an unpinned credential, and the decision service down"
ce sh -c "cd /w/c2 && https_proxy=http://lenny:bad@198.19.50.41:3128 $G push origin main 2>&1 | tail -1 | cut -c1-160"
ce sh -c "cd /w/c2 && unset https_proxy; https_proxy=http://198.19.50.41:3128 $G -c http.proxyAuthMethod=anyauth push origin main 2>&1 | tail -1 | cut -c1-160"
docker stop -t0 oa-authz >/dev/null; /bin/sleep 1
ce sh -c "cd /w/c2 && $G push origin main 2>&1 | tail -1 | cut -c1-160"
docker start oa-authz >/dev/null; /bin/sleep 2
echo "### 9. Envoy killed during a 1 GiB push: the ref must not move, and a retry must land"
newbare bigk
docker exec -d -e https_proxy=$P -e GIT_SSL_CAINFO=/c/ca.pem -e GIT_TERMINAL_PROMPT=0 oa-gitc sh -c "cd /w/big && $G push -q $U/bigk.git main > /tmp/k.out 2>&1; echo rc=\$? >> /tmp/k.out"
/bin/sleep 2.5; docker rm -f oa-envoy >/dev/null; /bin/sleep 3
docker exec oa-gitc cut -c1-200 /tmp/k.out
echo "server ref after cut: [$(srv sh -c 'git -C /repos/bigk.git rev-parse --verify -q main || echo none')] quarantine dirs: $(srv sh -c 'ls /repos/bigk.git/objects | grep -c incoming')"
log 1
bash $D/envoy.sh final.json
t0=$(tm); ce sh -c "cd /w/big && $G push -q $U/bigk.git main 2>&1; echo retry rc=\$?"; t1=$(tm); python3 -c "print('retry seconds', round($t1-$t0,1))"
srv git -C /repos/bigk.git rev-parse main
echo "### 10. max_stream_duration 5 s cuts a large push cleanly"
bash $D/envoy.sh msd5.json
newbare bigm
t0=$(tm); ce sh -c "cd /w/big && $G push $U/bigm.git main 2>&1 | tail -2 | cut -c1-200"; t1=$(tm); python3 -c "print('seconds until the client returned', round($t1-$t0,1))"
echo "server ref: [$(srv sh -c 'git -C /repos/bigm.git rev-parse --verify -q main || echo none')]"
docker logs oa-envoy 2>&1 | grep -a '"authority":"git.internal' | tail -1 | cut -c1-330
echo "### 11. a silent pre-receive hook against stream_idle_timeout 10 s"
bash $D/envoy.sh sit10.json
newbare slow; srv sh -c 'cat > /repos/slow.git/hooks/pre-receive <<H
#!/bin/sh
sleep 25
exit 0
H
chmod +x /repos/slow.git/hooks/pre-receive'
ce sh -c "mkdir -p /w/slow && cd /w/slow && git init -q -b main && echo a > f && git add f && $G commit -qm a"
t0=$(tm); ce sh -c "cd /w/slow && $G push $U/slow.git main 2>&1 | tail -1 | cut -c1-160"; t1=$(tm); python3 -c "print('receive.keepAlive default: seconds', round($t1-$t0,1))"
echo "server ref: [$(srv sh -c 'git -C /repos/slow.git rev-parse --short -q --verify main || echo none')]"
newbare slow0; srv sh -c 'cp /repos/slow.git/hooks/pre-receive /repos/slow0.git/hooks/pre-receive; git -C /repos/slow0.git config receive.keepAlive -1'
t0=$(tm); ce sh -c "cd /w/slow && $G push $U/slow0.git main 2>&1 | tail -2 | cut -c1-200"; t1=$(tm); python3 -c "print('keepalive off: seconds', round($t1-$t0,1))"
/bin/sleep 16; echo "server ref after hook finished: [$(srv sh -c 'git -C /repos/slow0.git rev-parse --short -q --verify main || echo none')]"
echo "### 12. proxy hop over TLS (I2), 64 MiB push"
bash $D/envoy.sh final-tls.json
newbare midt
ce sh -c "cd /w/mid && https_proxy=https://lenny:pin@198.19.50.41:3128 $G -c http.proxySSLCAInfo=/c/ca.pem -c http.proxySSLCertPasswordProtected=false push -q $U/midt.git main 2>&1; echo rc=\$?" 
srv git -C /repos/midt.git rev-parse main; log 1
