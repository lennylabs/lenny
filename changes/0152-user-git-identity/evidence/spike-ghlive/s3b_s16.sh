#!/bin/sh
. /w/lib.sh
A=$(utok lennylabsdev); B=$(utok itsfivepastnine); I=$(inst_token)
BE="353446532+itsfivepastnine@users.noreply.github.com"
work=/tmp/s3b; rm -rf $work; git clone -q https://github.com/lenny-test/test-repo.git $work; cd $work
commitpush() { # token repo branch name email label
  f=$(echo "$6" | tr -c "a-z0-9-" "-"); echo "$6 $(date +%s%N)" > "spike-$f.txt"; git add -A
  GIT_AUTHOR_NAME=$4 GIT_AUTHOR_EMAIL=$5 GIT_COMMITTER_NAME=$4 GIT_COMMITTER_EMAIL=$5 git commit -q -m "spike $6"
  git push -q "https://x-access-token:$1@github.com/$2.git" HEAD:refs/heads/$3 2>&1 | grep -v '^remote:' | head -2; }
state() { for i in 1 2 3 4 5 6 7 8 9 10; do o=$(gql $A "{ repository(owner:\"lenny-test\", name:\"$1\") { pullRequest(number:$2) { reviewDecision mergeStateStatus baseRefName } } }" | jq -c .data.repository.pullRequest); echo "$o" | grep -q UNKNOWN || break; sleep 3; done; echo "$o"; }
echo "== S3 variant: App push with an author that is no GitHub account, alice approves"
br=spike/s3-unattr-$(date +%s); git checkout -q -B base origin/main
commitpush "$B" lenny-test/test-repo $br itsfivepastnine "$BE" bob-opens-unattr
n=$(body $B POST /repos/lenny-test/test-repo/pulls "{\"title\":\"spike s3 unattributed\",\"head\":\"$br\",\"base\":\"main\"}" | jq -r .number)
commitpush "$I" lenny-test/test-repo $br "agent" "agent@example.invalid" app-unattr
sleep 3; echo "approve: $(api $A POST /repos/lenny-test/test-repo/pulls/$n/reviews '{"event":"APPROVE"}' | cut -c1-4)"; sleep 4
echo "CASE app-unattributed: PR #$n state=$(state test-repo $n)"; echo $n >> /state/s3.prs; echo $br >> /state/s3.branches
echo "== S16: approval after a base change"
for repo in test-repo test-repo-2; do
  cd /tmp; rm -rf w16; git clone -q https://github.com/lenny-test/$repo.git w16; cd w16; git checkout -q -B base origin/main
  b2=spike/s16-base-$(date +%s); git push -q "https://x-access-token:$A@github.com/lenny-test/$repo.git" HEAD:refs/heads/$b2 2>/dev/null
  hb=spike/s16-head-$(date +%s); commitpush "$B" lenny-test/$repo $hb itsfivepastnine "$BE" s16-$repo
  n=$(body $B POST /repos/lenny-test/$repo/pulls "{\"title\":\"spike s16\",\"head\":\"$hb\",\"base\":\"main\"}" | jq -r .number)
  api $A POST /repos/lenny-test/$repo/pulls/$n/reviews '{"event":"APPROVE"}' >/dev/null; sleep 3
  before=$(body $A GET /repos/lenny-test/$repo/pulls/$n/reviews | jq -c '[.[] | .state]')
  pb=$(api $B PATCH /repos/lenny-test/$repo/pulls/$n "{\"base\":\"$b2\"}" | cut -c1-4); sleep 4
  after=$(body $A GET /repos/lenny-test/$repo/pulls/$n/reviews | jq -c '[.[] | .state]')
  echo "$repo PR #$n: reviews before=$before, base change status=$pb, reviews after=$after, state=$(state $repo $n)"
  echo "$repo $n" >> /state/s16.prs; echo "$repo $b2" >> /state/s16.branches; echo "$repo $hb" >> /state/s16.branches
done
echo "== PR body limit, larger"
n=$(cut -d' ' -f1 /state/s15.prs)
for sz in 524288 1048576 4194304; do b=$(head -c $sz /dev/zero | tr '\0' 'a'); r=$(printf '{"body":"%s"}' "$b" | curl -s -w '\n%{http_code}' -X PATCH -H "Authorization: Bearer $A" -H 'Accept: application/vnd.github+json' -d @- $API/repos/lenny-test/test-repo/pulls/$n); echo "body $sz chars: $(echo "$r" | tail -1) $(echo "$r" | sed '$d' | jq -c '{len: (.body|length), message, errors: [.errors[]? | (.message // .)]}' 2>/dev/null | cut -c1-160)"; done
