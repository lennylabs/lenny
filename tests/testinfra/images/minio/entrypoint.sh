#!/bin/sh
# Entrypoint of the test-infrastructure MinIO image. It runs the named
# program when the first argument is a program on PATH (minio, mc, sh), and
# otherwise runs minio with the arguments, so `server /data` starts the
# MinIO server as the upstream image's entrypoint did.
if [ "$#" -eq 0 ]; then
	set -- minio
elif ! command -v "$1" >/dev/null 2>&1; then
	set -- minio "$@"
fi
exec "$@"
