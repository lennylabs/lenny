#!/bin/bash
# usage: gradle.sh <image> <label> <cmd: gradle|wrapper> [docker args...]
cd "$(dirname "$0")/.."
IMG=$1; L=$2; CMD=$3; shift 3
b=$(docker logs spk-proxy 2>&1 | wc -l)
if [ "$CMD" = wrapper ]; then RUN='./gradlew --no-daemon -q resolve'; else RUN='gradle --no-daemon -q resolve'; fi
docker run --rm --network spk-int --cpus 2 -m 2g -v $PWD/$PROJ:/src:ro -v $PWD/shared:/shared "$@" $IMG sh -c "cp -r /src /p && cd /p && echo GUH=\$GRADLE_USER_HOME && timeout 300 $RUN > /tmp/out 2>&1; rc=\$?; grep -E 'Picked up|RESOLVED|Downloading|UnknownHost|name resolution|Could not resolve|Exception' /tmp/out | sort -u | head -5 | cut -c1-200; exit \$rc"
rc=$?
echo "RESULT [$IMG] $CMD $L: exit=$rc $(docker logs spk-proxy 2>&1 | tail -n +$((b+1)) | grep -oE '(CONNECT|ABSOLUTE) [^ ]+' | sort | uniq -c | tr '\n' ' ')"
