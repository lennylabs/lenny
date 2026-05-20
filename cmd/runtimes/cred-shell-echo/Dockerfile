# SPDX-License-Identifier: MIT
#
# §12.9.8 tier-9 credential-leakage probe runtime image. Pairs the
# Basic-level echo loop (pkg/runtimekit/echocore) with an Alpine
# base that retains /bin/sh, so the probes can run
# `kubectl exec ... cat /proc/<pid>/environ` and
# `kubectl exec ... ls /run/lenny/` against a running agent pod.
#
# This image is TEST-ONLY. The §13.1 pod-security posture forbids a
# shell in production agent pods; the lenny-pod-security admission
# webhook rejects this image in production installations. The
# tier-9 overlay deploys it explicitly for the leakage probes.
#
# Usage: docker build -f cmd/runtimes/cred-shell-echo/Dockerfile -t lennylabs/cred-shell-echo:dev .

ARG GO_VERSION=1.25

FROM golang:${GO_VERSION} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' \
    -o /out/cred-shell-echo ./cmd/runtimes/cred-shell-echo

# Alpine base: a shell, busybox utilities, and the /proc filesystem
# the tier-9 probes read. Stays single-stage thin for image size
# (~10 MB compared to ~5 MB for distroless).
FROM alpine:3.20
RUN addgroup -g 65532 -S nonroot && \
    adduser  -u 65532 -S -G nonroot nonroot
COPY --from=build /out/cred-shell-echo /usr/local/bin/cred-shell-echo
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/cred-shell-echo"]
