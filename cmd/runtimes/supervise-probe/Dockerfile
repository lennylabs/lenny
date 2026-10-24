# SPDX-License-Identifier: MIT
#
# Test-only probe runtime image for the runtime supervisor's cluster tiers
# (tests/testinfra/superviseprobe). It differs from the generic platform
# Dockerfile in two ways the tests read:
#
#   - ENV PROBE_IMAGE_ENV, which no probe Runtime's `env` sets, so a
#     test can assert that the supervisor's launch environment drops the
#     image ENV (§4.7.10 Runtime supervisor, Environment).
#   - STOPSIGNAL SIGINT, so a test can assert that pod deletion delivers no
#     SIGINT to the runtime process (§4.7.10 Runtime supervisor, Signals).
#
# It also carries the supervisor's tier-1 C harness, statically linked, at
# /usr/local/bin/lenny-supervisor-harness, so tier 6 can run the harness's
# synchronous-fault case under each sandboxed runtime (§4.7.10 Runtime
# supervisor, Signals: the kernel's row for a synchronous fault).
#
# Usage: docker build -f cmd/runtimes/supervise-probe/Dockerfile -t lenny-runtime-supervise-probe .
ARG GO_VERSION=1.25

FROM golang:${GO_VERSION} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/lenny ./cmd/runtimes/supervise-probe

FROM golang:${GO_VERSION} AS harness
WORKDIR /src
COPY supervisor/ ./supervisor/
RUN make -C supervisor OUT=/out HARNESS_LDFLAGS=-static /out/harness

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/lenny /usr/local/bin/lenny
COPY --from=harness /out/harness /usr/local/bin/lenny-supervisor-harness
ENV PROBE_IMAGE_ENV=from-image
STOPSIGNAL SIGINT
ENTRYPOINT ["/usr/local/bin/lenny"]
