#!/bin/bash
# usage: t.sh NAME [docker run args...] IMAGE CMD...
W=spike-federation
name=$1; shift
n0=$(wc -l < $W/logs/server.jsonl)
out=$W/logs/$name.out
{
echo "### TEST $name  $(date +%T)"
echo "### docker run $*"
docker run --rm --name spikefed-c-$name -v $W/run:/var/run/netagent -v $W/certs:/certs:ro \
  -v $W/py/scripts:/app:ro -v $W/node/scripts:/napp:ro -v $W/go/cmd:/src/cmd:ro "$@" 2>&1
echo "### exit $?"
echo "### server log:"
tail -n +$((n0+1)) $W/logs/server.jsonl
} > $out 2>&1
cat $out
