#!/bin/bash
# glab-capture.sh <outdir> [attack]: runs glab through netagent against the stub. GITLAB_HOST, placeholder token, no config.
O=$(cd "$(dirname "$0")" && pwd); cd "$O"
OUT=raw/$1; mkdir -p $OUT
ENVV="GITLAB_HOST=gitlab.example.test GITLAB_TOKEN=lenny-injected GLAB_NO_PROMPT=1 GLAB_CONFIG_DIR=/tmp/glab GLAB_CHECK_UPDATE=false GLAB_SEND_TELEMETRY=false NO_COLOR=1"
run(){
 local l=$1; shift
 docker exec gh-cli sh -c "curl -s http://gh-stub:9000/drain >/dev/null"
 docker exec gh-cli sh -c "cd /tmp/glrepo && env $ENVV timeout 30 $*" >$OUT/$l.out 2>$OUT/$l.err </dev/null; local rc=$?
 docker exec gh-cli curl -s http://gh-stub:9000/drain >$OUT/$l.jsonl
 echo "rc=$rc" >>$OUT/$l.out
 printf '%-28s rc=%-3s reqs=%-3s %s\n' $l $rc $(wc -l <$OUT/$l.jsonl) "$(tail -1 $OUT/$l.err | cut -c1-130)"
}
if [ "${2:-}" != attack ]; then
run mr-create-basic   "glab mr create --title t --description d --source-branch feature --target-branch main --yes"
run mr-create-all     "glab mr create --title t --description d --source-branch feature --target-branch main --draft --assignee bob --reviewer bob --label bug --milestone v1 --yes"
run mr-view           "glab mr view 7"
run mr-view-comments  "glab mr view 7 --comments"
run mr-list           "glab mr list"
run mr-approve        "glab mr approve 7"
run mr-merge          "glab mr merge 7 --yes"
run mr-merge-auto     "glab mr merge 7 --auto-merge --yes"
run mr-merge-now      "glab mr merge 7 --auto-merge=false --yes"
run mr-merge-squash   "glab mr merge 7 --squash --remove-source-branch --yes"
run api-rest-get      "glab api projects/acme%2Fwidgets"
run api-rest-post     "glab api --method POST projects/acme%2Fwidgets/merge_requests -f title=t -f source_branch=feature -f target_branch=main"
run api-graphql       "glab api graphql -f query='query { currentUser { username } }'"
run api-graphql-mut   "glab api graphql -f query='mutation { mergeRequestAccept(input:{projectPath:\"acme/widgets\",iid:\"7\",sha:\"x\"}){ errors } }'"
fi
run a-approve         "glab mr approve 7"
run a-merge           "glab mr merge 7 --yes"
run a-api-approve     "glab api --method POST projects/acme%2Fwidgets/merge_requests/7/approve"
run a-api-unapprove   "glab api --method POST projects/acme%2Fwidgets/merge_requests/7/unapprove"
run a-api-merge       "glab api --method PUT projects/acme%2Fwidgets/merge_requests/7/merge"
run a-api-merge-id    "glab api --method PUT projects/1/merge_requests/7/merge"
run a-api-mwps-cancel "glab api --method POST projects/acme%2Fwidgets/merge_requests/7/cancel_merge_when_pipeline_succeeds"
run a-api-rebase      "glab api --method PUT projects/acme%2Fwidgets/merge_requests/7/rebase"
run a-api-update      "glab api --method PUT projects/acme%2Fwidgets/merge_requests/7 -f target_branch=release"
run a-api-note-quick  "glab api --method POST projects/acme%2Fwidgets/merge_requests/7/notes -f body=/approve"
run a-gql-accept      "glab api graphql -f query='mutation { mergeRequestAccept(input:{projectPath:\"acme/widgets\",iid:\"7\",sha:\"x\"}){ errors } }'"
run a-gql-alias       "glab api graphql -f query='mutation { x: mergeRequestAccept(input:{projectPath:\"acme/widgets\",iid:\"7\",sha:\"x\"}){ errors } }'"
run a-gql-approve     "glab api graphql -f query='mutation { mergeRequestApprove: mergeRequestSetDraft(input:{projectPath:\"acme/widgets\",iid:\"7\",draft:false}){ errors } }'"
run a-quick-approve   "glab mr create --title t --description /approve --source-branch feature --target-branch main --yes"
run a-quick-merge     "glab mr create --title t --description /merge --source-branch feature --target-branch main --yes"
run a-api-quick-merge "glab api --method POST projects/acme%2Fwidgets/merge_requests -f title=t -f source_branch=feature -f target_branch=main -f description=/merge"
run a-api-auto-create "glab api --method POST projects/acme%2Fwidgets/merge_requests -f title=t -f source_branch=feature -f target_branch=main -F merge_when_pipeline_succeeds=true"
