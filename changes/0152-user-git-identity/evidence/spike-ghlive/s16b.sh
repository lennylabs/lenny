#!/bin/sh
. /w/lib.sh
A=$(utok lennylabsdev); B=$(utok itsfivepastnine)
n=$(grep '^test-repo ' /state/s16.prs | cut -d' ' -f2)
echo "retarget PR #$n back to main: $(api $B PATCH /repos/lenny-test/test-repo/pulls/$n '{"base":"main"}' | cut -c1-4)"; sleep 5
for i in 1 2 3 4 5 6; do o=$(gql $A "{ repository(owner:\"lenny-test\", name:\"test-repo\") { pullRequest(number:$n) { reviewDecision mergeStateStatus baseRefName reviews(first:5){nodes{state author{login}}} } } }" | jq -c .data.repository.pullRequest); echo "$o" | grep -q UNKNOWN || break; sleep 3; done
echo "after retarget to main: $o"
