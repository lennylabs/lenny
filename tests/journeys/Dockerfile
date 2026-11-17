# SPDX-License-Identifier: MIT
#
# The journey agent's container. `lenny-test --tier journeys` builds this
# image with a build context holding only this file, agent-entrypoint.sh,
# and the lenny-ctl and journey-egress-proxy binaries it cross-compiles from
# the tree, tags it with a digest of the four, and runs every container by
# the image's ID. The agent runs in it with no host home directory, no
# Docker socket, and no host kubeconfig, and its egress proxy runs from the
# same image (TESTING.md, "Persona journeys").
#
# The image holds the claude CLI, kubectl, helm, lenny-ctl, the egress
# proxy, a shell, coreutils (timeout), and the CA certificates. The claude
# CLI is the native binary of Claude Code's npm platform packages, which
# runs without Node.js.
#
# Pins:
#   Base image   debian:bookworm-slim, by the digest of its multi-arch index
#   Claude Code  2.1.326, @anthropic-ai/claude-code-linux-{x64,arm64};
#                the SHA-256 values are of the registry tarballs, whose
#                SHA-512 matches the registry's published integrity
#   kubectl      v1.31.4, the Kubernetes version of the Kind nodes
#                (tests/testinfra/kind/cluster.yaml)
#   Helm         v4.2.0, as tests/gate/Dockerfile pins it
#
# To move a pin, update the version and every checksum together.
FROM debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc AS fetch
ARG TARGETARCH
ARG CLAUDE_CODE_VERSION=2.1.326
ARG KUBECTL_VERSION=v1.31.4
ARG HELM_VERSION=v4.2.0
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl; \
    case "${TARGETARCH}" in \
      amd64) claude_pkg=linux-x64; claude_sha=113a6b58f9ee5a94b8bb841252070f60ff5f295b3d28c7f40b480a5b54f5579b; \
             kubectl_sha=298e19e9c6c17199011404278f0ff8168a7eca4217edad9097af577023a5620f; \
             helm_sha=97dbeb971be4ac4b27e3839976d9564c0fb35c6f3b1da89dd1e292d236af4096 ;; \
      arm64) claude_pkg=linux-arm64; claude_sha=04c707c086ee47fd5732c9c140df8d1dbf3167c4d5846b8a22f5acb7cfb9a584; \
             kubectl_sha=b97e93c20e3be4b8c8fa1235a41b4d77d4f2022ed3d899230dbbbbd43d26f872; \
             helm_sha=1f8de130dfbd04de64978e7b852a7a547be1404956a366608276d2520b678670 ;; \
      *) echo "unsupported architecture ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    mkdir -p /out; cd /tmp; \
    curl -fsSLo claude.tgz "https://registry.npmjs.org/@anthropic-ai/claude-code-${claude_pkg}/-/claude-code-${claude_pkg}-${CLAUDE_CODE_VERSION}.tgz"; \
    echo "${claude_sha}  claude.tgz" | sha256sum -c -; \
    tar -xzf claude.tgz; install -m 0755 package/claude /out/claude; \
    curl -fsSLo kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl"; \
    echo "${kubectl_sha}  kubectl" | sha256sum -c -; \
    install -m 0755 kubectl /out/kubectl; \
    curl -fsSLo helm.tgz "https://get.helm.sh/helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz"; \
    echo "${helm_sha}  helm.tgz" | sha256sum -c -; \
    tar -xzf helm.tgz; install -m 0755 "linux-${TARGETARCH}/helm" /out/helm

FROM debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates; \
    rm -rf /var/lib/apt/lists/*; \
    mkdir -p /workspace /journey /home/agent; \
    chmod 0777 /home/agent
COPY --from=fetch /out/claude /out/kubectl /out/helm /usr/local/bin/
COPY lenny-ctl journey-egress-proxy /usr/local/bin/
COPY agent-entrypoint.sh /usr/local/bin/journey-entrypoint
# Claude Code's documented settings for a restricted network: no update
# check or update path, no telemetry, no error reporting, and no other
# nonessential traffic, so the agent needs only the hosts the egress proxy
# allows. CLAUDE_CODE_SUBPROCESS_ENV_SCRUB strips credentials from the
# environment of the commands Claude Code runs; the harness does not rely
# on it, since the agent's shell is limited to kubectl, helm, and lenny-ctl.
ENV HOME=/home/agent \
    DISABLE_AUTOUPDATER=1 \
    DISABLE_UPDATES=1 \
    DISABLE_TELEMETRY=1 \
    DISABLE_ERROR_REPORTING=1 \
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
    CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/journey-entrypoint"]
