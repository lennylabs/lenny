#!/bin/bash
# alog.sh [fields...] : print r3-envoy JSON access log lines (waits for the 10 s file flush timer when W is set).
[ -n "$W" ] && sleep $W
docker logs r3-envoy 2>&1 | grep '^{' | python3 -c "
import json,sys
f=sys.argv[1:] or ['authority','code','flags','details','up_host','dur_ms']
for l in sys.stdin:
  d=json.loads(l); print(' '.join('%s' % d.get(k) for k in f))" "$@"
