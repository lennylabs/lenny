#!/bin/bash
# What a v0-only server answers to a v2 ls-refs POST. fault front strips Git-Protocol before Gitea; gitsrv -v0only does it before stock git http-backend.
D=$(cd "$(dirname "$0")" && pwd); O=$D/..; . $D/tokens.env
body=$(python3 -c "
def p(s): return '%04x%s'%(len(s)+4,s)
print(p('command=ls-refs\n')+p('agent=x\n')+'0001'+p('peel\n')+p('ref-prefix refs/heads/main\n')+'0000',end='')")
docker exec oa-cl2 curl -sS http://172.30.50.23:9100/set?mode=v0only
echo "--- Gitea behind the stripping front"
docker exec oa-cl2 curl -sS -k -i -u alice:$AR -H 'Git-Protocol: version=2' -H 'Content-Type: application/x-git-upload-pack-request' -H 'Accept: application/x-git-upload-pack-result' --data-binary "$body" https://172.30.50.23/alice/priv.git/git-upload-pack | head -20 | od -c | head -24 | cut -c1-80
docker exec oa-cl2 curl -sS http://172.30.50.23:9100/set?mode=pass
echo "--- stock git http-backend, gitsrv -v0only"
docker rm -f oa-gitv0 >/dev/null 2>&1
docker run -d --name oa-gitv0 --network oa-priv --ip 172.30.50.24 --cpus 1 -m 256m -v $O/bin:/b:ro -v $O/certs:/c:ro -e GIT_CONFIG_COUNT=1 -e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0='*' oa-git sh -c 'git init -q --bare /repos/r.git; cd /tmp; git init -q -b main w; cd w; git -c user.name=a -c user.email=a@a commit -q --allow-empty -m a; git push -q /repos/r.git main; exec /b/gitsrv -v0only -cert /c/git/tls.crt -key /c/git/tls.key' >/dev/null
/bin/sleep 1.5
docker exec oa-cl2 curl -sS -k -i -H 'Git-Protocol: version=2' -H 'Content-Type: application/x-git-upload-pack-request' -H 'Accept: application/x-git-upload-pack-result' --data-binary "$body" https://172.30.50.24/r.git/git-upload-pack | od -c | head -16 | cut -c1-80
docker logs oa-gitv0 2>&1 | tail -3 | cut -c1-200
docker exec oa-cl2 /b/lsrefs -mode one -insecure -repo https://172.30.50.24/r.git -ref main
echo "--- after the client treats an empty 200 as a v2 refusal"
docker exec oa-cl2 /b/lsrefs -mode one -insecure -repo https://172.30.50.24/r.git -ref main
docker exec oa-cl2 /b/lsrefs -mode one -insecure -repo https://172.30.50.24/r.git -ref nosuch
docker exec oa-cl2 curl -sS http://172.30.50.23:9100/set?mode=v0only
docker exec oa-cl2 /b/lsrefs -mode one -insecure -auth "alice:$AR" -repo https://172.30.50.23/alice/priv.git -ref main
docker exec oa-cl2 /b/lsrefs -mode one -insecure -auth "alice:$AR" -repo https://172.30.50.23/alice/priv.git -ref v1
docker exec oa-cl2 /b/lsrefs -mode one -insecure -auth "alice:$AR" -repo https://172.30.50.23/alice/priv.git -ref nosuch
docker exec oa-cl2 /b/lsrefs -mode one -insecure -auth "alice:bad" -repo https://172.30.50.23/alice/priv.git -ref main
docker exec oa-cl2 /b/lsrefs -mode one -insecure -repo https://172.30.50.23/alice/priv.git -ref main
docker exec oa-cl2 curl -sS http://172.30.50.23:9100/set?mode=pass
docker rm -f oa-gitv0 >/dev/null
