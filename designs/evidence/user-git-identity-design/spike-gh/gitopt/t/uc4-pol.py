# uc4-pol.py: the presets as user-credential-delivery-design.md revision 3 writes them, as data, plus the oracle file the attack tool reads.
import json
R='{repo}'
GH=[R+'/pulls/*/reviews/**',R+'/pulls/*/merge',R+'/pulls/*/merge-async',R+'/pulls/*/update-branch',R+'/merges',R+'/merge-upstream',
 R+'/contents/**',R+'/git/refs/**',R+'/git/commits',R+'/git/trees',R+'/git/tags',R+'/branches/*/rename',
 R+'/actions/runs/*/pending_deployments',R+'/actions/runs/*/approve',R+'/actions/runs/*/deployment_protection_rule',R+'/actions/runs/*/force-cancel',R+'/environments/**',
 R+'/branches/*/protection/**',R+'/rulesets/**','/orgs/*/rulesets/**',R+'/actions/permissions/**',
 '/user/keys',R+'/keys']
GM=['addPullRequestReview','submitPullRequestReview','updatePullRequestReview','deletePullRequestReview','dismissPullRequestReview',
 'mergePullRequest','enablePullRequestAutoMerge','disablePullRequestAutoMerge','enqueuePullRequest','dequeuePullRequest','mergeBranch','updatePullRequestBranch','revertPullRequest',
 'createCommitOnBranch','createRef','updateRef','updateRefs','deleteRef','createLinkedBranch',
 'approveDeployments','rejectDeployments','updateEnvironment','deleteEnvironment',
 'createBranchProtectionRule','updateBranchProtectionRule','deleteBranchProtectionRule','createRepositoryRuleset','updateRepositoryRuleset','deleteRepositoryRuleset']
GENC=['**/contents/{enc*}','**/branches/{enc}','**/branches/{enc}/*','**/branches/{enc}/*/*','**/branches/{enc}/*/*/*','**/labels/{enc}','**/compare/{enc*}','**/git/ref/{enc*}','**/git/refs/{enc*}','**/git/matching-refs/{enc*}','**/releases/tags/{enc}','**/commits/{enc}','**/commits/{enc}/*','**/commits/{enc}/*/*','**/environments/{enc}','**/environments/{enc}/*','**/environments/{enc}/*/*','**/environments/{enc}/*/*/*']
P='projects/*'
GLR=[P+'/merge_requests/*/approve',P+'/merge_requests/*/unapprove',P+'/merge_requests/*/reset_approvals',P+'/approvals',P+'/approval_rules/**',P+'/merge_requests/*/approvals',P+'/merge_requests/*/approval_rules/**',
 P+'/merge_requests/*/merge',P+'/merge_requests/*/rebase',P+'/merge_requests/*/cancel_merge_when_pipeline_succeeds',
 P+'/repository/commits',P+'/repository/files/**',P+'/repository/branches',P+'/repository/branches/**',
 P+'/protected_branches/**',
 'personal_access_tokens/self/rotate','user/keys',P+'/deploy_keys/**']
RW=['merge_requests','issues','repository','protected_branches','deploy_keys','approval_rules','approvals','pipelines','hooks','members']
GLENC=[{'pattern':'projects/{enc}','refuseDecoded':RW},{'pattern':'projects/{enc}/*','refuseDecoded':RW},{'pattern':'projects/{enc}/*/*','refuseDecoded':RW},{'pattern':'projects/{enc}/*/*/*','refuseDecoded':RW},{'pattern':'projects/{enc}/*/*/*/*','refuseDecoded':RW},{'pattern':'projects/{enc}/*/*/*/*/*','refuseDecoded':RW},{'pattern':'groups/{enc}','refuseDecoded':RW},{'pattern':'groups/{enc}/*','refuseDecoded':RW},{'pattern':'groups/{enc}/*/*','refuseDecoded':RW}]
for x in ['repository/branches/{enc}','repository/branches/{enc}/*','repository/files/{enc}','repository/files/{enc}/*','repository/tags/{enc}','repository/commits/{enc}','repository/commits/{enc}/*','repository/tree/{enc}','labels/{enc}','wikis/{enc}']: GLENC.append({'pattern':'**/'+x})
def api(p): return [{'pattern':'api/v4/'+e['pattern'],**{k:v for k,v in e.items() if k!='pattern'}} if not e['pattern'].startswith('**') else e for e in p]
QA='approve|lgtm|unapprove|merge|rebase|submit_review|target_branch|run_pipeline'
STRIP=['Authorization','Private-Token','Job-Token','Deploy-Token','X-Api-Key','Cookie','Proxy-Authorization']
def rules(l): return [{'action':'deny','methods':['WRITE'],'path':('/'+x if not x.startswith(('/','{')) else x)} for x in l]
def gh(pre,gq,hosts=None):
    return {'mode':'deny','matcher':'uc4','restPrefix':pre,'denyMutations':GM,'stripHeaders':STRIP,
      'uc4':{'default':'allow','repoRoots':['/repos/*/*','/repositories/*'],'rules':rules(GH),'graphqlPaths':[gq],'writeNoQuery':True,'writeJSONOnly':True,
             'encodedSegments':[{'pattern':e} for e in GENC],'refuseFamilies':True,'undecodableRefused':True}}
gitlab={'mode':'deny','matcher':'uc4','restPrefix':'/api/v4/','denyMutations':['mergeRequestAccept'],'stripHeaders':STRIP+['Sudo'],
  'uc4':{'default':'allow','rules':rules(GLR),'graphqlPaths':['/api/graphql'],'writeNoQuery':True,'writeJSONOnly':True,'encodedSegments':api(GLENC),'refuseFamilies':True,
         'denyBodyKeys':['merge_when_pipeline_succeeds','auto_merge'],'undecodableRefused':True,'bodyPatterns':['(?i)^\\s*/('+QA+')\\b']}}
generic={'mode':'deny','matcher':'uc4','restPrefix':'/','denyMutations':['addComment'],'stripHeaders':['Authorization','Cookie'],
  'uc4':{'default':'allow','rules':[{'action':'deny','methods':['DELETE'],'path':'/rest/api/*/issue/*'}],'graphqlPaths':['/graphql']}}
genericb={'mode':'deny','matcher':'uc4','restPrefix':'/','stripHeaders':['Authorization','Cookie'],
  'uc4':{'default':'allow','bodyPatterns':['(?i)^\\s*/(lgtm|approve)\\b']}}
d={'ghes':gh('/api/v3/','/api/graphql'),'ghcom':gh('/','/graphql'),'gitlab':gitlab,'generic':generic,'genericb':genericb}
json.dump(d,open('/t/policy-uc4.json','w'),indent=1)
# the same with the content family removed, to read the merge rule alone on the false positive row
e=json.loads(json.dumps(d))
for k in('ghes','ghcom'): e[k]['uc4']['rules']=[r for r in e[k]['uc4']['rules'] if 'contents' not in r['path']]
json.dump(e,open('/t/policy-uc4-nocontents.json','w'),indent=1)
# small bounds, to reach the time rules without a long wait
f=json.loads(json.dumps(d))
for k in('ghes','ghcom','gitlab'): f[k]['uc4']['bodyReadMs']=3000
json.dump(f,open('/t/policy-uc4-bodyread3s.json','w'),indent=1)
g=json.loads(json.dumps(d))
for k in('ghes','ghcom','gitlab'): g[k]['uc4']['checkBudgetUs']=1
json.dump(g,open('/t/policy-uc4-budget1us.json','w'),indent=1)
# the oracle: a separate suffix reading of what the stub saw, so a row that reaches with a denied action shows even where the anchored rule misses it
O=[]
for x in GH:
    p=x.replace('{repo}/','.../').replace('*','{x}') if x.startswith('{repo}') else x
    p=p.replace('/{x}{x}','/**')
    for m in('POST','PUT','PATCH','DELETE'): O.append({'method':m,'path':p})
json.dump({'ghes':{'denyMutations':GM,'denyRest':O},'ghcom':{'denyMutations':GM,'denyRest':O}},open('/t/policy-uc4-oracle.json','w'),indent=1)
b=[{"hosts":["ghes.example.test"],"header":"Authorization","value":"token GH-REAL-55aa01","secret":"GH-REAL-55aa01","filter":"ghes"},
 {"hosts":["api.github.com"],"header":"Authorization","value":"token GHCOM-REAL-77cc02","secret":"GHCOM-REAL-77cc02","filter":"ghcom"},
 {"hosts":["api.acme.ghe.com"],"header":"Authorization","value":"token GHE-REAL-44bb03","secret":"GHE-REAL-44bb03","filter":"ghcom"},
 {"hosts":["gitlab.example.test"],"header":"Private-Token","value":"GL-REAL-66dd04","secret":"GL-REAL-66dd04","filter":"gitlab"},
 {"hosts":["other.example.test"],"header":"Authorization","value":"Bearer OTHER-REAL-11ee05","secret":"OTHER-REAL-11ee05","filter":"generic"},
 {"hosts":["nossh.example.test"],"header":"Authorization","value":"Bearer NOSSH-REAL-22ff06","secret":"NOSSH-REAL-22ff06","filter":"genericb"},
 {"hosts":["api.example.test"],"header":"Authorization","value":"Bearer ACME-REAL-9f2c71","secret":"ACME-REAL-9f2c71"}]
json.dump(b,open('/t/bindings-uc4.json','w'),indent=1)
print('rest',len(GH),'mutations',len(GM),'gitlab rest',len(GLR))
