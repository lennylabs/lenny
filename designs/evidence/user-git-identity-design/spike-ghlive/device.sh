#!/bin/sh
# Device flow for one account. Tokens stay in /state (tmpfs). Prints no token.
set -u
want=$1
. /cred/github.env
resp=$(curl -s -X POST -H 'Accept: application/json' -d "client_id=$GITHUB_APP_CLIENT_ID" https://github.com/login/device/code)
dc=$(echo "$resp" | jq -r .device_code); uc=$(echo "$resp" | jq -r .user_code); iv=$(echo "$resp" | jq -r .interval); ex=$(echo "$resp" | jq -r .expires_in)
echo "$uc" > /state/$want.usercode
echo "waiting" > /state/$want.status
end=$(( $(date +%s) + ex ))
while [ $(date +%s) -lt $end ]; do
  sleep "$iv"
  r=$(curl -s -X POST -H 'Accept: application/json' -d "client_id=$GITHUB_APP_CLIENT_ID&device_code=$dc&grant_type=urn:ietf:params:oauth:grant-type:device_code" https://github.com/login/oauth/access_token)
  err=$(echo "$r" | jq -r '.error // empty')
  case "$err" in
    authorization_pending) continue;;
    slow_down) iv=$((iv+5)); continue;;
    "") echo "$r" > /state/$want.tok; chmod 600 /state/$want.tok
        at=$(echo "$r" | jq -r .access_token)
        login=$(curl -s -H "Authorization: Bearer $at" -H 'Accept: application/vnd.github+json' https://api.github.com/user | jq -r .login)
        mem=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $at" -H 'Accept: application/vnd.github+json' https://api.github.com/user/memberships/orgs/lenny-test)
        echo "$r" | jq -c --arg login "$login" --arg want "$want" --arg mem "$mem" '{authorized_as: $login, expected: $want, matches: ($login == $want), org_membership_status: $mem, has_refresh_token: (.refresh_token != null), expires_in, refresh_token_expires_in, token_type, scope}' > /state/$want.status
        exit 0;;
    *) echo "error: $err" > /state/$want.status; exit 0;;
  esac
done
echo "expired" > /state/$want.status
