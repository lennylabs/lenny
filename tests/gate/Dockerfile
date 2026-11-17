# SPDX-License-Identifier: MIT
#
# The merge gate's toolchain. `lenny-test gate` builds this image when no
# image with the tag derived from this file exists, and runs the merge
# gate inside it with the worktree mounted at its host path. Editing any
# pin below changes the tag, so the next gate run builds a new image.
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
#
# The Python base image is Debian bookworm with the buildpack toolchain,
# which supplies git, make, and the C toolchain (gcc and binutils) that
# the cgo, race-detector, and runtime supervisor checks need.

ARG GO_VERSION=1.25.0
ARG PYTHON_VERSION=3.12.12

FROM golang:${GO_VERSION}-bookworm AS go

FROM python:${PYTHON_VERSION}-bookworm

ARG TARGETARCH
ARG HELM_VERSION=v4.2.0
ARG BUF_VERSION=v1.69.0
ARG DOCKER_CLI_VERSION=29.4.3
ARG ENVTEST_K8S_VERSION=1.31.0

COPY --from=go /usr/local/go /usr/local/go

ENV GOPATH=/go \
    GOCACHE=/root/.cache/go-build \
    GOTOOLCHAIN=local \
    PATH=/go/bin:/usr/local/go/bin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    KUBEBUILDER_ASSETS=/usr/local/kubebuilder/bin

RUN apt-get update \
 && apt-get install -y --no-install-recommends jq \
 && rm -rf /var/lib/apt/lists/* \
 && git config --system --add safe.directory '*'

# Prebuilt binaries, each checked against the published SHA-256.
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) helm_sha=97dbeb971be4ac4b27e3839976d9564c0fb35c6f3b1da89dd1e292d236af4096; \
             buf_arch=x86_64; buf_sha=c1989e0f23143c044cc4f3e1d5c0d3453fa5f83f8330aed6bab76080192740b2; \
             docker_arch=x86_64; docker_sha=bc9734a89d3edd15eeca8620961f6499ba69948814c85d7ac3488e34b3e16d01; \
             envtest_sha=b72c0c764c797e6b2cfd6d417abdad7b25d4fbc9f8475edeb44c8dd598999b76 ;; \
      arm64) helm_sha=1f8de130dfbd04de64978e7b852a7a547be1404956a366608276d2520b678670; \
             buf_arch=aarch64; buf_sha=8acd7161665ce25933a7cb2c982b3137bbcaa6e70ad58ea2cc311dba4f4f2f5c; \
             docker_arch=aarch64; docker_sha=fdde3dbedbd8dc9e9e54a3172e682b4e69e740296e580542ea9ceacebbbf4f4c; \
             envtest_sha=087123cfb6ac48a1002db19df7ee96949b54d34860805a41397bcb4cd0b5d5e4 ;; \
      *) echo "unsupported architecture ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    tmp="$(mktemp -d)"; cd "${tmp}"; \
    curl -fsSLo helm.tgz "https://get.helm.sh/helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz"; \
    echo "${helm_sha}  helm.tgz" | sha256sum -c -; \
    tar -xzf helm.tgz; install -m 0755 "linux-${TARGETARCH}/helm" /usr/local/bin/helm; \
    curl -fsSLo buf "https://github.com/bufbuild/buf/releases/download/${BUF_VERSION}/buf-Linux-${buf_arch}"; \
    echo "${buf_sha}  buf" | sha256sum -c -; \
    install -m 0755 buf /usr/local/bin/buf; \
    curl -fsSLo docker.tgz "https://download.docker.com/linux/static/stable/${docker_arch}/docker-${DOCKER_CLI_VERSION}.tgz"; \
    echo "${docker_sha}  docker.tgz" | sha256sum -c -; \
    tar -xzf docker.tgz; install -m 0755 docker/docker /usr/local/bin/docker; \
    curl -fsSLo envtest.tgz "https://github.com/kubernetes-sigs/controller-tools/releases/download/envtest-v${ENVTEST_K8S_VERSION}/envtest-v${ENVTEST_K8S_VERSION}-linux-${TARGETARCH}.tar.gz"; \
    echo "${envtest_sha}  envtest.tgz" | sha256sum -c -; \
    tar -xzf envtest.tgz; mkdir -p "${KUBEBUILDER_ASSETS}"; \
    install -m 0755 controller-tools/envtest/* "${KUBEBUILDER_ASSETS}/"; \
    cd /; rm -rf "${tmp}"

# Go-built tools, at the versions the workflows and setup-dev.sh pin.
RUN set -eux; \
    go install mvdan.cc/gofumpt@v0.10.0; \
    go install golang.org/x/tools/cmd/goimports@v0.45.0; \
    go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11; \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2; \
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2; \
    go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.5; \
    go clean -cache -modcache

WORKDIR /src
