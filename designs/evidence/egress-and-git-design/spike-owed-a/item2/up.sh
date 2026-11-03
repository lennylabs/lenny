#!/bin/bash
# Brings up the decision stand-in, a private git origin with no route from the client, and the two clients.
O=$(cd "$(dirname "$0")/.." && pwd); S=$O/..
docker volume create oa-repos >/dev/null
docker run -d --name oa-authz --network oa-net --ip 198.19.50.11 --cpus 0.5 -m 128m -e NAME=authz1 -e PIN=172.30.50.21:443 -v $S/spike-lsrefs/bin:/b:ro alpine:3.22 /b/authz >/dev/null
docker run -d --name oa-git --network oa-priv --ip 172.30.50.21 --cpus 2 -m 2g -v oa-repos:/repos -v $O/bin:/b:ro -v $O/certs:/c:ro -e GIT_CONFIG_COUNT=2 -e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0='*' -e GIT_CONFIG_KEY_1=http.receivepack -e GIT_CONFIG_VALUE_1=true oa-git /b/gitsrv -cert /c/git/tls.crt -key /c/git/tls.key >/dev/null
docker run -d --name oa-gitc --network oa-net --ip 198.19.50.30 --cpus 2 -m 3g -v $O/certs:/c:ro oa-git sleep infinity >/dev/null
docker run -d --name oa-gitd --network oa-priv --ip 172.30.50.30 --add-host git.internal.oa.test:172.30.50.21 --cpus 2 -m 3g -v $O/certs:/c:ro oa-git sleep infinity >/dev/null
