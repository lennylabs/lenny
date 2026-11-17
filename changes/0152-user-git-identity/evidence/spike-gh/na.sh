#!/bin/bash
# na.sh [netagent flags]: restarts gh-na (and the client that shares its namespace). Upstream TLS is verified against the stub CA only.
O=$(cd "$(dirname "$0")" && pwd); cd "$O"
docker rm -f gh-cli >/dev/null 2>&1; docker rm -f gh-na >/dev/null 2>&1
rm -f run/stub/stub-ca.pem.tmp
docker run -d --name gh-na --cpus 1 --memory 256m --network gh-pod -v $O/bin:/b:ro -v $O/cfg-bindings.json:/cfg/bindings.json:ro -v $O/${POLICY:-policy-strict.json}:/cfg/policy.json:ro -v $O/run/stub/stub-ca.pem:/certs/pub-ca.pem:ro -v $O/run/na/ca:/var/netagent -v $O/run/na/anchor:/run/netagent alpine:3.22 /b/netagent -direct -policy /cfg/policy.json -reqlog /var/netagent/reqlog.jsonl "$@" >/dev/null
for i in $(seq 1 50); do docker exec gh-na wget -qO- http://127.0.0.1:9100/stats >/dev/null 2>&1 && break; /bin/sleep 0.2; done
cp run/na/anchor/ca.pem run/trust/pod-ca.pem
docker run -d --name gh-cli --network container:gh-na --cpus 1 --memory 512m -v $O/dl/gh/x/usr/bin/gh:/usr/local/bin/gh:ro -v $O/dl/glab/bin/glab:/usr/local/bin/glab:ro -v $O/run/trust:/s:ro -v $O/bin:/b:ro -v $O/work:/work:ro \
 -e HTTPS_PROXY=http://127.0.0.1:3128 -e https_proxy=http://127.0.0.1:3128 -e HTTP_PROXY=http://127.0.0.1:3128 -e NO_PROXY=gh-stub -e no_proxy=gh-stub -e SSL_CERT_FILE=/s/pod-ca.pem -e GIT_SSL_CAINFO=/s/pod-ca.pem -e GIT_TERMINAL_PROMPT=0 -e GH_NO_UPDATE_NOTIFIER=1 -e GH_PROMPT_DISABLED=1 -e GH_CONFIG_DIR=/tmp/gh -e GH_PAGER=cat -e PAGER=cat gh-client sleep infinity >/dev/null
docker exec gh-cli sh -c 'cp -r /work/repo /tmp/repo; cp -r /work/glrepo /tmp/glrepo; git config --global --add safe.directory "*"'
