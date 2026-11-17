#!/bin/sh
# Test 4b: the progress scenario with the direct upstream, through the forwarding proxy (packet-trace timestamps).
pkill -f bin/upstream; pkill -f bin/proxy; sleep 0.3
GIT_CONFIG_GLOBAL=/tmp/srv-progress.gc HOME=/tmp /spike/t3/bin/upstream -mode direct 2>/dev/null &
/spike/t3/bin/proxy -routes 'github.com=http://localhost:8080' -allow 'github.com/org/small' 2>/dev/null &
sleep 0.5; rm -rf /tmp/c4
HOME=/tmp/h4 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TRACE_PACKET=/tmp/pt.proxy git -c protocol.version=2 -c url.http://127.0.0.1:7073/https/.insteadOf=https:// \
  clone -q --progress https://github.com/org/small /tmp/c4 2>/dev/null
grep -E 'sideband< \\2slow|sideband< \\2Enumerating' /tmp/pt.proxy | awk '{print $1, $NF, $(NF-1)}'
pkill -f bin/upstream; pkill -f bin/proxy
