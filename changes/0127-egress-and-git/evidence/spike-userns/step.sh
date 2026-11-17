#!/bin/bash
cd "$(dirname "$0")"
K="kubectl --context kind-lenny-userns"
echo "--- first pod's message"; $K -n mir get pod userns -o jsonpath='{.status.containerStatuses[0].state.terminated.message}' 2>/dev/null | cut -c1-700; echo
$K -n mir get events --field-selector involvedObject.name=userns -o jsonpath='{range .items[*]}{.reason}: {.message}{"\n"}{end}' | grep -i fail | cut -c1-700
