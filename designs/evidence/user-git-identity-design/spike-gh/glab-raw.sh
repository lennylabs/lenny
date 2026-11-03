#!/bin/bash
# glab-raw.sh: raw requests against the GitLab binding through netagent. Prints status, filter header, and what reached the stub.
c(){ # label, curl args
 local l=$1; shift
 docker exec gh-cli curl -s http://gh-stub:9000/drain >/dev/null
 r=$(docker exec gh-cli curl -s -o /tmp/resp -D /tmp/hdr -w '%{http_code}' --cacert /s/pod-ca.pem --max-time 8 -H 'Private-Token: lenny-injected' "$@"; echo " rc=$?")
 f=$(docker exec gh-cli sh -c "grep -i '^x-lenny-filter' /tmp/hdr | tr -d '\r' | cut -d' ' -f2; python3 -c \"import json;print(json.load(open('/tmp/resp')).get('message','')[43:140])\" 2>/dev/null")
 n=$(docker exec gh-cli curl -s http://gh-stub:9000/drain | grep -c . )
 printf '%-44s http=%-10s stub=%s %s\n' "$l" "$r" "$n" "$(echo $f | cut -c1-120)"
}
H=https://gitlab.example.test
P=$H/api/v4/projects
J=(-H 'Content-Type: application/json')
c "ctl mr create"             -X POST "${J[@]}" -d '{"title":"t","description":"d","source_branch":"feature","target_branch":"main"}' $P/acme%2Fwidgets/merge_requests
c "ctl mr view, nested group" $P/acme%2Fsub%2Fwidgets/merge_requests/7
c "ctl lower-case %2f"        $P/acme%2fwidgets/merge_requests/7
c "ctl numeric project"       $P/1/merge_requests/7
c "description /merge"        -X POST "${J[@]}" -d '{"title":"t","description":"/merge","source_branch":"f","target_branch":"m"}' $P/acme%2Fwidgets/merge_requests
c "description /approve after text" -X POST "${J[@]}" -d '{"title":"t","description":"hello\n/approve","source_branch":"f","target_branch":"m"}' $P/acme%2Fwidgets/merge_requests
c "description JSON-escaped \\u002fmerge" -X POST "${J[@]}" -d '{"title":"t","description":"\u002fmerge","source_branch":"f","target_branch":"m"}' $P/acme%2Fwidgets/merge_requests
c "description CR-separated /merge" -X POST "${J[@]}" -d '{"title":"t","description":"x\r/merge","source_branch":"f","target_branch":"m"}' $P/acme%2Fwidgets/merge_requests
c "description in the query"  -X POST "${J[@]}" -d '{"title":"t"}' "$P/acme%2Fwidgets/merge_requests?description=/merge"
c "form body with description" -X POST -d 'title=t&description=/merge' $P/acme%2Fwidgets/merge_requests
c "ordinary path in description" -X POST "${J[@]}" -d '{"title":"t","description":"see\n/usr/bin/env for details","source_branch":"f","target_branch":"m"}' $P/acme%2Fwidgets/merge_requests
c "approve REST"              -X POST $P/acme%2Fwidgets/merge_requests/7/approve
c "merge REST"                -X PUT $P/acme%2Fwidgets/merge_requests/7/merge
c "merge fully encoded"       -X PUT $P/acme%2Fwidgets%2Fmerge_requests%2F7%2Fmerge
c "approve shaped as create"  -X POST "${J[@]}" -d '{"title":"t"}' $P/acme%2Fwidgets%2Fmerge_requests%2F7%2Fapprove/merge_requests
c "encoded dot dot in project" -X POST "${J[@]}" -d '{"title":"t"}' --path-as-is $P/acme%2F..%2Fwidgets/merge_requests
c "double slash"              -X PUT --path-as-is $P//acme%2Fwidgets/merge_requests/7/merge
c "dot dot"                   -X PUT --path-as-is $P/acme%2Fwidgets/x/../merge_requests/7/merge
c "trailing slash"            -X POST "${J[@]}" -d '{"title":"t"}' $P/acme%2Fwidgets/merge_requests/
c "case change"               -X PUT $P/acme%2Fwidgets/MERGE_REQUESTS/7/MERGE
c "encoded project slash on merge literal" -X PUT $P/acme%2Fwidgets/merge_requests/7%2Fmerge
c "method override"           -X POST "${J[@]}" -d '{"title":"t"}' -H 'X-HTTP-Method-Override: PUT' $P/acme%2Fwidgets/merge_requests
c "HEAD"                      -I $P/acme%2Fwidgets/merge_requests/7/merge
c "OPTIONS"                   -X OPTIONS $P/acme%2Fwidgets/merge_requests/7/merge
c "GraphQL accept"            -X POST "${J[@]}" -d '{"query":"mutation{mergeRequestAccept(input:{projectPath:\"a/b\",iid:\"7\",sha:\"x\"}){errors}}"}' $H/api/graphql
c "GraphQL alias"             -X POST "${J[@]}" -d '{"query":"mutation{project:mergeRequestAccept(input:{projectPath:\"a/b\",iid:\"7\",sha:\"x\"}){errors}}"}' $H/api/graphql
c "GraphQL create with /merge" -X POST "${J[@]}" -d '{"query":"mutation{mergeRequestCreate(input:{projectPath:\"a/b\",title:\"t\",sourceBranch:\"f\",targetBranch:\"m\",description:\"/merge\"}){errors}}"}' $H/api/graphql
c "GraphQL note /approve"     -X POST "${J[@]}" -d '{"query":"mutation{createNote(input:{noteableId:\"x\",body:\"/approve\"}){errors}}"}' $H/api/graphql
c "GraphQL batch"             -X POST "${J[@]}" -d '[{"query":"query{currentUser{username}}"}]' $H/api/graphql
c "GraphQL GET"               "$H/api/graphql?query=query%7BcurrentUser%7Busername%7D%7D"
c "Authorization header added" -H 'Authorization: Bearer lenny-injected' $P/1/merge_requests/7
c "note REST /approve"        -X POST "${J[@]}" -d '{"body":"/approve"}' $P/acme%2Fwidgets/merge_requests/7/notes
