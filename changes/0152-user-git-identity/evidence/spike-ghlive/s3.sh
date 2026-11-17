#!/bin/sh
. /w/lib.sh
A=$(utok lennylabsdev); B=$(utok itsfivepastnine); I=$(inst_token)
R=lenny-test/test-repo
AE="353446916+lennylabsdev@users.noreply.github.com"; BE="353446532+itsfivepastnine@users.noreply.github.com"
work=/tmp/s3; rm -rf $work; git clone -q https://github.com/$R.git $work 2>/dev/null; cd $work
gpush() { # token branch author-name author-email label
  f=$(echo "$5" | tr -c "a-z0-9-" "-"); echo "$5 $(date +%s%N)" > "spike-$f.txt"; git add -A
  GIT_AUTHOR_NAME=$3 GIT_AUTHOR_EMAIL=$4 GIT_COMMITTER_NAME=$3 GIT_COMMITTER_EMAIL=$4 git commit -q -m "spike $5"
  git push -q "https://x-access-token:$1@github.com/$R.git" HEAD:refs/heads/$2 2>&1 | grep -v '^remote:' | head -2; echo "push($5): $?"; }
state() { # pr
  for i in 1 2 3 4 5 6 7 8 9 10; do
    o=$(gql $A "{ repository(owner:\"lenny-test\", name:\"test-repo\") { pullRequest(number:$1) { reviewDecision mergeStateStatus mergeable } } }" | jq -c .data.repository.pullRequest)
    echo "$o" | grep -q UNKNOWN || break; sleep 3; done; echo "$o"; }
case_run() { # label pusher-token pusher-name pusher-email approver-token
  br=spike/s3-$1-$(date +%s); git checkout -q -B base origin/main
  gpush "$B" $br itsfivepastnine "$BE" "bob-opens-$1"
  n=$(body $B POST /repos/$R/pulls "{\"title\":\"spike s3 $1\",\"head\":\"$br\",\"base\":\"main\"}" | jq -r .number)
  if [ "$2" != none ]; then gpush "$2" $br "$3" "$4" "push-$1"; fi
  sleep 3
  ap=$(api $5 POST /repos/$R/pulls/$n/reviews '{"event":"APPROVE","body":"spike"}' | cut -c1-110)
  sleep 4
  last=$(body $A GET /repos/$R/pulls/$n/commits | jq -c '[.[-1] | {author: .author.login, committer: .committer.login}]')
  echo "CASE $1: PR #$n approve=[$ap] last-commit=$last state=$(state $n)"
  echo $n >> /state/s3.prs; echo $br >> /state/s3.branches; }
CA=$(child "$A" '["test-repo"]' '{"contents":"write","pull_requests":"write"}' | jq -r .token)
echo "== control: bob opens, alice approves, no other push"; case_run control none x x "$A"
echo "== A: bob opens, App installation token pushes (alice's agent, author alice), alice approves"; case_run app "$I" lennylabsdev "$AE" "$A"
echo "== B: bob opens, alice's user token pushes, alice approves"; case_run user "$A" lennylabsdev "$AE" "$A"
echo "== C: bob opens, alice's scoped child pushes, alice approves"; case_run child "$CA" lennylabsdev "$AE" "$A"
echo "== D: bob opens, App pushes, bob (the author) approves"; case_run author "$I" itsfivepastnine "$BE" "$B"
revoke_one $CA >/dev/null
