#!/bin/sh
# build.sh [cmd...], run inside the golang container.
cd /src; export CGO_ENABLED=0 GOFLAGS=-mod=mod
for c in ${@:-netagent origin egress goclient}; do go build -o /out/$c ./cmd/$c || exit 1; done
