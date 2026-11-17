# t34.py: items 3 to 6, with a prediction per row from the text. The stub proves forwarding only.
import json,subprocess,time,datetime
steps=open('/out/uc6/rows.steps','w')
def now(): return datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%S.%f')+'000Z'
J=json.dumps
AU={'ghes.example.test':'Authorization: token lenny-injected','api.github.com':'Authorization: token lenny-injected','gitlab.example.test':'Private-Token: lenny-injected'}
def run(group,label,host,m,path,exp,hdr=(),body=None,ct='application/json',protos=('--http1.1','--http2'),scheme='https'):
    for proto in protos:
        a=['curl','-sS','-m','12','--path-as-is','-o','/tmp/b','-w','%{http_code}','--cacert','/s/bundle.pem','-x','127.0.0.1:3128',proto,'-X',m]
        if host in AU: a+=['-H',AU[host]]
        for h in hdr: a+=['-H',h]
        if body is not None:
            open('/tmp/bd','wb').write(body if isinstance(body,bytes) else body.encode()); a+=['--data-binary','@/tmp/bd']
            if ct: a+=['-H','Content-Type: '+ct]
        a.append(f'{scheme}://{host}{path}')
        s=now()
        try: r=subprocess.run(a,capture_output=True,text=True,timeout=16); code=r.stdout.strip(); err=r.stderr.strip()[:70]
        except subprocess.TimeoutExpired: code='timeout'; err=''
        time.sleep(0.02); e=now()
        steps.write(J({'group':group,'label':label,'host':host,'exp':exp,'proto':proto[6:],'http':code,'err':err,'start':s,'end':e})+'\n'); steps.flush()
g=lambda q,**k: J({'query':q,**k})
Q='query{viewer{login}}'; DM='mutation{addPullRequestReview(input:{}){clientMutationId}}'; DGL='mutation{mergeRequestAccept(input:{projectPath:"a/b",iid:"7",sha:"x"}){errors}}'
# 3 the stem's direction
for host,G in(('api.github.com','/graphql'),('ghes.example.test','/api/graphql'),('gitlab.example.test','/api/graphql')):
    D=DGL if 'gitlab' in host else DM
    for suf in('.json','.foo','.json.foo','.JSON'):
        run('3',f'{G}{suf} with a denied mutation',host,'POST',G+suf,'ref',body=g(D))
        run('3',f'{G}{suf} with an allowed query',host,'POST',G+suf,'ref',body=g(Q))
    run('3',f'{G} with a denied mutation',host,'POST',G,'ref',body=g(D)); run('3',f'{G} with an allowed query',host,'POST',G,'fwd',body=g(Q))
U='/api/uploads/repos/acme/widgets/releases/1/assets'
run('3','uploads path exact','ghes.example.test','POST',U+'?name=a','fwd',body=b'\x00',ct='application/octet-stream')
for suf in('.json','.foo','.bin.foo'): run('3','uploads path'+suf,'ghes.example.test','POST',U+suf+'?name=a','ref',body=b'\x00',ct='application/octet-stream')
H='other.example.test'
run('3','generic /graphql, denied name',H,'POST','/graphql','ref',body=g('mutation{addComment(input:{}){clientMutationId}}')); run('3','generic /graphql, allowed',H,'POST','/graphql','fwd',body=g(Q))
for suf in('.json','.foo'): run('3','generic POST /graphql'+suf+', denied name',H,'POST','/graphql'+suf,'ref',body=g('mutation{addComment(input:{}){clientMutationId}}'))
run('3','generic PUT /graphql.json, denied name, not POST',H,'PUT','/graphql.json','fwd',body=g('mutation{addComment(input:{}){clientMutationId}}'))
C='gen-c.example.test'
run('3','allow rule, exact',C,'GET','/rest/api/2/project','fwd'); run('3','allow rule, POST exact',C,'POST','/rest/api/2/project','fwd',body='{}')
for suf in('.json','.foo','.json.foo'):
    run('3','allow rule'+suf+' never matches',C,'GET','/rest/api/2/project'+suf,'ref'); run('3','allow rule POST'+suf,C,'POST','/rest/api/2/project'+suf,'ref',body='{}')
run('3','no rule at all under default deny',C,'GET','/x','ref')
for p,e in(('report.tar','ref'),('report.tar.gz','ref'),('report.tar.gz.bak','ref'),('REPORT.TAR.GZ','ref'),('report.txt','fwd'),('report','fwd'),('report.ta','fwd'),('report.tarx','fwd'),('x.report.tar','fwd')):
    run('3','dotted deny, DELETE /files/'+p,C,'DELETE','/files/'+p,e)
run('3','dotted deny, GET passes',C,'GET','/files/report.tar.gz','fwd') if False else None
# 4 carried names
GH=['addPullRequestReview','mergePullRequest','createRef','createCommitOnBranch','updatePullRequestBranch','approveDeployments','createEnvironment','createBranchProtectionRule','createRepositoryRuleset']
GL=['mergeRequestAccept','commitCreate','branchCreate','branchDelete']
W='gh-on-gl.example.test'; X='gl-on-ghes.example.test'
for n in GL+GH:
    run('4',f'github-deny on a GitLab host, {n}',W,'POST','/api/graphql','ref',body=g('mutation{%s(input:{}){clientMutationId}}'%n))
    run('4',f'github-deny on a GitLab host, alias {n}',W,'POST','/api/graphql','ref',body=g('mutation{x:%s(input:{}){clientMutationId}}'%n),protos=('--http1.1',))
    run('4',f'gitlab-deny on GHES, {n}',X,'POST','/api/graphql','ref',body=g('mutation{%s(input:{}){clientMutationId}}'%n))
    run('4',f'gitlab-deny on GHES, query naming {n}',X,'POST','/api/graphql','ref',body=g('query{%s}'%n),protos=('--http1.1',))
for host,lab in((W,'github-deny on a GitLab host'),(X,'gitlab-deny on GHES')):
    run('4',lab+', other mutation',host,'POST','/api/graphql','fwd',body=g('mutation{createIssue(input:{}){clientMutationId}}'))
    run('4',lab+', query',host,'POST','/api/graphql','fwd',body=g(Q))
for m,p in(('POST','/api/v4/projects/1/merge_requests/7/approve'),('PUT','/api/v4/projects/1/merge_requests/7/merge'),('GET','/api/v4/projects/1'),('POST','/api/v4/projects/1/issues')):
    run('4','github-deny on a GitLab host, REST '+m+' '+p,W,m,p,'ref',body=None if m=='GET' else '{}')
for m,p in(('PUT','/api/v3/repos/a/b/pulls/7/merge'),('POST','/api/v3/repos/a/b/pulls/7/reviews'),('GET','/api/v3/repos/a/b'),('POST','/repos/a/b/pulls/7/reviews'),('PUT','/repos/a/b/pulls/7/merge')):
    run('4','gitlab-deny on GHES, REST '+m+' '+p,X,m,p,'ref',body=None if m=='GET' else '{}')
for n,host,G in(('mergeRequestAccept','ghes.example.test','/api/graphql'),('commitCreate','api.github.com','/graphql'),('mergePullRequest','gitlab.example.test','/api/graphql')):
    run('4',f'own flavour carries {n}',host,'POST',G,'ref',body=g('mutation{%s(input:{}){clientMutationId}}'%n))
# 5 routing overrides on every terminated host
HS=[('api.example.test','no rules'),('other.example.test','generic rules'),('ghes.example.test','preset, covered'),('api.github.com','preset'),('gitlab.example.test','preset, covered'),('github.com','covered, no binding')]
for host,lab in HS:
    for h in('X-Original-URL: /x','X-Rewrite-URL: /x','X-Forwarded-Path: /x','X-Forwarded-Prefix: /x','X-Forwarded-Uri: /x','X-Envoy-Original-Path: /x','x-original-url: /x','X-Original-URL;'):
        run('5',f'{lab} {host} GET {h}',host,'GET','/plain/path','ref',hdr=(h,))
    run('5',f'{lab} {host} POST X-Original-URL',host,'POST','/plain/path','ref',hdr=('X-Original-URL: /x',),body='{}')
run('5','no rules, plain absolute-form, X-Original-URL','api.example.test','GET','/plain/path','ref',hdr=('X-Original-URL: /x',),scheme='http',protos=('--http1.1',))
run('5','no rules, X-HTTP-Method-Override is not on the routing list','api.example.test','POST','/plain/path','fwd',hdr=('X-HTTP-Method-Override: PUT',),body='{}')
run('5','no rules, no header','api.example.test','GET','/plain/path','fwd')
run('5','opaque host, netagent cannot see it','lfs-store.example.test','GET','/plain/path','fwd',hdr=('X-Original-URL: /x',))
# 6 GitLab token routes
for root,rn in(('/api/v4/projects/acme%2Fwidgets','enc'),('/api/v4/projects/1','num')):
    for k in('access_tokens','deploy_tokens'):
        for m,p in(('POST',k),('DELETE',k+'/1'),('POST',k+'/1/rotate'),('PUT',k+'/1'),('DELETE',k+'/1.json'),('POST',k+'.foo')):
            run('6',f'{rn} {m} {p}','gitlab.example.test',m,root+'/'+p,'ref',body=None if m=='DELETE' else '{}')
        run('6',f'{rn} GET {k}','gitlab.example.test','GET',root+'/'+k,'fwd'); run('6',f'{rn} GET {k}/1','gitlab.example.test','GET',root+'/'+k+'/1','fwd')
steps.close()
