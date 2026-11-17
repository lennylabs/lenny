#!/bin/bash
# a-srv.sh [ocup flags]: restarts the fake upstream.
O=$(cd "$(dirname "$0")" && pwd); cd "$O"
docker rm -f oc-up-srv >/dev/null 2>&1
docker run -d --name oc-up-srv --network oc-up --ip 198.19.0.20 --ulimit nofile=200000:200000 -v $O/bin:/b:ro -v $O/certs:/certs:ro \
  --network-alias api.example.test --network-alias ghes.example.test --network-alias gitlab.example.test --network-alias registry.example.test --network-alias pypi.example.test --network-alias other.example.test --network-alias llm.example.test --network-alias pinned.example.test --network-alias attacker.example \
  -e ACME_SECRET=ACME-REAL-9f2c71 -e GH_SECRET=GH-REAL-55aa01 -e GITLAB_SECRET=GL-REAL-66dd04 -e NPM_SECRET=NPM-REAL-77bb02 -e PYPI_USER=pypiuser -e PYPI_SECRET=PYPI-REAL-33cc03 alpine:3.22 /b/ocup "$@" >/dev/null
/bin/sleep 0.5
