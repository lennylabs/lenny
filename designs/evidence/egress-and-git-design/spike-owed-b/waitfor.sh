#!/bin/bash
# waitfor.sh TESTCOMMAND: returns when the command succeeds, or after 570 s
for i in $(seq 1 285); do if eval "$1"; then echo "condition met after $((i*2)) s"; exit 0; fi; sleep 2; done; echo "timed out"
