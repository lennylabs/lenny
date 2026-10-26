# Test-infrastructure MinIO image, built from the upstream MinIO server and
# MinIO client (mc) source at pinned release tags. The upstream images
# (minio/minio, minio/mc on Docker Hub, and quay.io/minio/*) no longer serve
# the releases the test fixtures pinned, so the Kind install, the compose
# stacks, and the testcontainers helper build this image instead of pulling.
#
# The image holds both binaries, as the upstream server image did: the
# server's compose healthcheck runs `mc ready local`, and the fixtures that
# create buckets run mc through `sh -c`. The entrypoint runs minio unless
# the first argument names a program on PATH, so `server /data` starts the
# server and `mc ...` or `sh -c ...` runs that program.
#
# MinIO and mc are licensed under the GNU AGPL v3 or later. The build
# clones the public upstream repositories at the pinned tags without
# modification,
# verifies each checkout against the pinned commit, copies each project's
# LICENSE and CREDITS into /licenses, and records the source repository and
# commit in the image labels, so the corresponding source is identified by
# the image itself.
#
# Build it with:
#
#	docker build -t lenny-minio tests/testinfra/images/minio
#
# To move to another release, change the RELEASE and COMMIT arguments
# together; the COMMIT is the tag's peeled commit
# (git ls-remote <repo> 'refs/tags/<tag>^{}').
ARG GO_IMAGE=golang:1.24.2-bookworm
ARG BASE_IMAGE=alpine:3.20.3

FROM ${GO_IMAGE} AS minio
ARG MINIO_RELEASE=RELEASE.2025-04-22T22-12-26Z
ARG MINIO_COMMIT=0d7408fc9969caf07de6a8c3a84f9fbb10a6739e
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
WORKDIR /src
RUN git clone --depth 1 --branch "${MINIO_RELEASE}" https://github.com/minio/minio.git . && \
    test "$(git rev-parse HEAD)" = "${MINIO_COMMIT}"
RUN go mod download
# The release build's ldflags, as the upstream Makefile computes them for a
# tagged release: gen-ldflags.go takes the release time in RFC 3339 form and
# reads the commit from git.
RUN version="$(echo "${MINIO_RELEASE#RELEASE.}" | sed -E 's/T([0-9]{2})-([0-9]{2})-([0-9]{2})Z$/T\1:\2:\3Z/')" && \
    ldflags="$(MINIO_RELEASE=RELEASE go run buildscripts/gen-ldflags.go "${version}")" && \
    go build -tags kqueue -trimpath -ldflags "${ldflags}" -o /out/minio . && \
    /out/minio --version

FROM ${GO_IMAGE} AS mc
ARG MC_RELEASE=RELEASE.2025-04-08T15-39-49Z
ARG MC_COMMIT=e929f89ceeedc48a45611382be9882db0bf1921d
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
WORKDIR /src
RUN git clone --depth 1 --branch "${MC_RELEASE}" https://github.com/minio/mc.git . && \
    test "$(git rev-parse HEAD)" = "${MC_COMMIT}"
RUN go mod download
RUN version="$(echo "${MC_RELEASE#RELEASE.}" | sed -E 's/T([0-9]{2})-([0-9]{2})-([0-9]{2})Z$/T\1:\2:\3Z/')" && \
    ldflags="$(MC_RELEASE=RELEASE go run buildscripts/gen-ldflags.go "${version}")" && \
    go build -tags kqueue -trimpath -ldflags "${ldflags}" -o /out/mc . && \
    /out/mc --version

FROM ${BASE_IMAGE}
ARG MINIO_RELEASE=RELEASE.2025-04-22T22-12-26Z
ARG MINIO_COMMIT=0d7408fc9969caf07de6a8c3a84f9fbb10a6739e
ARG MC_RELEASE=RELEASE.2025-04-08T15-39-49Z
ARG MC_COMMIT=e929f89ceeedc48a45611382be9882db0bf1921d
LABEL org.opencontainers.image.title="lenny-minio" \
      org.opencontainers.image.description="MinIO server and mc built from upstream source for the Lenny test infrastructure" \
      org.opencontainers.image.licenses="AGPL-3.0-or-later" \
      org.opencontainers.image.source="https://github.com/minio/minio" \
      org.opencontainers.image.revision="${MINIO_COMMIT}" \
      org.opencontainers.image.version="${MINIO_RELEASE}" \
      dev.lenny.minio.source="https://github.com/minio/minio" \
      dev.lenny.minio.release="${MINIO_RELEASE}" \
      dev.lenny.minio.commit="${MINIO_COMMIT}" \
      dev.lenny.mc.source="https://github.com/minio/mc" \
      dev.lenny.mc.release="${MC_RELEASE}" \
      dev.lenny.mc.commit="${MC_COMMIT}"
RUN apk add --no-cache ca-certificates
COPY --from=minio /out/minio /usr/bin/minio
COPY --from=mc /out/mc /usr/bin/mc
COPY --from=minio /src/LICENSE /licenses/minio/LICENSE
COPY --from=minio /src/CREDITS /licenses/minio/CREDITS
COPY --from=mc /src/LICENSE /licenses/mc/LICENSE
COPY --from=mc /src/CREDITS /licenses/mc/CREDITS
COPY entrypoint.sh /usr/bin/lenny-minio-entrypoint
# The upstream image's mc configuration directory, which is writable for any
# uid; the fixtures run the image as uid 1000 and as root.
ENV MC_CONFIG_DIR=/tmp/.mc
EXPOSE 9000
VOLUME ["/data"]
ENTRYPOINT ["/usr/bin/lenny-minio-entrypoint"]
CMD ["minio"]
