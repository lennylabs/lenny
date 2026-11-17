#!/bin/sh
# uploadpack.packObjectsHook: 8 s of silence before pack-objects (keepalive scenario).
sleep 8; exec "$@"
