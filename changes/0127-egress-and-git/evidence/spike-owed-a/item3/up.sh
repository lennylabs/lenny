#!/bin/bash
O=$(cd "$(dirname "$0")/.." && pwd); S=$O/..; R=$O/item3/raw
docker run -d --name oa-gitea --network oa-priv --ip 172.30.50.22 --network-alias gitea.internal.oa.test --cpus 2 -m 1g -v $O/certs:/c:ro \
  -e USER_UID=1000 -e USER_GID=1000 -e GITEA__database__DB_TYPE=sqlite3 -e GITEA__security__INSTALL_LOCK=true -e GITEA__service__DISABLE_REGISTRATION=true \
  -e GITEA__server__PROTOCOL=https -e GITEA__server__HTTP_PORT=443 -e GITEA__server__DOMAIN=gitea.internal.oa.test -e GITEA__server__ROOT_URL=https://gitea.internal.oa.test/ \
  -e GITEA__server__CERT_FILE=/c/gitea/tls.crt -e GITEA__server__KEY_FILE=/c/gitea/tls.key -e GITEA__server__DISABLE_SSH=true -e GITEA__log__LEVEL=Warn \
  -e GITEA__repository__DEFAULT_BRANCH=main gitea/gitea:1.24 >/dev/null
docker run -d --name oa-cl2 --network oa-priv --ip 172.30.50.30 --cpus 1 -v $O/bin:/b:ro -v $O/certs:/c:ro oa-git sleep infinity >/dev/null
for i in $(seq 1 60); do docker exec oa-cl2 curl -s --cacert /c/ca.pem -o /dev/null https://gitea.internal.oa.test/api/healthz && break; /bin/sleep 1; done
docker exec oa-cl2 curl -sS --cacert /c/ca.pem https://gitea.internal.oa.test/api/v1/version; echo
ex(){ docker exec -u git oa-gitea "$@"; }
ex gitea admin user create --username alice --password 'alice-pw-1234' --email alice@acme.com --admin --must-change-password=false | tail -1
ex gitea admin user create --username bob --password 'bob-pw-12345' --email bob@acme.com --must-change-password=false | tail -1
T(){ ex gitea admin user generate-access-token --username $1 --token-name $2 --scopes $3 --raw; }
A=$(T alice full write:repository,write:user,read:user); AR=$(T alice ro read:repository); AU=$(T alice noscope read:user); B=$(T bob bobt read:repository,read:user)
printf 'A=%s\nAR=%s\nAU=%s\nB=%s\n' "$A" "$AR" "$AU" "$B" > $O/item3/tokens.env
api(){ docker exec oa-cl2 curl -sS --cacert /c/ca.pem -H "Authorization: token $A" -H 'Content-Type: application/json' "$@"; }
for r in priv pub; do p=true; [ $r = pub ] && p=false; api -X POST https://gitea.internal.oa.test/api/v1/user/repos -d "{\"name\":\"$r\",\"private\":$p,\"auto_init\":true,\"default_branch\":\"main\"}" | jq -c '{full_name,private}'; done
for r in priv pub; do docker exec oa-cl2 sh -c "cd /tmp && rm -fr w$r && GIT_SSL_CAINFO=/c/ca.pem git clone -q https://alice:$A@gitea.internal.oa.test/alice/$r.git w$r && cd w$r && git config user.name alice && git config user.email alice@acme.com && echo x >> f && git add f && git commit -qm c2 && git branch dev && git tag -a v1 -m v1 && GIT_SSL_CAINFO=/c/ca.pem git push -q origin main dev v1 && git log -1 --format=%H && git rev-parse v1 v1^{commit}"; done
