#!/bin/bash
# build.sh [cmd...]: builds the spike's programs in a container, with the module cache in a named volume.
O=$(cd "$(dirname "$0")" && pwd)
docker volume create od-gomod >/dev/null; docker volume create od-gocache >/dev/null
docker rm -f od-build >/dev/null 2>&1
docker run --rm --name od-build -v od-gomod:/go/pkg/mod -v od-gocache:/root/.cache/go-build -v $O/go:/src -v $O/bin:/out -w /src -e GOFLAGS=-mod=mod -e CGO_ENABLED=0 golang:1.26 sh -c 'for c in '"${*:-\$(ls cmd)}"'; do go build -o /out/$c ./cmd/$c || exit 1; done'
