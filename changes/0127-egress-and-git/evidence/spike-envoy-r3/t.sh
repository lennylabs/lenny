#!/bin/bash
# t.sh <user:token|-> <curl args...> : curl from r3-client through Envoy.
c=$1; shift
if [ "$c" = "-" ]; then a=(); else a=(--proxy-user "$c"); fi
docker exec r3-client curl -sS -o ${OUT:-/dev/stderr} -w '\n=> http=%{http_code} connect=%{http_connect} time=%{time_total}s size=%{size_download}\n' -x http://198.18.0.10:3128 --proxytunnel "${a[@]}" --max-time 20 "$@" 2>&1
