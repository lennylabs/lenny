#!/bin/sh
. /w/lib.sh
P=$(utok lennylabsdev)
r=$(child "$P" '["test-repo"]' '{"contents":"read"}')
echo "response keys: $(echo "$r" | jq -c 'keys')"
echo "response without token: $(echo "$r" | jq -c 'del(.token) | {expires_at, user: .user.login, installation: (.installation | if . then {repository_selection, permissions, repos: [.repositories[]?.full_name]} else null end), app: .app.slug, hashed: (.hashed_token != null), token_last_eight: null}')"
C=$(echo "$r" | jq -r .token)
SHA=$(api $C GET /repos/lenny-test/test-repo/git/ref/heads/main | cut -f2 | jq -r .object.sha)
echo "write a ref on test-repo (contents:read child, expect refused): $(api $C POST /repos/lenny-test/test-repo/git/refs "{\"ref\":\"refs/heads/spike/scope-read\",\"sha\":\"$SHA\"}" | cut -c1-160)"
SHA2=$(api $C GET /repos/lenny-test/test-repo-2/git/ref/heads/main | cut -f2 | jq -r .object.sha)
r2=$(child "$P" '["test-repo"]' '{"contents":"write"}'); W2=$(echo "$r2" | jq -r .token)
echo "contents:write child on test-repo, write ref on test-repo (expect allowed): $(api $W2 POST /repos/lenny-test/test-repo/git/refs "{\"ref\":\"refs/heads/spike/scope-ok\",\"sha\":\"$SHA\"}" | cut -c1-100)"
echo "same child, write ref on test-repo-2 (expect refused): $(api $W2 POST /repos/lenny-test/test-repo-2/git/refs "{\"ref\":\"refs/heads/spike/scope-other\",\"sha\":\"$SHA2\"}" | cut -c1-160)"
echo "parent (not a child), write ref on test-repo-2 (control, expect allowed): $(api $P POST /repos/lenny-test/test-repo-2/git/refs "{\"ref\":\"refs/heads/spike/scope-parent\",\"sha\":\"$SHA2\"}" | cut -c1-100)"
echo "cleanup: $(api $W2 DELETE /repos/lenny-test/test-repo/git/refs/heads/spike/scope-ok | cut -c1-3) $(api $P DELETE /repos/lenny-test/test-repo-2/git/refs/heads/spike/scope-parent | cut -c1-3)"
revoke_one $C >/dev/null; revoke_one $W2 >/dev/null
