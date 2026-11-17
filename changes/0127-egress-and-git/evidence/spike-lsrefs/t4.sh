#!/bin/bash
E=http://198.19.0.41:3128; EB=http://198.19.0.42:3128; G=https://git.spike
L="docker exec ls-client /b/lsrefs"
RUN="$L -mode run -insecure -repo $G/fast.git -rps 25 -timeout 10s"
ctl(){ docker exec ls-client wget -qO- "http://198.19.0.$1:9002/$2"; }
echo "### tunnel reuse across tenants (20 calls alternating acme and globex)"
for k in true false; do
  ctl 11 'tenants?reset=1' >/dev/null; ctl 12 'tenants?reset=1' >/dev/null; ctl 11 'tenants?reset=1' >/dev/null; ctl 12 'tenants?reset=1' >/dev/null
  $L -mode tenants -insecure -proxy $E -repo $G/fast.git -n 20 -keepalive=$k
  echo "  keepalive=$k decisions seen: authz1 $(ctl 11 tenants) authz2 $(ctl 12 tenants)"
done
echo "### one Envoy stopped during load, one retry, connect budget 1.5 s"
( /bin/sleep 5; docker stop -t0 ls-envoy-b >/dev/null ) &
$RUN -proxy $E,$EB -retries 1 -dial 1s -dur 12s; wait; docker start ls-envoy-b >/dev/null; /bin/sleep 3
echo "### one Envoy frozen (black hole), one retry, connect budget 1.5 s"
( /bin/sleep 5; docker pause ls-envoy-b >/dev/null ) &
$RUN -proxy $E,$EB -retries 1 -dial 1s -dur 12s; wait; docker unpause ls-envoy-b >/dev/null; /bin/sleep 2
echo "### graceful Envoy drain during load, one retry"
( /bin/sleep 5; curl -s -XPOST 'http://127.0.0.1:19942/drain_listeners?graceful' >/dev/null ) &
$RUN -proxy $E,$EB -retries 1 -dial 1s -dur 15s; wait; ./envoy.sh b 42 19942 >/dev/null
echo "### both Envoys stopped, one retry"
docker stop -t0 ls-envoy-a ls-envoy-b >/dev/null
echo "  $($L -mode one -insecure -proxy $E,$EB -retries 1 -dial 1s -repo $G/fast.git)"
docker start ls-envoy-a ls-envoy-b >/dev/null; /bin/sleep 3
echo "  after restart: $($L -mode one -insecure -proxy $E -repo $G/fast.git)"
echo "### both Envoys frozen, one retry"
docker pause ls-envoy-a ls-envoy-b >/dev/null
echo "  $($L -mode one -insecure -proxy $E,$EB -retries 1 -dial 1s -repo $G/fast.git)"
docker unpause ls-envoy-a ls-envoy-b >/dev/null; /bin/sleep 1
echo "### host outage is still classified as the host"
echo "  hang (10 s cap): $($L -mode one -insecure -proxy $E -repo $G/hang.git)"
