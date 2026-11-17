#!/bin/bash
# t3b.sh TAG : host_ttl expiry after a rebinding to a private address.
cd "$(dirname "$0")"; TAG=${1:-v1.40.2}
dset() { docker exec r3-dns wget -qO- "localhost:8053/set?name=$1&a=$2&aaaa=$3&ttl=$4" >/dev/null; echo "   [$(date +%T)] dnsd: $1 A=$2 ttl=$4"; }
get() { r=$(./t.sh agent:good -s "$1" | grep -E "hello|=>" | tr '\n' ' '); echo "   [$(date +%T)] GET $1 -> $r"; }
python3 gen.py envoy/g-rebind2.json dns=dnsd family=V4_ONLY minrefresh=1s refresh=2s hostttl=5s && ./envoy.sh $TAG envoy/g-rebind2.json >/dev/null
dset rebind.spike 198.18.0.20 "" 1; get http://rebind.spike/
dset rebind.spike 10.99.0.5 "" 1
for s in 2 4 6 8 10 12; do sleep 2; get http://rebind.spike/; done
curl -s localhost:19911/stats | grep -E "egress_dns\.(dns_address_filter_out|host_removed|num_hosts|host_added)"
echo "## idle for 12 s (longer than host_ttl 5s), then CONNECT"
sleep 12; curl -s localhost:19911/stats | grep -E "egress_dns\.(host_removed|num_hosts)"; get http://rebind.spike/
W=11 ./alog.sh authority code flags details up_host | tail -3
