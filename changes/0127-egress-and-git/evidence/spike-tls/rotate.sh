#!/bin/bash
# rotate.sh NAME: atomic symlink swap of /certs/live, as the kubelet does for a Secret mount.
cd "$(dirname "$0")"
docker run --rm -v stls-certs:/certs -v $PWD/certs:/src:ro alpine:3.22 sh -c '
n=$1; mkdir -p /certs/live; ts=..$(date +%s%N); mkdir /certs/live/$ts; cp /src/$n/tls.crt /src/$n/tls.key /certs/live/$ts/; cp /src/ca1.pem /src/ca2.pem /certs/; chmod -R a+rX /certs
ln -s $ts /certs/live/..data_tmp; mv -T /certs/live/..data_tmp /certs/live/..data
[ -e /certs/live/tls.crt ] || { ln -s ..data/tls.crt /certs/live/tls.crt; ln -s ..data/tls.key /certs/live/tls.key; }' -- "$1"
