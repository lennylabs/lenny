#!/bin/bash
# failure modes. One retry on a new connection unless stated.
E=http://198.19.0.41:3128; EB=http://198.19.0.42:3128; G=https://git.spike
L="docker exec ls-client /b/lsrefs"
RUN="$L -mode run -insecure -repo $G/fast.git -rps 25 -timeout 10s"
echo "### baseline 10 s"; $RUN -proxy $E,$EB -retries 1 -dial 1s -dur 10s
echo "### all decision endpoints stopped: every call fails fast and closed"
docker stop -t0 ls-authz1 ls-authz2 >/dev/null; /bin/sleep 3
echo "  one call no retry: $($L -mode one -insecure -proxy $E -repo $G/fast.git)"
echo "  one call one retry: $($L -mode one -insecure -proxy $E,$EB -retries 1 -repo $G/fast.git)"
docker start ls-authz1 ls-authz2 >/dev/null; /bin/sleep 4
echo "  after restart: $($L -mode one -insecure -proxy $E -repo $G/fast.git)"
echo "### one decision endpoint killed during load"
( /bin/sleep 5; docker exec ls-authz1 pkill -9 authz ) & 
$RUN -proxy $E,$EB -retries 1 -dial 1s -dur 15s; wait
echo "### one Envoy stopped during load, no retry"
( /bin/sleep 5; docker stop -t0 ls-envoy-b >/dev/null ) &
$RUN -proxy $E,$EB -retries 0 -dial 1s -dur 12s; wait; docker start ls-envoy-b >/dev/null; /bin/sleep 3
echo "### one Envoy stopped during load, one retry"
( /bin/sleep 5; docker stop -t0 ls-envoy-b >/dev/null ) &
$RUN -proxy $E,$EB -retries 1 -dial 1s -dur 12s; wait; docker start ls-envoy-b >/dev/null; /bin/sleep 3
echo "### one Envoy frozen (black hole), one retry, dial 1 s"
( /bin/sleep 5; docker pause ls-envoy-b >/dev/null ) &
$RUN -proxy $E,$EB -retries 1 -dial 1s -timeout 3s -dur 12s; wait; docker unpause ls-envoy-b >/dev/null
echo "### both Envoys stopped: egress path down"
docker stop -t0 ls-envoy-a ls-envoy-b >/dev/null
echo "  one call one retry: $($L -mode one -insecure -proxy $E,$EB -retries 1 -dial 1s -repo $G/fast.git)"
docker start ls-envoy-a ls-envoy-b >/dev/null; /bin/sleep 3
echo "  after restart: $($L -mode one -insecure -proxy $E -repo $G/fast.git)"
