#!/bin/bash
# build.sh [cmd...]: cross-builds the spike's programs into bin/. Uses the local module cache only.
O=$(cd "$(dirname "$0")" && pwd)
cd "$O/go"
export GOCACHE=$O/.gocache GOFLAGS=-mod=mod GOPROXY=off GOTOOLCHAIN=local GOOS=linux GOARCH=arm64 CGO_ENABLED=0
mkdir -p "$O/bin"
for c in ${@:-$(ls cmd)}; do go build -o "$O/bin/$c" ./cmd/$c || exit 1; done
