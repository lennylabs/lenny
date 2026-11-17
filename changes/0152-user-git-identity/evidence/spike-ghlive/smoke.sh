#!/bin/sh
# Read-only smoke test of the GitHub App setup. Prints no secret or token.
set -u
apk add -q curl openssl jq >/dev/null 2>&1 || { echo "apk failed"; exit 1; }
. /cred/github.env
API=https://api.github.com
H='-H Accept:application/vnd.github+json -H X-GitHub-Api-Version:2022-11-28'
b64() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
now=$(date +%s)
hdr=$(printf '{"alg":"RS256","typ":"JWT"}' | b64)
pl=$(printf '{"iat":%d,"exp":%d,"iss":"%s"}' $((now-60)) $((now+540)) "$GITHUB_APP_CLIENT_ID" | b64)
sig=$(printf '%s.%s' "$hdr" "$pl" | openssl dgst -sha256 -sign /cred/github-app.pem | b64)
JWT="$hdr.$pl.$sig"
echo "== 1. App identity (JWT signed with the private key)"
curl -s $H -H "Authorization: Bearer $JWT" $API/app > /tmp/app.json
jq '{id, slug, name, owner: .owner.login, permissions, events}' /tmp/app.json
[ "$(jq -r .id /tmp/app.json)" = "$GITHUB_APP_ID" ] && echo "OK: App ID matches github.env" || echo "MISMATCH: App ID"
echo "== 2. Installations"
curl -s $H -H "Authorization: Bearer $JWT" $API/app/installations > /tmp/inst.json
jq '[.[] | {id, account: .account.login, repository_selection, permissions}]' /tmp/inst.json
IID=$(jq -r '.[] | select(.account.login=="lenny-test") | .id' /tmp/inst.json)
[ -n "$IID" ] && echo "OK: installed on lenny-test" || { echo "FAIL: not installed on lenny-test"; exit 0; }
echo "== 3. Installation token (value not printed)"
TOK=$(curl -s -X POST $H -H "Authorization: Bearer $JWT" $API/app/installations/$IID/access_tokens | jq -r .token)
[ -n "$TOK" ] && [ "$TOK" != null ] && echo "OK: installation token issued" || echo "FAIL: no installation token"
echo "== 4. Repositories the installation covers"
curl -s $H -H "Authorization: Bearer $TOK" "$API/installation/repositories?per_page=100" | jq '{total_count, repos: [.repositories[] | {full_name, private}]}'
echo "== 5. Can the App create a repository in the org? (needs Administration; checked without creating: permission listing above)"
echo "== 6. Test accounts exist"
for u in lennylabsdev itsfivepastnine; do curl -s $H -H "Authorization: Bearer $TOK" $API/users/$u | jq -c '{login, id, type, created_at}'; done
echo "== 7. Public org membership (private membership reads 404 without Members permission)"
for u in lennylabsdev itsfivepastnine; do code=$(curl -s -o /dev/null -w '%{http_code}' $H $API/orgs/lenny-test/public_members/$u); echo "$u public_member: $code"; done
echo "== 8. Client secret valid (checking a dummy token: 404 = credentials accepted, 401 = rejected)"
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST $H -u "$GITHUB_APP_CLIENT_ID:$GITHUB_APP_CLIENT_SECRET" -d '{"access_token":"ghu_invalidsmoketesttoken0000000000000000"}' $API/applications/$GITHUB_APP_CLIENT_ID/token)
echo "check-token status: $code"
echo "== 9. Device flow enabled (requests a code and discards it)"
curl -s -X POST -H 'Accept: application/json' -d "client_id=$GITHUB_APP_CLIENT_ID" https://github.com/login/device/code | jq '{has_device_code: (.device_code != null), verification_uri, expires_in, error, error_description}'
