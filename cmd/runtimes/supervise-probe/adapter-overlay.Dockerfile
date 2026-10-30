# SPDX-License-Identifier: MIT
#
# Kind-only overlay of the e2e adapter image: it adds the supervise-probe
# binary at /usr/local/bin/lenny-setup-probe so a setup command, which runs
# in the adapter container, can leave the residue the supervised scrub
# removes there (a process detached from its process group and a System V
# shared-memory segment), and so a test can read that residue with
# `kubectl exec -c adapter` (§5.2 Supervised scrub sequence).
# tests/testinfra/kind/install.sh builds the adapter binary image as
# lenny-adapter-base:<tag> and builds this overlay over it as
# lenny-adapter:<tag>. The base is a separate image so that a rebuild
# overlays the binary image rather than the previous overlay. No released
# image carries it.
#
# Usage: docker build -f cmd/runtimes/supervise-probe/adapter-overlay.Dockerfile \
#          --build-arg BASE_IMAGE=lenny-adapter-base:e2e \
#          --build-arg PROBE_IMAGE=lenny-runtime-supervise-probe:e2e \
#          -t lenny-adapter:e2e cmd/runtimes/supervise-probe
ARG BASE_IMAGE
ARG PROBE_IMAGE

FROM ${PROBE_IMAGE} AS probe

FROM ${BASE_IMAGE}
COPY --from=probe /usr/local/bin/lenny /usr/local/bin/lenny-setup-probe
