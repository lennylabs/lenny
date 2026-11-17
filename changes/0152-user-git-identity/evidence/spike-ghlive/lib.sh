# Shared helpers for the live GitHub spikes. Never print a token.
. /cred/github.env
API=https://api.github.com
ORG=lenny-test
b64() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
jwt() { now=$(date +%s); h=$(printf '{"alg":"RS256","typ":"JWT"}' | b64); p=$(printf '{"iat":%d,"exp":%d,"iss":"%s"}' $((now-60)) $((now+540)) "$GITHUB_APP_CLIENT_ID" | b64); printf '%s.%s.%s' "$h" "$p" "$(printf '%s.%s' "$h" "$p" | openssl dgst -sha256 -sign /cred/github-app.pem | b64)"; }
inst_token() { J=$(jwt); i=$(curl -s -H "Authorization: Bearer $J" -H 'Accept: application/vnd.github+json' $API/app/installations | jq -r ".[] | select(.account.login==\"$ORG\") | .id"); curl -s -X POST -H "Authorization: Bearer $J" -H 'Accept: application/vnd.github+json' $API/app/installations/$i/access_tokens | jq -r .token; }
utok() { jq -r .access_token /state/$1.tok; }
# refresh <user>: rotates the stored parent; prints a JSON summary only
refresh() {
  rt=$(jq -r .refresh_token /state/$1.tok)
  r=$(curl -s -X POST -H 'Accept: application/json' --data-urlencode "client_id=$GITHUB_APP_CLIENT_ID" --data-urlencode "client_secret=$GITHUB_APP_CLIENT_SECRET" --data-urlencode grant_type=refresh_token --data-urlencode "refresh_token=$rt" https://github.com/login/oauth/access_token)
  if echo "$r" | jq -e .access_token >/dev/null 2>&1; then cp /state/$1.tok /state/$1.tok.prev; echo "$r" > /state/$1.tok; echo "$r" | jq -c '{refreshed: true, expires_in, refresh_token_expires_in, new_refresh_token: (.refresh_token != null)}'; else echo "$r" | jq -c '{refreshed: false, error, error_description}'; fi
}
# child <parent-token> <repos-json-array> <perms-json>: prints JSON with the child under .token (caller must not print it)
child() { jq -n --arg at "$1" --argjson repos "$2" --argjson perms "$3" --arg t "$ORG" '{access_token: $at, target: $t, repositories: $repos, permissions: $perms}' | curl -s -X POST -u "$GITHUB_APP_CLIENT_ID:$GITHUB_APP_CLIENT_SECRET" -H 'Accept: application/vnd.github+json' -H 'X-GitHub-Api-Version: 2022-11-28' -d @- $API/applications/$GITHUB_APP_CLIENT_ID/token/scoped; }
# revoke_one <token>: deletes one app token
revoke_one() { jq -n --arg at "$1" '{access_token: $at}' | curl -s -o /dev/null -w '%{http_code}' -X DELETE -u "$GITHUB_APP_CLIENT_ID:$GITHUB_APP_CLIENT_SECRET" -H 'Accept: application/vnd.github+json' -d @- $API/applications/$GITHUB_APP_CLIENT_ID/token; }
# api <token> <METHOD> <path> [json]: prints "status<TAB>compact body"
api() { t=$1; m=$2; p=$3; d=${4:-}; if [ -n "$d" ]; then out=$(curl -s -w '\n%{http_code}' -X "$m" -H "Authorization: Bearer $t" -H 'Accept: application/vnd.github+json' -H 'X-GitHub-Api-Version: 2022-11-28' -d "$d" "$API$p"); else out=$(curl -s -w '\n%{http_code}' -X "$m" -H "Authorization: Bearer $t" -H 'Accept: application/vnd.github+json' -H 'X-GitHub-Api-Version: 2022-11-28' "$API$p"); fi; code=$(echo "$out" | tail -1); body=$(echo "$out" | sed '$d'); printf '%s\t%s\n' "$code" "$(echo "$body" | jq -c . 2>/dev/null | cut -c1-400)"; }
gql() { jq -n --arg q "$2" '{query: $q}' | curl -s -X POST -H "Authorization: Bearer $1" -H 'Accept: application/vnd.github+json' -d @- $API/graphql; }
valid() { curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $1" -H 'Accept: application/vnd.github+json' $API/user; }
# body <token> <METHOD> <path> [json]: prints the raw JSON body only, untruncated
body() { if [ -n "${4:-}" ]; then curl -s -X "$2" -H "Authorization: Bearer $1" -H 'Accept: application/vnd.github+json' -H 'X-GitHub-Api-Version: 2022-11-28' -d "$4" "$API$3"; else curl -s -X "$2" -H "Authorization: Bearer $1" -H 'Accept: application/vnd.github+json' -H 'X-GitHub-Api-Version: 2022-11-28' "$API$3"; fi; }
