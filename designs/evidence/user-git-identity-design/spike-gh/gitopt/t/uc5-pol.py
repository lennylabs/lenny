# uc5-pol.py: the presets as user-credential-delivery-design.md revision 5 writes them, as data.
import json
R='{repo}'
W=['WRITE']
GH=[R+'/pulls/*/reviews/**',R+'/pulls/*/merge',R+'/pulls/*/merge-async',R+'/pulls/*/update-branch',R+'/merges',R+'/merge-upstream',
 R+'/contents/**',R+'/git/refs/**',R+'/git/commits',R+'/git/trees',R+'/git/tags',
 R+'/actions/runs/*/pending_deployments',R+'/actions/runs/*/approve',R+'/actions/runs/*/deployment_protection_rule',R+'/actions/runs/*/force-cancel',
 R+'/environments/*/deployment-branch-policies/**',R+'/environments/*/deployment_protection_rules/**',
 R+'/branches/**',R+'/rulesets/**','/orgs/*/rulesets/**',R+'/actions/permissions/**',
 '/user/keys/**','/user/gpg_keys/**','/user/ssh_signing_keys/**',R+'/keys/**']
GM=['addPullRequestReview','submitPullRequestReview','updatePullRequestReview','deletePullRequestReview','dismissPullRequestReview',
 'mergePullRequest','enablePullRequestAutoMerge','disablePullRequestAutoMerge','enqueuePullRequest','dequeuePullRequest','mergeBranch','updatePullRequestBranch','revertPullRequest',
 'createCommitOnBranch','createRef','updateRef','updateRefs','deleteRef','createLinkedBranch',
 'approveDeployments','rejectDeployments','createEnvironment','updateEnvironment','deleteEnvironment','pinEnvironment','reorderEnvironment',
 'createBranchProtectionRule','updateBranchProtectionRule','deleteBranchProtectionRule','createRepositoryRuleset','updateRepositoryRuleset','deleteRepositoryRuleset']
GENC=['**/contents/{enc*}','**/branches/{enc}','**/branches/{enc}/*','**/branches/{enc}/*/*','**/branches/{enc}/*/*/*','**/labels/{enc}','**/compare/{enc*}','**/git/ref/{enc*}','**/git/refs/{enc*}','**/git/matching-refs/{enc*}','**/releases/tags/{enc}','**/commits/{enc}','**/commits/{enc}/*','**/commits/{enc}/*/*','**/environments/{enc}','**/environments/{enc}/*','**/environments/{enc}/*/*','**/environments/{enc}/*/*/*']
P='projects/*'
GLR=[P+'/merge_requests/*/approve',P+'/merge_requests/*/unapprove',P+'/merge_requests/*/reset_approvals',P+'/approvals',P+'/approval_rules/**',P+'/merge_requests/*/approvals',P+'/merge_requests/*/approval_rules/**',
 P+'/merge_requests/*/merge',P+'/merge_requests/*/rebase',P+'/merge_requests/*/cancel_merge_when_pipeline_succeeds',
 P+'/repository/commits',P+'/repository/files/**',P+'/repository/branches',P+'/repository/branches/**',
 P+'/protected_branches/**',
 'personal_access_tokens/self/rotate','user/keys/**','deploy_keys/**',P+'/deploy_keys/**',P+'/access_tokens',P+'/deploy_tokens']
RW=['merge_requests','issues','repository','protected_branches','deploy_keys','approval_rules','approvals','pipelines','hooks','members']
GLENC=[{'pattern':'projects/{enc}','refuseDecoded':RW},{'pattern':'projects/{enc}/*','refuseDecoded':RW},{'pattern':'projects/{enc}/*/*','refuseDecoded':RW},{'pattern':'projects/{enc}/*/*/*','refuseDecoded':RW},{'pattern':'projects/{enc}/*/*/*/*','refuseDecoded':RW},{'pattern':'projects/{enc}/*/*/*/*/*','refuseDecoded':RW},{'pattern':'groups/{enc}','refuseDecoded':RW},{'pattern':'groups/{enc}/*','refuseDecoded':RW},{'pattern':'groups/{enc}/*/*','refuseDecoded':RW}]
for x in ['repository/branches/{enc}','repository/branches/{enc}/*','repository/files/{enc}','repository/files/{enc}/*','repository/tags/{enc}','repository/commits/{enc}','repository/commits/{enc}/*','repository/tree/{enc}','labels/{enc}','wikis/{enc}']: GLENC.append({'pattern':'**/'+x})
def api(p): return [{'pattern':'api/v4/'+e['pattern'],**{k:v for k,v in e.items() if k!='pattern'}} if not e['pattern'].startswith('**') else e for e in p]
QA='approve|lgtm|unapprove|merge|rebase|submit_review|target_branch|run_pipeline'
STRIP=['Authorization','Private-Token','Job-Token','Deploy-Token','X-Api-Key','Cookie','Proxy-Authorization']
def rules(l): return [{'action':'deny','methods':W,'path':('/'+x if not x.startswith(('/','{')) else x)} for x in l]
def gh(pre,gq,up=''):
    return {'mode':'deny','matcher':'uc4','restPrefix':pre,'denyMutations':GM,'stripHeaders':STRIP,
      'uc4':{'default':'allow','repoRoots':['/repos/*/*','/repositories/*'],'rules':rules(GH)+[{'action':'deny','methods':['PUT','DELETE'],'path':R+'/environments/*'}],'graphqlPaths':[gq],'writeNoQuery':True,'writeJSONOnly':True,'uploadsPath':up,
             'encodedSegments':[{'pattern':e} for e in GENC],'refuseFamilies':True,'undecodableRefused':True}}
gitlab={'mode':'deny','matcher':'uc4','restPrefix':'/api/v4/','denyMutations':['mergeRequestAccept','commitCreate','branchCreate','branchDelete'],'stripHeaders':STRIP+['Sudo'],
  'uc4':{'default':'allow','rules':rules(GLR),'graphqlPaths':['/api/graphql'],'writeNoQuery':True,'writeJSONOnly':True,'encodedSegments':api(GLENC),'refuseFamilies':True,
         'denyBodyKeys':['merge_when_pipeline_succeeds','auto_merge'],'undecodableRefused':True,'bodyPatterns':['(?i)^\\s*/('+QA+')\\b']}}
generic={'mode':'deny','matcher':'uc4','restPrefix':'/','denyMutations':['addComment'],'stripHeaders':['Authorization','Cookie'],
  'uc4':{'default':'allow','rules':[{'action':'deny','methods':['DELETE'],'path':'/rest/api/*/issue/*'}],'graphqlPaths':['/graphql']}}
genericb={'mode':'deny','matcher':'uc4','restPrefix':'/','stripHeaders':['Authorization','Cookie'],
  'uc4':{'default':'allow','bodyPatterns':['(?i)^\\s*/(lgtm|approve)\\b']}}
d={'ghes':gh('/api/v3/','/api/graphql','/api/uploads/repos/*/*/releases/*/assets'),'ghcom':gh('/','/graphql'),'gitlab':gitlab,'generic':generic,'genericb':genericb}
json.dump(d,open('/t/policy-uc5.json','w'),indent=1)
# a oracle: a separate suffix reading of what the stub saw
O=[]
for x in GH:
    p=x.replace('{repo}/','.../').replace('/**','/@@').replace('*','{x}').replace('/@@','/**')
    for m in('POST','PUT','PATCH','DELETE'): O.append({'method':m,'path':p})
for m in('PUT','DELETE'): O.append({'method':m,'path':'.../environments/{x}'})
json.dump({'ghes':{'denyMutations':GM,'denyRest':O},'ghcom':{'denyMutations':GM,'denyRest':O}},open('/t/policy-uc5-oracle.json','w'),indent=1)
# hosts: earlier data plus the LFS test repositories, with the provider level the text names
v=json.load(open('/t/vcs.json'))
for h in v:
    for r in('acme/lfsgl','acme/lfsother','acme/lfsvar','acme/lfsexp','acme/lfsevil','acme/lfsothera'):
        h['readable'].append(r); h['writable'].append(r)
    h['lfsTransferDeclared']=h['host'] in('gitlab.example.test',)
    h['level']='mint-coarse' if h['host']=='gitlab.example.test' else 'mint-scoped'
v2=json.loads(json.dumps(v))
for h in v2:
    if h['host']=='gitlab.example.test': h['lfsLiveRunPassed']=True
json.dump(v,open('/t/vcs5-gitlab-refused.json','w'),indent=1); json.dump(v2,open('/t/vcs5.json','w'),indent=1)
M=json.loads(json.dumps(v2))
for h in M:
    if h['host']=='ghes.example.test': h['fetchVia']='mirror.example.test'
json.dump(M,open('/t/vcs5-mirror.json','w'),indent=1)
Pl=lambda *f:[{'prefix':'acme/','mode':'required','allowRuntimeHeldCredentials':x} for x in f]
base=lambda **k:{**{'pools':Pl(True),'released':True,'suspended':False,'current':'USER-FILE-CURRENT','handling':2},**k}
S={'all':base(),'rotated':base(current='USER-FILE-ROTATED'),'suspended':base(suspended=True),'flagoff':base(pools=Pl(False)),'norelease':base(released=False),'twopools':base(pools=Pl(True,False)),'twopoolson':base(pools=Pl(True,True))}
for k,x in S.items(): json.dump({'ghes.example.test':x,'github.com':x,'gitlab.example.test':x},open(f'/t/s5-{k}.json','w'),indent=1)
b=json.load(open('/t/bindings-uc4.json'))
json.dump(b,open('/t/bindings-uc5.json','w'),indent=1)
print('rest',len(GH)+1,'mutations',len(GM),'gitlab rest',len(GLR))
