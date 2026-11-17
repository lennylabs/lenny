# SPDX-License-Identifier: MIT
#
# The merge gate's toolchain. `lenny-test gate` builds this image when no
# image with the tag derived from this file exists, or when the tag names
# another image than the one the gate recorded when it built it, and runs
# the merge gate inside it with the worktree mounted at its host path.
# Editing any pin below changes the tag, so the next gate run builds a new
# image.
#
# The pins follow the ones the repository already uses:
#   Go             go.mod (go directive)
#   Python         .github/workflows (python-version 3.12)
#   Helm           .github/workflows/release.yml (setup-helm v4.2.0)
#   buf, gofumpt, goimports, protoc-gen-go, protoc-gen-go-grpc, and
#   golangci-lint  .github/workflows/pr.yml (go install pins)
#   controller-gen scripts/setup-dev.sh
#   envtest        tests/testinfra/envtest (Kubernetes 1.31.0)
#   Docker CLI     the client for the host daemon the gate's
#                  testcontainers run on (Docker 29)
#   jq             scripts/lib/versions.sh names 1.6 as the floor;
#                  scripts/check-schema-breaking.sh needs it
#   gitleaks       the static tier's secret scan (cmd/lenny-test,
#                  secretscan.go); the release current on 2026-11-17,
#                  which .gitleaks.toml names as its minVersion
#
# Every base image is pinned by the digest of its multi-arch index, which
# covers linux/amd64 and linux/arm64. To move a base image to a new tag,
# read the index digest with `docker buildx imagetools inspect <tag>` and
# update the tag and the digest together. Every downloaded binary is
# checked against the checksum its publisher releases.
#
# The Python base image is Debian bookworm with the buildpack toolchain,
# which supplies git, make, curl, and the C toolchain (gcc and binutils)
# that the cgo, race-detector, and runtime supervisor checks need, and
# useradd and setpriv, which the gate uses to run as the worktree's owner
# rather than as root. GOCACHE lies outside /root for that user.

FROM golang:1.25.0-bookworm@sha256:81dc45d05a7444ead8c92a389621fafabc8e40f8fd1a19d7e5df14e61e98bc1a AS go

FROM docker:29.4.3-cli@sha256:51e23845f5caff1e688a2fae003b0c69d635c9200ad544731db1593731df1d3a AS docker

FROM python:3.12.12-bookworm@sha256:c0abd0758831ad99b7a29e0c1a875da9c4abb9a2e3f21e2eeb585dbcadfb6cd0

ARG TARGETARCH
ARG HELM_VERSION=v4.2.0
ARG BUF_VERSION=v1.69.0
ARG ENVTEST_K8S_VERSION=1.31.0
ARG JQ_VERSION=1.8.1
ARG GITLEAKS_VERSION=8.30.1

COPY --from=go /usr/local/go /usr/local/go
# The Docker CLI of the official image is a static binary.
COPY --from=docker /usr/local/bin/docker /usr/local/bin/docker

ENV GOPATH=/go \
    GOCACHE=/var/cache/go-build \
    GOTOOLCHAIN=local \
    PATH=/go/bin:/usr/local/go/bin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    KUBEBUILDER_ASSETS=/usr/local/kubebuilder/bin

RUN git config --system --add safe.directory '*'

# Prebuilt binaries. Helm, buf, jq, and gitleaks are checked against the
# SHA-256 their releases publish (gitleaks in
# gitleaks_<version>_checksums.txt); envtest against the SHA-512 that
# controller-tools publishes in envtest-releases.yaml.
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) helm_sha=97dbeb971be4ac4b27e3839976d9564c0fb35c6f3b1da89dd1e292d236af4096; \
             buf_arch=x86_64; buf_sha=c1989e0f23143c044cc4f3e1d5c0d3453fa5f83f8330aed6bab76080192740b2; \
             jq_sha=020468de7539ce70ef1bceaf7cde2e8c4f2ca6c3afb84642aabc5c97d9fc2a0d; \
             gitleaks_arch=x64; gitleaks_sha=551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb; \
             envtest_sha512=5d96ae284610863ce5974e030aecd2eaad693f3210103ca778107aa0ea00f6f1d0a7b1b34aa74d7257cb0d7f713c2da365beba89b1d60823ce56c7b84b935423 ;; \
      arm64) helm_sha=1f8de130dfbd04de64978e7b852a7a547be1404956a366608276d2520b678670; \
             buf_arch=aarch64; buf_sha=8acd7161665ce25933a7cb2c982b3137bbcaa6e70ad58ea2cc311dba4f4f2f5c; \
             jq_sha=6bc62f25981328edd3cfcfe6fe51b073f2d7e7710d7ef7fcdac28d4e384fc3d4; \
             gitleaks_arch=arm64; gitleaks_sha=e4a487ee7ccd7d3a7f7ec08657610aa3606637dab924210b3aee62570fb4b080; \
             envtest_sha512=72f5c8fd615c9db62eeb66e30edfda0f3879bffa3577c5776ec83363c018d7f51c174ac5ea807414072a21f8151a7bdf9826f414543f690686550e49db202ca0 ;; \
      *) echo "unsupported architecture ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    tmp="$(mktemp -d)"; cd "${tmp}"; \
    curl -fsSLo helm.tgz "https://get.helm.sh/helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz"; \
    echo "${helm_sha}  helm.tgz" | sha256sum -c -; \
    tar -xzf helm.tgz; install -m 0755 "linux-${TARGETARCH}/helm" /usr/local/bin/helm; \
    curl -fsSLo buf "https://github.com/bufbuild/buf/releases/download/${BUF_VERSION}/buf-Linux-${buf_arch}"; \
    echo "${buf_sha}  buf" | sha256sum -c -; \
    install -m 0755 buf /usr/local/bin/buf; \
    curl -fsSLo jq "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/jq-linux-${TARGETARCH}"; \
    echo "${jq_sha}  jq" | sha256sum -c -; \
    install -m 0755 jq /usr/local/bin/jq; \
    curl -fsSLo gitleaks.tgz "https://github.com/gitleaks/gitleaks/releases/download/v${GITLEAKS_VERSION}/gitleaks_${GITLEAKS_VERSION}_linux_${gitleaks_arch}.tar.gz"; \
    echo "${gitleaks_sha}  gitleaks.tgz" | sha256sum -c -; \
    tar -xzf gitleaks.tgz gitleaks; install -m 0755 gitleaks /usr/local/bin/gitleaks; \
    curl -fsSLo envtest.tgz "https://github.com/kubernetes-sigs/controller-tools/releases/download/envtest-v${ENVTEST_K8S_VERSION}/envtest-v${ENVTEST_K8S_VERSION}-linux-${TARGETARCH}.tar.gz"; \
    echo "${envtest_sha512}  envtest.tgz" | sha512sum -c -; \
    tar -xzf envtest.tgz; mkdir -p "${KUBEBUILDER_ASSETS}"; \
    install -m 0755 controller-tools/envtest/* "${KUBEBUILDER_ASSETS}/"; \
    cd /; rm -rf "${tmp}"

# Go-built tools, at the versions the workflows and setup-dev.sh pin. The
# go command checks each module against the Go checksum database.
RUN set -eux; \
    go install mvdan.cc/gofumpt@v0.10.0; \
    go install golang.org/x/tools/cmd/goimports@v0.45.0; \
    go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11; \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2; \
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2; \
    go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.5; \
    go clean -cache -modcache

# The image's own tag, which `lenny-test gate` passes at build time.
# `lenny-test gate --here` reads it to confirm that it runs in the gate
# image. It is last, so a new tag rebuilds only this layer.
ARG LENNY_GATE_TAG
RUN set -eu; \
    test -n "${LENNY_GATE_TAG}"; \
    mkdir -p /etc/lenny-gate; \
    printf '%s\n' "${LENNY_GATE_TAG}" > /etc/lenny-gate/image

WORKDIR /src
