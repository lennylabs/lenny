#!/bin/bash
# tun.sh [tunnel flags] : run the tunnel client on the pub network.
cd "$(dirname "$0")"
docker run --rm --network sr3-pub -v $PWD/bin:/b:ro alpine:3.22 /b/tunnel "$@"
