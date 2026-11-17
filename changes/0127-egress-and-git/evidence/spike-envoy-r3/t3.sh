#!/bin/bash
# t3.sh TAG [hostttl] : live DNS rebinding against the DFP cache with resolved_address_filter.
cd "$(dirname "$0")"; TAG=${1:-v1.40.2}; HTTL=${2:-300s}
dset() { docker exec r3-dns wget -qO- "localhost:8053/set?name=$1&a=$2&aaaa=$3&ttl=$4" >/dev/null; echo "   [$(date +%T)] dnsd: $1 A=$2 AAAA=$3 ttl=$4"; }
get() { r=$(./t.sh agent:good -s "$1" | grep -E "hello|=>" | tr '\n' ' '); echo "   [$(date +%T)] GET $1 -> $r"; }
st() { curl -s localhost:19911/stats | grep -E "egress_dns\.(dns_address_filter_out|dns_query_attempt|dns_query_success|host_address_changed|host_removed|num_hosts)" | tr '\n' ' '; echo; }
python3 gen.py envoy/g-rebind.json dns=dnsd family=ALL minrefresh=1s refresh=${REFRESH:-60s} hostttl=$HTTL && ./envoy.sh $TAG envoy/g-rebind.json >/dev/null
docker restart r3-dns >/dev/null; sleep 0.5
echo "## phase 1: rebind.spike -> public 198.18.0.20 (TTL 1)"
dset rebind.spike 198.18.0.20 "" 1
get http://rebind.spike/
./tun.sh -target rebind.spike:7000 -max 25s > logs/t3-tunnel-$TAG.txt 2>&1 &
sleep 1
echo "## phase 2: rebind.spike -> 2001:db8:1::20 (public v6, TTL 1): proves refresh is applied"
dset rebind.spike "" 2001:db8:1::20 1; sleep 3
get http://rebind.spike/
echo "## phase 3: rebind.spike -> 10.99.0.5 (private, reachable from Envoy) TTL 1"
dset rebind.spike 10.99.0.5 "" 1; sleep 3
get http://rebind.spike/; st
echo "## phase 4: rebind.spike -> fd00:99::5 (ULA, reachable) TTL 0"
dset rebind.spike "" fd00:99::5 0; sleep 3
get http://rebind.spike/; st
echo "## phase 5: mixed answer A=10.99.0.5,198.18.0.20"
dset rebind.spike 10.99.0.5,198.18.0.20 "" 1; sleep 3
for i in 1 2 3; do get http://rebind.spike/; done; st
echo "## phase 6: back to private only, then fresh name never resolved before"
dset rebind.spike 10.99.0.5 "" 1; dset fresh.spike 10.99.0.5 "" 1; sleep 3
get http://rebind.spike/; get http://fresh.spike/; st
echo "## dnsd queries for rebind.spike (refresh cadence)"
docker exec r3-dns wget -qO- localhost:8053/log | grep "rebind" | awk '{print $1,$2,$4,$5}' | tr '\n' ';' | fold -w 200; echo
wait
echo "## long tunnel opened in phase 1 (target :7000 ticks once per second)"; cat logs/t3-tunnel-$TAG.txt
W=11 ./alog.sh authority code flags details up_host dur_ms
