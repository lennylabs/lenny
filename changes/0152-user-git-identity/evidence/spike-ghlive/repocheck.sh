#!/bin/sh
set -u
apk add -q curl openssl jq >/dev/null 2>&1
. /cred/github.env
API=https://api.github.com; H='-H Accept:application/vnd.github+json -H X-GitHub-Api-Version:2022-11-28'
b64() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
now=$(date +%s); hdr=$(printf '{"alg":"RS256","typ":"JWT"}' | b64)
pl=$(printf '{"iat":%d,"exp":%d,"iss":"%s"}' $((now-60)) $((now+540)) "$GITHUB_APP_CLIENT_ID" | b64)
JWT="$hdr.$pl.$(printf '%s.%s' "$hdr" "$pl" | openssl dgst -sha256 -sign /cred/github-app.pem | b64)"
IID=$(curl -s $H -H "Authorization: Bearer $JWT" $API/app/installations | jq -r '.[] | select(.account.login=="lenny-test") | .id')
TOK=$(curl -s -X POST $H -H "Authorization: Bearer $JWT" $API/app/installations/$IID/access_tokens | jq -r .token)
for r in test-repo test-repo-2; do
  echo "== $r"
  curl -s $H -H "Authorization: Bearer $TOK" $API/repos/lenny-test/$r | jq -c '{private, default_branch, size}'
  db=$(curl -s $H -H "Authorization: Bearer $TOK" $API/repos/lenny-test/$r | jq -r .default_branch)
  curl -s $H -H "Authorization: Bearer $TOK" $API/repos/lenny-test/$r/commits/$db | jq -c '{has_commit: (.sha != null), message: (.message // null)}'
  curl -s $H -H "Authorization: Bearer $TOK" "$API/repos/lenny-test/$r/rules/branches/$db" | jq -c '[.[]? | {type, parameters}]'
done
