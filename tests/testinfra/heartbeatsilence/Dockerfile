# Test-only fixture image for the tier-5 stream-failure cases. It layers
# the heartbeat-silence fixture on top of a reference runtime image, whose
# binary stays at /usr/local/bin/lenny unchanged, and makes the fixture the
# entrypoint with that binary as its child:
#
#	docker build -f tests/testinfra/heartbeatsilence/Dockerfile \
#	  --build-arg BASE_IMAGE=lenny-runtime-echo:e2e \
#	  -t lenny-runtime-heartbeat-silence-echo:e2e .
#
# tests/testinfra/kind/install.sh builds one image per reference runtime it
# wraps and installs them only for the heartbeat-silence pools.
ARG GO_VERSION=1.25
ARG BASE_IMAGE

FROM golang:${GO_VERSION} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/heartbeat-silence \
    ./tests/testinfra/heartbeatsilence/cmd/heartbeat-silence

FROM ${BASE_IMAGE}
COPY --from=build /out/heartbeat-silence /usr/local/bin/heartbeat-silence
ENTRYPOINT ["/usr/local/bin/heartbeat-silence", "/usr/local/bin/lenny"]
