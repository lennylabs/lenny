#!/bin/sh
# uploadpack.packObjectsHook: one progress line per second for 8 s before pack-objects.
for i in 1 2 3 4 5 6 7 8; do echo "slow phase $i" >&2; sleep 1; done; exec "$@"
