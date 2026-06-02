# Builds the lenny-coredns image: stock CoreDNS plus the coredns-ratelimit
# and coredns-filter plugins the dedicated lenny-system instance requires
# (spec §13.2 lines 498-536). The plugin set is defined by plugin.cfg in
# this directory.
ARG COREDNS_VERSION=v1.11.3

FROM golang:1.22-alpine AS build
ARG COREDNS_VERSION
RUN apk add --no-cache git make
WORKDIR /go/src/github.com/coredns
RUN git clone --depth 1 --branch "${COREDNS_VERSION}" https://github.com/coredns/coredns.git
WORKDIR /go/src/github.com/coredns/coredns
# Replace the stock plugin list with the lenny plugin chain, then run
# CoreDNS's generated plugin wiring + build. The two external plugins are
# resolved from the module proxy (mirror it for an air-gapped build).
COPY plugin.cfg ./plugin.cfg
RUN go get github.com/coredns/ratelimit \
 && go get github.com/milgradesec/filter \
 && go generate coredns.go \
 && CGO_ENABLED=0 go build -o /coredns .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /coredns /coredns
USER 65532:65532
EXPOSE 53 53/udp 9153 8080
ENTRYPOINT ["/coredns"]
