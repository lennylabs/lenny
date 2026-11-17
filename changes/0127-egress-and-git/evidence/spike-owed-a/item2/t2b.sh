#!/bin/bash
# Item 2 second pass: HTTP/1.1 so the POST is truly chunked, exit codes on the cut cases, and the proxy hop over TLS.
D=$(cd "$(dirname "$0")" && pwd)
P=http://lenny:pin@198.19.50.41:3128; U=https://git.internal.oa.test
G="git -c http.proxyAuthMethod=basic -c user.name=alice -c user.email=alice@acme.com"
ce(){ docker exec -e https_proxy=$P -e GIT_SSL_CAINFO=/c/ca.pem -e GIT_TERMINAL_PROMPT=0 oa-gitc "$@"; }
srv(){ docker exec oa-git "$@"; }
log(){ docker logs oa-git 2>&1 | tail -n ${1:-3} | cut -c21-230; }
tm(){ python3 -c 'import time;print(time.time())'; }
newbare(){ srv sh -c "git init -q --bare /repos/$1.git && git -C /repos/$1.git symbolic-ref HEAD refs/heads/main"; }
peak(){ docker exec oa-envoy cat /sys/fs/cgroup/memory.peak; }
bash $D/envoy.sh final.json
echo "### 3b. 64 MiB, http.version=HTTP/1.1: a chunked POST on the wire"
newbare mid11
ce sh -c "cd /w/mid && GIT_TRACE_CURL=1 $G -c http.version=HTTP/1.1 push -q $U/mid11.git main 2>/tmp/tr; echo push rc=\$?; grep -aiE 'Send header: (Transfer-Encoding|Content-Length|Expect)' /tmp/tr | sed 's/^.*Send header/Send header/' | sort | uniq -c"
srv git -C /repos/mid11.git rev-parse main; srv git -C /repos/mid11.git fsck --full 2>&1 | tail -1; log 2
echo "### 5b. 1 GiB, HTTP/1.1 chunked, through the tunnel"
newbare big11
p0=$(peak); t0=$(tm); ce sh -c "cd /w/big && $G -c http.version=HTTP/1.1 push -q $U/big11.git main 2>&1; echo rc=\$?"; t1=$(tm); python3 -c "print('tunnel seconds', round($t1-$t0,1))"; echo "envoy memory.peak before=$p0 after=$(peak)"
srv git -C /repos/big11.git rev-parse main; srv git -C /repos/big11.git fsck --full 2>&1 | tail -1; log 1
docker exec oa-gitc curl -s http://198.19.50.41:9901/stats | grep -aE '^(http.egress.downstream_cx_length|cluster.origin_md.upstream_cx_total|cluster.origin_md.upstream_cx_tx_bytes_total|cluster.origin_md.upstream_cx_rx_bytes_total)' | cut -c1-120
echo "### 10b. max_stream_duration 5 s: exit code and access log"
bash $D/envoy.sh msd5.json
newbare bigm2
t0=$(tm); ce sh -c "cd /w/big && $G -c http.version=HTTP/1.1 push $U/bigm2.git main > /tmp/o 2>&1; rc=\$?; tail -3 /tmp/o | cut -c1-200; echo push rc=\$rc"; t1=$(tm); python3 -c "print('seconds until the client returned', round($t1-$t0,1))"
/bin/sleep 3; echo "server ref: [$(srv sh -c 'git -C /repos/bigm2.git rev-parse --verify -q main || echo none')]"
docker logs oa-envoy 2>&1 | grep -a 'git.internal' | grep -av DownstreamTunnelSuccessfullyEstablished | tail -2 | cut -c1-300
echo "### 11b. silent hook against stream_idle_timeout 10 s, exit codes"
bash $D/envoy.sh sit10.json
newbare sl1; newbare sl2; for r in sl1 sl2; do srv sh -c "printf '#!/bin/sh\nsleep 25\nexit 0\n' > /repos/$r.git/hooks/pre-receive; chmod +x /repos/$r.git/hooks/pre-receive"; done
srv git -C /repos/sl2.git config receive.keepAlive -1
for r in sl1 sl2; do t0=$(tm); ce sh -c "cd /w/slow && $G -c http.version=HTTP/1.1 push $U/$r.git main > /tmp/o 2>&1; rc=\$?; tail -2 /tmp/o | cut -c1-160; echo $r push rc=\$rc"; t1=$(tm); python3 -c "print('$r seconds', round($t1-$t0,1))"; echo "$r server ref at client return: [$(srv sh -c "git -C /repos/$r.git rev-parse --short -q --verify main || echo none")]"; done
/bin/sleep 18; echo "sl2 server ref after hook ended: [$(srv sh -c 'git -C /repos/sl2.git rev-parse --short -q --verify main || echo none')]"; log 1
echo "### 12b. proxy hop over TLS (I2), 64 MiB HTTP/1.1 chunked, then 1 GiB"
bash $D/envoy.sh final-tls.json
docker exec oa-gitc sh -c 'echo "198.19.50.41 lenny-egress.lenny-system.svc" >> /etc/hosts'
PT=https://lenny:pin@lenny-egress.lenny-system.svc:3128
newbare midt2; newbare bigt2
ce sh -c "cd /w/mid && https_proxy=$PT $G -c http.proxySSLCAInfo=/c/ca.pem -c http.version=HTTP/1.1 push -q $U/midt2.git main 2>&1; echo rc=\$?"; srv git -C /repos/midt2.git rev-parse main; log 1
t0=$(tm); ce sh -c "cd /w/big && https_proxy=$PT $G -c http.proxySSLCAInfo=/c/ca.pem -c http.version=HTTP/1.1 push -q $U/bigt2.git main 2>&1; echo rc=\$?"; t1=$(tm); python3 -c "print('TLS hop seconds', round($t1-$t0,1))"; srv git -C /repos/bigt2.git rev-parse main; log 1
