#!/bin/sh
# usage: tt.sh PROXY TARGET TOKEN MAX
D=spike-proxy-alts/common
docker run --rm --network spike-px-net -v $D:/c:ro spike-squid7:local python3 /c/tunnel_timer.py "$@"
