#!/bin/bash
# lifetime.sh <envoy-tag> : run each timeout variant against an active (upstream
# ticks every 1s), an idle (silent), and a client-active (client writes every 1s,
# upstream silent) CONNECT tunnel; the client abandons each tunnel at 20s.
cd "$(dirname "$0")"
TAG=${1:-v1.40.2}
L=logs/lifetime-$TAG; mkdir -p $L
variants=(
  "none|"
  "route.max_stream_duration=5s|route=max_stream_duration: {max_stream_duration: 5s}"
  "hcm.common.max_stream_duration=5s|hcm=common_http_protocol_options: {max_stream_duration: 5s}"
  "hcm.stream_idle_timeout=5s|hcm=stream_idle_timeout: 5s"
  "route.idle_timeout=5s|route=idle_timeout: 5s"
  "route.timeout=5s|route=timeout: 5s"
  "hcm.common.max_connection_duration=5s|hcm=common_http_protocol_options: {max_connection_duration: 5s}"
  "hcm.common.max_connection_duration=5s+drain_timeout=2s|hcm=drain_timeout: 2s
          common_http_protocol_options: {max_connection_duration: 5s}"
  "hcm.common.idle_timeout=5s|hcm=common_http_protocol_options: {idle_timeout: 5s}"
)
printf "%-58s | %-28s | %-28s | %-28s\n" variant active-upstream idle client-active
for v in "${variants[@]}"; do
  name=${v%%|*}; arg=${v#*|}
  python3 envoy/render.py envoy/lt.yaml ${arg:+"$arg"}
  ./envoy.sh $TAG envoy/lt.yaml >/dev/null || { echo "$name: envoy failed"; docker logs r3-envoy 2>&1 | tail -5; continue; }
  ./tun.sh -target target.spike:7000 -max 20s > $L/$name.active 2>&1 &
  ./tun.sh -target target.spike:7001 -max 20s > $L/$name.idle 2>&1 &
  ./tun.sh -target target.spike:7001 -max 20s -send 1 > $L/$name.client 2>&1 &
  wait
  sleep 1; docker logs r3-envoy 2>&1 | grep '^{' > $L/$name.access
  f() { tail -1 $1 | sed -E 's/tunnel ended after ([0-9.]+)s: (.*)/\1s \2/' | sed -E 's/read tcp [^ ]+: //; s/ \(last=.*//' | cut -c1-28; }
  printf "%-58s | %-28s | %-28s | %-28s\n" "$name" "$(f $L/$name.active)" "$(f $L/$name.idle)" "$(f $L/$name.client)"
done
