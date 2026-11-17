#!/bin/sh
# SPDX-License-Identifier: MIT
#
# The journey agent container's entrypoint. When the scenario grants the
# gateway, it forwards 127.0.0.1:8080 inside the container to the gateway
# Service through the journey cluster's API server, so the agent reaches
# the gateway over the cluster network rather than a port on the host. It
# then runs the agent command it was given.
set -eu

if [ -n "${JOURNEY_GATEWAY_FORWARD:-}" ]; then
  log=/tmp/gateway-forward.log
  kubectl --kubeconfig /journey/harness-kubeconfig -n lenny-system \
    port-forward svc/lenny-gateway 8080:8080 >"$log" 2>&1 &
  i=0
  until grep -q 'Forwarding from' "$log" 2>/dev/null; do
    i=$((i + 1))
    if [ "$i" -gt 120 ]; then
      echo "journey-entrypoint: the gateway port-forward did not start" >&2
      cat "$log" >&2
      exit 70
    fi
    sleep 0.5
  done
fi

exec "$@"
