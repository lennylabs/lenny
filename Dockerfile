# Multi-stage build for the Lenny platform binaries. The BINARY
# build-arg selects which cmd/ package to compile — lenny-adapter,
# lenny-controller, lenny-gateway, lenny-webhook, or
# lenny-token-service:
#
#	docker build --build-arg BINARY=lenny-adapter -t lenny-adapter .
#
# The runtime stage is gcr.io/distroless/static:nonroot, a minimal
# non-root base with no shell and no package manager, consistent with
# the §13.1 pod-security posture (non-root, read-only root filesystem,
# all capabilities dropped — enforced by the pod securityContext).
ARG GO_VERSION=1.25

FROM golang:${GO_VERSION} AS build
ARG BINARY
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN test -n "$BINARY" || { echo 'the BINARY build-arg is required' >&2; exit 1; }
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/lenny ./cmd/"$BINARY"

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/lenny /usr/local/bin/lenny
ENTRYPOINT ["/usr/local/bin/lenny"]
