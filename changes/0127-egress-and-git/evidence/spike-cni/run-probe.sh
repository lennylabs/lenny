#!/bin/sh
# Usage: run-probe.sh <label>   -> writes out/probe-<label>.txt and prints it
set -u
W=$(cd "$(dirname "$0")" && pwd)
K="kubectl --context kind-spike-cni"
WORKER_IP=$($K get node spike-cni-worker -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')
CP_IP=$($K get node spike-cni-control-plane -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')
OTHER_POD_IP=$($K -n other get pod other-echo -o jsonpath='{.status.podIP}')
GW_POD_IP=$($K -n lenny-system get pod -l app=lenny-gateway -o jsonpath='{.items[0].status.podIP}')
EGRESS_POD_IP=$($K -n lenny-system get pod -l app=lenny-egress -o jsonpath='{.items[0].status.podIP}')
OUT="$W/out/probe-$1.txt"
{
  echo "# $(date -u +%Y-%m-%dT%H:%M:%SZ) label=$1"
  echo "# netpols in lenny-agents: $($K -n lenny-agents get netpol -o name | tr '\n' ' ')"
  $K -n lenny-agents exec -i client -- env WORKER_IP="$WORKER_IP" CP_IP="$CP_IP" OTHER_POD_IP="$OTHER_POD_IP" GW_POD_IP="$GW_POD_IP" EGRESS_POD_IP="$EGRESS_POD_IP" sh -s < "$W/probe.sh"
} | tee "$OUT"
