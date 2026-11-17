#!/bin/bash
# build.sh [cmd...]: builds inside a golang container with a gh- volume for the module cache, at a low CPU limit.
O=$(cd "$(dirname "$0")" && pwd)
docker volume create gh-gomod >/dev/null
docker run --rm --name gh-build --cpus 3 -v gh-gomod:/go -v $O/go:/src -v $O/bin:/out -w /src -e CGO_ENABLED=0 -e GOFLAGS=-mod=mod golang:1.26 sh -c "for c in ${*:-stub netagent attack}; do go build -o /out/\$c ./cmd/\$c || exit 1; done"
