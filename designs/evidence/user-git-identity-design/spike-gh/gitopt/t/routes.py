# routes.py: from GitHub's published OpenAPI description and GraphQL schema, every write route and mutation with a review, merge, ref, protection, or ruleset effect.
import json,re
o=json.load(open('/src/openapi.json'))
ver=o['info'].get('version'); 
C=[('review',r'/pulls/\{pull_number\}/(reviews|requested_reviewers)'),('merge',r'/pulls/\{pull_number\}/(merge|update-branch)|/merges$|/merge-upstream$|/merge-queue'),('ref',r'/git/(refs|ref|commits|trees|tags)|/contents/\{path\}|/branches/\{branch\}/rename|/git/matching'),('protection',r'/protection|/tags/protection|/actions/permissions|/environments/\{environment_name\}|/pending_deployments|/deployment_protection|/approve$|/force-cancel|/approvals$|/deployment-branch|/rules/branches'),('ruleset',r'/rulesets')]
rows=[]
for p,ops in o['paths'].items():
    if not p.startswith('/repos/') : continue
    for m,v in ops.items():
        if m not in('post','put','patch','delete'): continue
        for c,rx in C:
            if re.search(rx,p):
                rows.append((c,m.upper(),p,v.get('operationId',''))); break
rows.sort()
out=[]
fill={'owner':'acme','repo':'widgets','pull_number':'7','review_id':'1','run_id':'1','branch':'main','ref':'heads/x','commit_sha':'abc','tree_sha':'abc','tag_sha':'abc','path':'f.txt','ruleset_id':'1','environment_name':'prod','protection_rule_id':'1','tag_protection_id':'1','custom_protection_rule_id':'1','pattern':'p','secret_name':'S','variable_name':'V','attempt_number':'1','workflow_id':'1','file_sha':'abc'}
for c,m,p,op in rows:
    q=re.sub(r'\{([a-z_]+)\}',lambda x:fill.get(x.group(1),'1'),p)
    out.append({'class':c,'method':m,'template':p,'path':q,'op':op})
json.dump(out,open('/run/routes.json','w'))
print('source: github/rest-api-description descriptions/api.github.com/api.github.com.json, info.version',ver)
print('write routes under /repos with the listed effects:',len(rows))
for c,m,p,op in rows: print(f"{c:11s} {m:6s} {p}  [{op}]")
g=open('/src/schema.graphql').read()
st=re.search(r'^type Mutation[^{]*\{',g,re.M).start(); mt=g[st:]; mt=mt[:mt.index('\n}')]
names=re.findall(r'^  ([a-zA-Z]+)\(',mt,re.M)
rx=re.compile(r'review|merge|enqueue|dequeue|ref\b|refs\b|Ref$|Refs$|createCommit|protection|ruleset|deployment|environment|reopenPull|closePull|updatePullRequestBranch|revert',re.I)
ms=[n for n in names if rx.search(n)]
json.dump(ms,open('/run/mutations.json','w'))
print('\nsource: docs.github.com/public/fpt/schema.docs.graphql, Mutation fields:',len(names),'selected:',len(ms))
for n in ms: print(' ',n)
