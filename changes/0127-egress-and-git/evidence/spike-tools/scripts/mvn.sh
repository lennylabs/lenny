#!/bin/bash
# usage: mvn.sh <image> <label> [docker -e args...]
cd "$(dirname "$0")/.."
IMG=$1; L=$2; shift 2
b=$(docker logs spk-proxy 2>&1 | wc -l)
docker run --rm --network spk-int --cpus 2 -m 1g -v $PWD/mvnproj:/src:ro -v $PWD/shared:/shared:ro "$@" $IMG sh -c 'cp -r /src /p && cd /p && mvn -B -ntp -Dmaven.repo.local=/tmp/repo dependency:resolve > /tmp/out 2>&1; rc=$?; grep -E "Picked up|Apache Maven|BUILD|commons-lang3:jar|ERROR\] (Failed|Plugin|Non-resolvable)|UnknownHost|Could not transfer" /tmp/out | head -3 | cut -c1-120; grep -oE "(UnknownHost|name resolution|Name or service)[^:]*" /tmp/out | sort -u | head -2; ls /tmp/repo/org/apache/commons/commons-lang3/3.17.0/*.jar 2>/dev/null; exit $rc'
rc=$?
n=$(docker logs spk-proxy 2>&1 | tail -n +$((b+1)) | grep -cE "CONNECT|ABSOLUTE")
echo "RESULT [$IMG] $L: exit=$rc proxy_requests=$n $(docker logs spk-proxy 2>&1 | tail -n +$((b+1)) | grep -oE '(CONNECT|ABSOLUTE) [^ ]+' | sort | uniq -c | tr '\n' ' ')"
