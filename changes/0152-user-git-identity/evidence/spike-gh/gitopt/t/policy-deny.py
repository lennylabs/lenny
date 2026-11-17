import json
a=json.load(open('/t/policy.json'))
gm=["addPullRequestReview","submitPullRequestReview","updatePullRequestReview","deletePullRequestReview","dismissPullRequestReview","mergePullRequest","enablePullRequestAutoMerge","disablePullRequestAutoMerge","enqueuePullRequest","dequeuePullRequest","updatePullRequestBranch","revertPullRequest","mergeBranch","createCommitOnBranch","createRef","updateRef","updateRefs","deleteRef"]
gr=[{"method":"POST","path":".../pulls/{n}/reviews"},{"method":"POST","path":".../pulls/{n}/reviews/{id}/events"},{"method":"PUT","path":".../pulls/{n}/merge"},{"method":"PUT","path":".../pulls/{n}/update-branch"},{"method":"POST","path":".../merges"},{"method":"POST","path":".../git/refs/**"},{"method":"PATCH","path":".../git/refs/**"},{"method":"DELETE","path":".../git/refs/**"},{"method":"POST","path":".../git/refs"},{"method":"PATCH","path":".../git/refs"},{"method":"DELETE","path":".../git/refs"}]
base=lambda pre,gq:{"mode":"deny","restPrefix":pre,"graphqlPath":gq,"maxBody":262144,"lexStrict":True,"canonicalForward":True,"maxDepth":64,"maxTokens":15000,"denyMutations":gm,"denyRest":gr,"stripHeaders":["Authorization"]}
gl={"mode":"deny","restPrefix":"/api/v4","graphqlPath":"/api/graphql","maxBody":262144,"lexStrict":True,"canonicalForward":True,"maxDepth":64,"maxTokens":15000,
 "denyMutations":["mergeRequestAccept"],
 "denyRest":[{"method":"POST","path":".../merge_requests/{n}/approve"},{"method":"POST","path":".../merge_requests/{n}/unapprove"},{"method":"PUT","path":".../merge_requests/{n}/merge"},{"method":"PUT","path":".../merge_requests/{n}/rebase"},{"method":"PUT","path":".../merge_requests/{n}/reset_approvals"},{"method":"POST","path":".../merge_requests/{n}/cancel_merge_when_pipeline_succeeds"}],
 "stripHeaders":["Authorization","Private-Token","Job-Token"]}
# the text rule, as a variant, so the routes can be measured with and without it
d={"ghes":base("/api/v3","/api/graphql"),"ghcom":base("","/graphql"),"gitlab":gl}
json.dump(d,open('/t/policy-deny-nowtext.json','w'),indent=1)
tr=[{"path":".../merge_requests","regex":"(?:^|[\\r\\n\\x0b\\x0c\\x{85}\\x{2028}\\x{2029}])[ \\t]*/(?:approve|unapprove|merge|rebase)\\b"},{"path":".../merge_requests/{n}","regex":"(?:^|[\\r\\n\\x0b\\x0c\\x{85}\\x{2028}\\x{2029}])[ \\t]*/(?:approve|unapprove|merge|rebase)\\b"},{"path":".../notes","regex":"(?:^|[\\r\\n\\x0b\\x0c\\x{85}\\x{2028}\\x{2029}])[ \\t]*/(?:approve|unapprove|merge|rebase)\\b"},{"path":".../discussions","regex":"(?:^|[\\r\\n\\x0b\\x0c\\x{85}\\x{2028}\\x{2029}])[ \\t]*/(?:approve|unapprove|merge|rebase)\\b"}]
gl2=dict(gl); gl2["bodyRules"]=tr
d2=dict(d); d2["gitlab"]=gl2
json.dump(d2,open('/t/policy-deny.json','w'),indent=1)
print(list(d2))
