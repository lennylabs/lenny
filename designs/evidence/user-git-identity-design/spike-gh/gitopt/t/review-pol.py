import json
base=json.load(open('/t/policy-deny.json'))
routes=json.load(open('/run/routes.json')); muts=json.load(open('/run/mutations.json'))
ENC=["**/contents/{enc}","**/branches/{enc}","**/branches/{enc}/*","**/labels/{enc}","**/compare/{enc}","**/git/ref/{enc}","**/git/refs/{enc}","**/git/matching-refs/{enc}","**/releases/tags/{enc}","**/commits/{enc}","**/commits/{enc}/*","**/environments/{enc}","**/environments/{enc}/*","**/secrets/{enc}","**/variables/{enc}","**/topics/{enc}"]
GLENC=["/api/v4/projects/{enc}","/api/v4/projects/{enc}/**","/api/v4/projects/{enc}/*","/api/v4/projects/{enc}/*/*","/api/v4/projects/{enc}/*/*/*","/api/v4/projects/{enc}/*/*/*/*","/api/v4/groups/{enc}","/api/v4/groups/{enc}/*","**/repository/branches/{enc}","**/repository/files/{enc}","**/repository/files/{enc}/*","**/repository/tags/{enc}","**/repository/commits/{enc}","**/repository/commits/{enc}/*","**/labels/{enc}","**/wikis/{enc}","**/repository/tree/{enc}"]
def var(m,ext=False):
    d=json.loads(json.dumps(base))
    for k,v in d.items():
        v['matcher']=m
        if k=='ghes': v['apiPrefixes']=['/api/v3','/api/graphql','/api/uploads']; v['encodedPaths']=ENC
        if k=='ghcom': v['apiPrefixes']=[]; v['encodedPaths']=ENC
        if k=='gitlab': v['apiPrefixes']=['/api/v4','/api/graphql']; v['encodedPaths']=GLENC; v['stripHeaders']=v['stripHeaders']+(['Sudo'] if ext else [])
        if ext and k!='gitlab':
            have={(x['method'],x['path']) for x in v['denyRest']}
            import re
            for r in routes:
                if 'requested_reviewers' in r['template']: continue
                pat='.../'+re.sub(r'^/repos/\{owner\}/\{repo\}/','',r['template'])
                pat=re.sub(r'\{path\}$','**',pat)
                pat=re.sub(r'\{ref\}$','**',pat)
                if (r['method'],pat) not in have:
                    v['denyRest'].append({'method':r['method'],'path':pat}); have.add((r['method'],pat))
            v['denyMutations']=sorted(set(v['denyMutations'])|{'createBranchProtectionRule','updateBranchProtectionRule','deleteBranchProtectionRule','createRepositoryRuleset','updateRepositoryRuleset','deleteRepositoryRuleset','approveDeployments','rejectDeployments'})
    return d
json.dump(var('literal'),open('/t/policy-r-literal.json','w'),indent=1)
json.dump(var(''),open('/t/policy-r-decode.json','w'),indent=1)
json.dump(var('strict'),open('/t/policy-r-strict.json','w'),indent=1)
json.dump(var('strict',True),open('/t/policy-r-strictext.json','w'),indent=1)
print('ext github denyRest',len(var('strict',True)['ghcom']['denyRest']),'denyMutations',len(var('strict',True)['ghcom']['denyMutations']))
