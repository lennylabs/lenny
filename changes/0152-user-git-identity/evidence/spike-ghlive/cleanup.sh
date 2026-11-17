#!/bin/sh
. /w/lib.sh
I=$(inst_token)
for repo in test-repo test-repo-2; do
  for n in $(body $I GET "/repos/lenny-test/$repo/pulls?state=open&per_page=100" | jq -r '.[] | select(.head.ref | startswith("spike/")) | .number'); do
    echo "$repo close PR #$n: $(api $I PATCH /repos/lenny-test/$repo/pulls/$n '{"state":"closed"}' | cut -c1-3)"; done
  for ref in $(body $I GET "/repos/lenny-test/$repo/git/matching-refs/heads/spike/" | jq -r '.[].ref'); do
    echo "$repo delete $ref: $(api $I DELETE /repos/lenny-test/$repo/git/$ref | cut -c1-3)"; done
done
echo "remaining spike refs: test-repo $(body $I GET /repos/lenny-test/test-repo/git/matching-refs/heads/spike/ | jq length), test-repo-2 $(body $I GET /repos/lenny-test/test-repo-2/git/matching-refs/heads/spike/ | jq length)"
echo "open spike PRs: $(for r in test-repo test-repo-2; do body $I GET "/repos/lenny-test/$r/pulls?state=open" | jq '[.[] | select(.head.ref | startswith("spike/"))] | length'; done | tr '\n' ' ')"
