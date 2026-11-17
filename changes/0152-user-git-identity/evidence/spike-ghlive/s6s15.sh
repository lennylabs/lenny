#!/bin/sh
. /w/lib.sh
A=$(utok lennylabsdev); B=$(utok itsfivepastnine); R=lenny-test/test-repo; R2=lenny-test/test-repo-2
mkbranch() { # token repo branch file -> creates branch from main with one file
  s=$(body $1 GET /repos/$2/git/ref/heads/main | jq -r .object.sha)
  api $1 POST /repos/$2/git/refs "{\"ref\":\"refs/heads/$3\",\"sha\":\"$s\"}" >/dev/null
  c=$(printf 'spike %s %s\n' "$3" "$(date +%s)" | openssl base64 -A)
  api $1 PUT /repos/$2/contents/$4 "{\"message\":\"spike $3\",\"content\":\"$c\",\"branch\":\"$3\"}" | cut -c1-3; }
mkpr() { body $1 POST /repos/$2/pulls "{\"title\":\"spike $3\",\"head\":\"$3\",\"base\":\"main\",\"body\":\"spike\"}" | jq -r '.number // .message'; }

echo "== S6"
echo "parent repo permissions (user token, App holds contents:write): $(body $A GET /repos/$R | jq -c .permissions)"
M=$(child "$A" '["test-repo"]' '{"metadata":"read"}' | jq -r .token)
echo "metadata-only child repo permissions: $(body $M GET /repos/$R | jq -c .permissions)"
echo "collaborator permission endpoint, parent: $(api $A GET /repos/$R/collaborators/lennylabsdev/permission | cut -f1) $(body $A GET /repos/$R/collaborators/lennylabsdev/permission | jq -c '{permission, role_name}')"
echo "collaborator permission endpoint, metadata-only child: $(api $M GET /repos/$R/collaborators/lennylabsdev/permission | cut -f1)"
echo "GET /user/emails with parent (App has emails:read): $(api $A GET /user/emails | cut -f1) verified-primary-present: $(body $A GET /user/emails | jq -c '[.[]? | select(.primary) | .verified]')"
CR=$(child "$A" '["test-repo"]' '{"contents":"read"}' | jq -r .token)
echo "GET /user/emails with contents-only child: $(api $CR GET /user/emails | cut -c1-120)"
CE=$(child "$A" '["test-repo"]' '{"emails":"read"}' | jq -r .token)
echo "GET /user/emails with emails:read child: $(api $CE GET /user/emails | cut -f1)"
echo "child with empty repositories list: $(child "$A" '[]' '{"contents":"read"}' | jq -c '{issued: (.token != null), selection: .installation.repository_selection, message, errors}')"

echo "== S15 setup: alice opens a PR on each repo"
echo "branch on test-repo: $(mkbranch $A $R spike/s15c spike-s15c.txt)"; N1=$(mkpr $A $R spike/s15c); echo "PR test-repo #$N1"
echo "branch on test-repo-2: $(mkbranch $A $R2 spike/s15d spike-s15d.txt)"; N2=$(mkpr $A $R2 spike/s15d); echo "PR test-repo-2 #$N2"
BC=$(child "$B" '["test-repo"]' '{"pull_requests":"write","contents":"read"}' | jq -r .token)
BC2=$(child "$B" '["test-repo-2"]' '{"pull_requests":"write","contents":"read"}' | jq -r .token)
echo "== S15 approve: bob's child (pull_requests:write, contents:read) approves alice's PR on test-repo"
echo "REST approve: $(api $BC POST /repos/$R/pulls/$N1/reviews '{"event":"APPROVE","body":"spike approve"}' | cut -c1-140)"
echo "== S15 merge with pull_requests:write alone (test-repo-2, no rules)"
NODE2=$(body $A GET /repos/$R2/pulls/$N2 | jq -r .node_id)
echo "REST merge by bob child: $(api $BC2 PUT /repos/$R2/pulls/$N2/merge '{"merge_method":"merge"}' | cut -c1-160)"
echo "GraphQL merge by bob child: $(gql $BC2 "mutation { mergePullRequest(input:{pullRequestId:\"$NODE2\"}) { pullRequest { merged } } }" | jq -c '{data, errors: [.errors[]?.message]}' | cut -c1-200)"
echo "== S15 node-ID mutation outside the child's repository"
echo "bob child scoped to test-repo comments on test-repo-2 PR by node id: $(gql $BC "mutation { addComment(input:{subjectId:\"$NODE2\", body:\"spike node-id\"}) { commentEdge { node { id } } } }" | jq -c '{data, errors: [.errors[]?.message]}' | cut -c1-220)"
echo "bob child scoped to test-repo creates PR on test-repo-2 via REST: $(api $BC POST /repos/$R2/pulls '{"title":"x","head":"spike/s15d","base":"main"}' | cut -c1-120)"
echo "== S15 request forms (parent token, harmless targets)"
echo "GraphQL batch (array body): $(printf '[{"query":"{viewer{login}}"},{"query":"{viewer{login}}"}]' | curl -s -w ' %{http_code}' -X POST -H "Authorization: Bearer $A" -d @- $API/graphql | cut -c1-160)"
echo "GraphQL over GET: $(curl -s -w ' %{http_code}' -G -H "Authorization: Bearer $A" --data-urlencode 'query={viewer{login}}' $API/graphql | cut -c1-160)"
echo "REST form body (issue comment on PR $N2): $(curl -s -w ' %{http_code}' -X POST -H "Authorization: Bearer $A" -H 'Content-Type: application/x-www-form-urlencoded' --data 'body=spike-form' $API/repos/$R2/issues/$N2/comments | jq -c '{id, message}' 2>/dev/null | cut -c1-120)"
s=$(body $A GET /repos/$R2/git/ref/heads/main | jq -r .object.sha); api $A POST /repos/$R2/git/refs "{\"ref\":\"refs/heads/spike/mo\",\"sha\":\"$s\"}" >/dev/null
echo "method override header (POST with X-HTTP-Method-Override: DELETE on a ref): $(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $A" -H 'X-HTTP-Method-Override: DELETE' -d '{}' $API/repos/$R2/git/refs/heads/spike/mo2); ref still exists: $(api $A GET /repos/$R2/git/ref/heads/spike/mo | cut -c1-3)"
echo "method override _method query: $(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $A" -d '{}' "$API/repos/$R2/git/refs/heads/spike/mo2?_method=DELETE"); ref still exists: $(api $A GET /repos/$R2/git/ref/heads/spike/mo | cut -c1-3)"
api $A DELETE /repos/$R2/git/refs/heads/spike/mo2 >/dev/null
echo "== S15 PR body limit (PATCH on PR $N1)"
for n in 65536 65537 262144; do b=$(head -c $n /dev/zero | tr '\0' 'a'); r=$(printf '{"body":"%s"}' "$b" | curl -s -w '\n%{http_code}' -X PATCH -H "Authorization: Bearer $A" -H 'Accept: application/vnd.github+json' -d @- $API/repos/$R/pulls/$N1); echo "body $n chars: $(echo "$r" | tail -1) $(echo "$r" | sed '$d' | jq -c '{len: (.body|length), message, errors: [.errors[]?.message]}' 2>/dev/null | cut -c1-140)"; done
echo "== S15 performed_via_github_app on a comment made with a child"
cid=$(body $BC POST /repos/$R/issues/$N1/comments '{"body":"spike via child"}' | jq -r .id)
echo "comment via child: $(body $A GET /repos/$R/issues/comments/$cid | jq -c '{user: .user.login, performed_via_github_app: (.performed_via_github_app.slug // null)}')"
echo "review via child: $(body $A GET /repos/$R/pulls/$N1/reviews | jq -c '[.[] | {user: .user.login, state}]')"
printf '%s %s\n' "$N1" "$N2" > /state/s15.prs
for t in $M $CR $CE $BC $BC2; do revoke_one $t >/dev/null; done
