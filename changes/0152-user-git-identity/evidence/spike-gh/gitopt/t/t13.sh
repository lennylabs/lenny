#!/bin/sh
# t13.sh <tag>: the GitLab preset: glab commands, raw routes in each spelling, and the text rule.
. /t/env.sh; . /t/lib.sh
TAG=$1; export GIT_CONFIG_GLOBAL=/t/cfg/Tb.gitconfig GITLAB_HOST=gitlab.example.test GITLAB_TOKEN=lenny-injected; TL=1; STEPT=30; W=$(mktemp -d); cd $W
git clone -q https://gitlab.example.test/acme/widgets.git c; cd c; git checkout -q -b feature-gl-$TAG-$$; echo x > n; git add n; git commit -qm n; git push -q -u origin HEAD
H=https://gitlab.example.test; P=$H/api/v4/projects; A=acme%2Fwidgets; M=$P/$A/merge_requests
C="curl -sS -m 12 -o /tmp/b -w http=%{http_code} --cacert /s/bundle.pem -x 127.0.0.1:3128 -H 'Private-Token: lenny-injected'"; J="-H 'Content-Type: application/json'"
step $TAG-mr-create "glab mr create --title t --description d --yes 2>&1 | tail -1"
step $TAG-mr-view "glab mr view 7 2>&1 | head -2 | tail -1"
step $TAG-mr-list "glab mr list 2>&1 | tail -1"
step $TAG-D-mr-approve "glab mr approve 7 2>&1 | tail -1 | cut -c1-120"
step $TAG-mr-revoke-D "glab mr revoke 7 2>&1 | tail -1 | cut -c1-120"
step $TAG-D-mr-merge "glab mr merge 7 --yes 2>&1 | tail -1 | cut -c1-120"
step $TAG-D-mr-rebase "glab mr rebase 7 2>&1 | tail -1 | cut -c1-120"
step $TAG-mr-note "glab mr note 7 -m hello 2>&1 | tail -1 | cut -c1-120"
step $TAG-mr-update "glab mr update 7 --title n 2>&1 | tail -1 | cut -c1-120"
step $TAG-mr-close "glab mr close 7 2>&1 | tail -1 | cut -c1-120"
step $TAG-issue-create "glab issue create --title t --description d --yes 2>&1 | tail -1 | cut -c1-120"
for r in "POST 7/approve" "POST 7/unapprove" "PUT 7/merge" "PUT 7/rebase" "PUT 7/reset_approvals" "POST 7/cancel_merge_when_pipeline_succeeds"; do m=${r% *}; t=${r#* }; n=$(echo $t | tr / -)
 step $TAG-D-raw-$n "$C -X $m '$M/$t' 2>&1; cut -c1-90 /tmp/b"
 step $TAG-D-raw-$n-numeric "$C -X $m '$P/1/merge_requests/$t' 2>&1; cut -c1-90 /tmp/b"
 step $TAG-D-raw-$n-fullencoded "$C -X $m '$P/$A%2Fmerge_requests%2F$(echo $t | sed s#/#%2F#)' 2>&1; cut -c1-90 /tmp/b"
 step $TAG-D-raw-$n-case "$C -X $m '$P/$A/MERGE_REQUESTS/$(echo $t | tr a-z A-Z)' 2>&1; cut -c1-90 /tmp/b"
 step $TAG-D-raw-$n-trailing "$C -X $m '$M/$t/' 2>&1; cut -c1-90 /tmp/b"
done
step $TAG-D-raw-override "$C -X POST -H 'X-HTTP-Method-Override: PUT' '$M/7/merge' 2>&1; cut -c1-90 /tmp/b"
mk(){ python3 -I -c "import json,sys; open('/tmp/j-'+sys.argv[1],'w').write(json.dumps(eval(sys.argv[2])))" "$1" "$2"; }
GQ='{"query":"mutation{%s(input:{projectPath:\"a/b\",iid:\"7\",sha:\"x\"}){errors}}"}'
mk ga '{"query":"mutation{mergeRequestAccept(input:{projectPath:\"a/b\",iid:\"7\",sha:\"x\"}){errors}}"}'
mk gb '{"query":"mutation{project:mergeRequestAccept(input:{projectPath:\"a/b\",iid:\"7\",sha:\"x\"}){errors}}"}'
mk gf '{"query":"mutation{...F} fragment F on Mutation{mergeRequestAccept(input:{projectPath:\"a/b\",iid:\"7\",sha:\"x\"}){errors}}"}'
mk gd '{"query":"mutation{mergeRequestSetDraft(input:{projectPath:\"a/b\",iid:\"7\",draft:False}){errors}}".replace("False","false")}'
mk gn '{"query":"mutation{createNote(input:{noteableId:\"x\",body:\"/approve\"}){errors}}"}'
mk gv '{"query":"mutation($i:CreateNoteInput!){createNote(input:$i){errors}}","variables":{"i":{"noteableId":"x","body":"/merge"}}}'
mk gc '{"query":"mutation{mergeRequestCreate(input:{projectPath:\"a/b\",title:\"t\",sourceBranch:\"f\",targetBranch:\"m\",description:\"x\\n/merge\"}){errors}}"}'
mk d1 '{"title":"t","description":"/merge"}'
mk d2 '{"title":"t","description":"hi\n/approve"}'
mk d4 '{"title":"t","description":"x\r/merge"}'
mk d5 '{"description":"/merge"}'
mk n1 '{"body":"/approve"}'
mk n2 '{"body":"/merge"}'
mk f1 '{"title":"t","description":"see\n/usr/bin/env"}'
mk f2 '{"title":"t","description":"run\n/merge the two lists by hand"}'
mk f3 '{"title":"t","description":"```\n/merge\n```"}'
mk f4 '{"title":"t","description":"/assign_reviewer @bob\n/target_branch release"}'
printf '%s' '{"title":"t","description":"\u002fmerge"}' > /tmp/j-d3
J="-H 'Content-Type: application/json'"
step $TAG-D-gql-accept "$C $J --data-binary @/tmp/j-ga $H/api/graphql 2>&1; cut -c1-90 /tmp/b"
step $TAG-D-gql-alias "$C $J --data-binary @/tmp/j-gb $H/api/graphql 2>&1; cut -c1-90 /tmp/b"
step $TAG-D-gql-fragment "$C $J --data-binary @/tmp/j-gf $H/api/graphql 2>&1; cut -c1-90 /tmp/b"
step $TAG-gql-neighbour-set-draft "$C $J --data-binary @/tmp/j-gd $H/api/graphql 2>&1; cut -c1-90 /tmp/b"
step $TAG-raw-neighbour-update "$C -X PUT $J -d '{\"target_branch\":\"release\"}' '$M/7' 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-desc-merge "$C $J --data-binary @/tmp/j-d1 '$M' 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-desc-approve-line2 "$C $J --data-binary @/tmp/j-d2 '$M' 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-desc-escaped "$C $J --data-binary @/tmp/j-d3 '$M' 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-desc-cr "$C $J --data-binary @/tmp/j-d4 '$M' 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-query "$C -X POST '$M?title=t&description=/merge' 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-form "$C -X POST -d 'title=t&description=/merge' '$M' 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-update "$C -X PUT $J --data-binary @/tmp/j-d5 '$M/7' 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-note "$C $J --data-binary @/tmp/j-n1 '$M/7/notes' 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-discussion "$C $J --data-binary @/tmp/j-n2 '$M/7/discussions' 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-issue-note "$C $J --data-binary @/tmp/j-n2 '$P/$A/issues/1/notes' 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-commit-comment "$C $J -d '{\"note\":\"/merge\"}' '$P/$A/repository/commits/abc/comments' 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-gql-note "$C $J --data-binary @/tmp/j-gn $H/api/graphql 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-gql-note-var "$C $J --data-binary @/tmp/j-gv $H/api/graphql 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-gql-mr-create "$C $J --data-binary @/tmp/j-gc $H/api/graphql 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-glab-create "glab mr create --title t --description /merge --source-branch feature --target-branch main --yes 2>&1 | tail -1 | cut -c1-120"
step $TAG-T-glab-note "glab mr note 7 -m /merge 2>&1 | tail -1 | cut -c1-120"
step $TAG-T-fp-path-in-desc "$C $J --data-binary @/tmp/j-f1 '$M' 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-fp-mention "$C $J --data-binary @/tmp/j-f2 '$M' 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-codeblock "$C $J --data-binary @/tmp/j-f3 '$M' 2>&1; cut -c1-90 /tmp/b"
step $TAG-T-other-action "$C $J --data-binary @/tmp/j-f4 '$M' 2>&1; cut -c1-90 /tmp/b"
cd /; rm -rf $W
