# t26.py <label> <group...>: revision 5 rows through the egress endpoint with a hard curl timeout. Each row states what the design text predicts: ref or fwd. The stub proves forwarding only.
import json,subprocess,sys,time,datetime
lab=sys.argv[1]; groups=sys.argv[2:]; steps=open(f'/out/uc5/{lab}.steps','w')
def now(): return datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%S.%f')+'000Z'
J=json.dumps
F={'ghes':('ghes.example.test','/api/v3','/api/graphql','Authorization: token lenny-injected'),
   'com':('api.github.com','','/graphql','Authorization: token lenny-injected'),
   'ghe':('api.acme.ghe.com','','/graphql','Authorization: token lenny-injected'),
   'gl':('gitlab.example.test','/api/v4','/api/graphql','Private-Token: lenny-injected'),
   'gen':('other.example.test','','/graphql','Authorization: Bearer lenny-injected'),
   'genb':('nossh.example.test','','/graphql','Authorization: Bearer lenny-injected'),
   'norule':('api.example.test','','/graphql','Authorization: Bearer lenny-injected')}
def run(label,fl,m,path,exp,hdr=(),body=None,protos=('h1','h2'),ct='application/json',extra=()):
    host,_,_,auth=F[fl]
    for proto in protos:
        flag={'h1':'--http1.1','h2':'--http2'}[proto]
        a=['curl','-sS','-m','12','--path-as-is','-o','/tmp/b','-w','%{http_code}','--cacert','/s/bundle.pem','-x','127.0.0.1:3128',flag,'-X',m,'-H',auth]+list(extra)
        for h in hdr: a+=['-H',h]
        if body is not None:
            open('/tmp/bd','wb').write(body if isinstance(body,bytes) else body.encode('utf-8','surrogatepass')); a+=['--data-binary','@/tmp/bd']
            if ct and not any(h.lower().startswith('content-type') for h in hdr): a+=['-H','Content-Type: '+ct]
        a.append(f'https://{host}{path}')
        s=now()
        try:
            r=subprocess.run(a,capture_output=True,text=True,timeout=16); code=r.stdout.strip(); err=r.stderr.strip()[:80]
        except subprocess.TimeoutExpired: code='timeout'; err=''
        time.sleep(0.02); e=now()
        steps.write(J({'label':label,'proto':proto,'fl':fl,'method':m,'path':path,'exp':exp,'http':code,'err':err,'start':s,'end':e})+'\n'); steps.flush()
Q='query{viewer{login}}'
g=lambda q,**k: J({'query':q,**k})
D='mutation{addPullRequestReview(input:{pullRequestId:"P",event:APPROVE}){clientMutationId}}'
H1=('h1',)
GHM=['createEnvironment','pinEnvironment','reorderEnvironment','addPullRequestReview','submitPullRequestReview','updatePullRequestReview','deletePullRequestReview','dismissPullRequestReview','mergePullRequest','enablePullRequestAutoMerge','disablePullRequestAutoMerge','enqueuePullRequest','dequeuePullRequest','mergeBranch','updatePullRequestBranch','revertPullRequest','createCommitOnBranch','createRef','updateRef','updateRefs','deleteRef','createLinkedBranch','approveDeployments','rejectDeployments','updateEnvironment','deleteEnvironment','createBranchProtectionRule','updateBranchProtectionRule','deleteBranchProtectionRule','createRepositoryRuleset','updateRepositoryRuleset','deleteRepositoryRuleset']
def paths(fl):
    host,P,G,_=F[fl]; R=P+'/repos/acme/widgets'; A=P+'/repositories/123'
    run('fp contents/pulls/7/merge',fl,'PUT',R+'/contents/pulls/7/merge','ref',body='{"message":"m","content":"eA=="}')
    run('fp issues/pulls/7/merge',fl,'POST',R+'/issues/pulls/7/merge','fwd',body='{}')
    run('fp issues/7/merge',fl,'PUT',R+'/issues/7/merge','fwd',body='{}')
    run('fp x/pulls/7/reviews',fl,'POST',R+'/x/pulls/7/reviews','fwd',body='{}')
    run('repo graphql-tools GET',fl,'GET',P+'/repos/acme/graphql-tools','fwd')
    run('repo graphql-tools issues POST',fl,'POST',P+'/repos/acme/graphql-tools/issues','fwd',body='{"title":"t"}')
    run('repo graphql-tools pulls GET',fl,'GET',P+'/repos/acme/graphql-tools/pulls/7','fwd')
    run('repo graphql-tools merge',fl,'PUT',P+'/repos/acme/graphql-tools/pulls/7/merge','ref',body='{}')
    run('repo acme/graphql GET',fl,'GET',P+'/repos/acme/graphql','fwd')
    run('owner graphql GET',fl,'GET',P+'/repos/graphql/graphql-js','fwd')
    run('users/graphql GET',fl,'GET',P+'/users/graphql','fwd')
    run('search/graphql GET',fl,'GET',P+'/search/graphql','fwd')
    run('first seg graphql-tools',fl,'GET',P+'/graphql-tools','ref')
    run('first seg graphql',fl,'GET',P+'/graphql' if fl=='ghes' else '/graphql','ref' if fl=='ghes' else 'ref')
    run('first seg GraphQL case',fl,'POST',P+'/GraphQL','ref' if fl=='ghes' else 'fwd',body=g(Q))
    run('first seg GraphQL case, denied doc',fl,'POST',P+'/GraphQL','ref',body=g(D))
    for u in('/graphql2','/graphql/foo','/graphqlx','/api/foo','/api/graphqlx','/api/v3/graphql','/api/uploads/x','/api/uploads/repos/acme/widgets/releases/1'):
        run('family '+u,fl,'POST',u,'ref',body=g(Q))
    run('uploads path, query and binary',fl,'POST','/api/uploads/repos/acme/widgets/releases/1/assets?name=a.bin&label=x','fwd' if fl=='ghes' else 'ref',body=b'\x00\x01',ct='application/octet-stream')
    run('uploads path, _method',fl,'POST','/api/uploads/repos/acme/widgets/releases/1/assets?_method=PUT','ref',body=b'\x00',ct='application/octet-stream')
    run('uploads path, override header',fl,'POST','/api/uploads/repos/acme/widgets/releases/1/assets?name=a','ref',body=b'\x00',ct='application/octet-stream',hdr=('X-HTTP-Method-Override: PUT',))
    for m,p,e in (('POST',R+'/graphql','ref'),('POST',R+'/graphql.json','ref'),('POST',R+'/graphql.foo','ref'),('POST',R+'/GraphQL','ref'),('POST',P+'/repos/acme/graphql','ref'),('PATCH',P+'/repos/acme/graphql','fwd'),('PUT',R+'/graphql','fwd'),('GET',R+'/graphql','fwd'),('POST',P+'/orgs/o/artifacts/metadata/deployment-record/cluster/graphql','ref'),('POST',P+'/repos/acme/graphql/issues','fwd')):
        run(f'final graphql {m} {p}',fl,m,p,e,body=None if m=='GET' else '{}')
    run('gql trailing slash, allowed doc',fl,'POST',G+'/','fwd',body=g(Q))
    run('gql trailing slash, denied doc',fl,'POST',G+'/','ref',body=g(D))
    run('gql double slash, allowed doc',fl,'POST','/'+G,'fwd',body=g(Q))
    run('gql .json',fl,'POST',G+'.json','ref',body=g(Q))
    if fl=='ghes':
        run('no prefix PUT /repos merge',fl,'PUT','/repos/acme/widgets/pulls/7/merge','ref',body='{}')
        run('non-root POST /repos issues',fl,'POST','/repos/acme/widgets/issues','ref',body='{}')
        run('non-root DELETE /x',fl,'DELETE','/x','ref')
        run('non-root GET /x',fl,'GET','/x','fwd')
        run('non-root relative root write',fl,'POST','/rel/api/v3/repos/acme/widgets/issues','ref',body='{}')
        run('GET /api/v3 root',fl,'GET','/api/v3/','fwd')
    run('alias /repositories merge',fl,'PUT',A+'/pulls/7/merge','ref',body='{}')
    run('alias /repositories reviews',fl,'POST',A+'/pulls/7/reviews','ref',body='{}')
    run('alias upper /REPOSITORIES',fl,'PUT',P+'/REPOSITORIES/123/PULLS/7/MERGE','ref',body='{}')
    for rt in ['pulls/7/reviews','pulls/7/reviews/1','pulls/7/reviews/1/events','pulls/7/reviews/1/dismissals','pulls/7/merge','pulls/7/merge-async','pulls/7/update-branch','merges','merge-upstream','contents/f.txt','contents/a/b.txt','git/refs','git/refs/heads/main','git/commits','git/trees','git/tags','branches/main/rename','branches','branches/main','branches/a/b/c','actions/runs/1/pending_deployments','actions/runs/1/approve','actions/runs/1/deployment_protection_rule','actions/runs/1/force-cancel','environments/prod','environments/prod/secrets/S','environments/prod/variables','environments/prod/variables/V','environments/prod/deployment-branch-policies','environments/prod/deployment-branch-policies/1','environments/prod/deployment_protection_rules','environments/prod/deployment_protection_rules/1','branches/main/protection','branches/main/protection/required_status_checks','rulesets','rulesets/1','actions/permissions','actions/permissions/workflow','keys','keys/1']:
        for root,rn in((R,'repos'),(A,'repositories')):
            for m in('POST','PUT','PATCH','DELETE'):
                e='fwd' if (rt in('environments/prod/secrets/S','environments/prod/variables','environments/prod/variables/V') or (rt=='environments/prod' and m in('POST','PATCH'))) else 'ref'
                run(f'family {rn} {m} {rt}',fl,m,root+'/'+rt,e,body='{}' if m!='DELETE' else None,protos=H1)
            run(f'family {rn} GET {rt}',fl,'GET',root+'/'+rt,'fwd',protos=H1)
    run('credential POST /user/keys',fl,'POST',P+'/user/keys','ref',body='{}')
    run('credential DELETE /user/keys/1',fl,'DELETE',P+'/user/keys/1','ref')
    for p in('/user/gpg_keys','/user/gpg_keys/1','/user/ssh_signing_keys','/user/ssh_signing_keys/1'):
        run('credential POST '+p,fl,'POST',P+p,'ref',body='{}'); run('credential DELETE '+p,fl,'DELETE',P+p,'ref'); run('credential GET '+p,fl,'GET',P+p,'fwd')
    run('credential DELETE {repo}/keys/1',fl,'DELETE',R+'/keys/1','ref')
    run('credential POST /orgs/acme/rulesets',fl,'POST',P+'/orgs/acme/rulesets','ref',body='{}')
    run('credential PUT /orgs/acme/rulesets/1',fl,'PUT',P+'/orgs/acme/rulesets/1','ref',body='{}')
    for n in GHM:
        run('mutation '+n,fl,'POST',G,'ref',body=g('mutation{%s(input:{}){clientMutationId}}'%n),protos=H1)
        run('alias '+n,fl,'POST',G,'ref',body=g('mutation{createPullRequest:%s(input:{}){clientMutationId}}'%n),protos=H1)
    for n in('createPullRequest','addComment','updatePullRequest','closePullRequest','resolveReviewThread','requestReviews','createIssue'):
        run('mutation other '+n,fl,'POST',G,'fwd',body=g('mutation{%s(input:{}){clientMutationId}}'%n),protos=H1)
    run('suffix merge.json',fl,'PUT',R+'/pulls/7/merge.json','ref',body='{}')
    run('suffix merge.JSON',fl,'PUT',R+'/pulls/7/merge.JSON','ref',body='{}')
    run('suffix merge.xml',fl,'PUT',R+'/pulls/7/merge.xml','ref',body='{}')
    run('suffix merge.json.json',fl,'PUT',R+'/pulls/7/merge.json.json','ref',body='{}')
    run('suffix merge.json.foo',fl,'PUT',R+'/pulls/7/merge.json.foo','ref',body='{}')
    run('suffix reviews.x',fl,'POST',R+'/pulls/7/reviews.x','ref',body='{}')
    run('suffix mid-segment dot, repo name',fl,'POST',P+'/repos/acme/widgets.js/issues','fwd',body='{}')
    run('suffix mid-segment dot, denied route',fl,'PUT',P+'/repos/acme/widgets.js/pulls/7/merge','ref',body='{}')
    run('suffix comments.foo keeps suffix',fl,'POST',R+'/issues/7/comments.foo','fwd',body='{}')
    run('suffix dotfile final segment, GET',fl,'GET',R+'/labels/.hidden','fwd')
    run('suffix merge.foo',fl,'PUT',R+'/pulls/7/merge.foo','ref',body='{}')
    run('suffix GET labels.json keeps suffix',fl,'GET',R+'/labels.json','fwd')
    run('suffix POST issues.json keeps suffix',fl,'POST',R+'/issues.json','fwd',body='{}')
    run('enc branches/a/b/protection, two segments',fl,'PUT',R+'/branches/a/b/protection','ref',body='{}')
    run('enc branches/a%2Fb/protection',fl,'PUT',R+'/branches/a%2Fb/protection','ref',body='{}')
    run('enc branches/main%2Fprotection',fl,'PUT',R+'/branches/main%2Fprotection','ref',body='{}')
    run('enc labels/x%2Fpulls%2F7%2Fmerge',fl,'PUT',R+'/labels/x%2Fpulls%2F7%2Fmerge','fwd',body='{}')
    run('enc labels/good%20first',fl,'PATCH',R+'/labels/good%20first','fwd',body='{}')
    run('enc GET contents/a%20b.txt',fl,'GET',R+'/contents/a%20b.txt','fwd')
    run('enc GET contents/d/a%20b.txt',fl,'GET',R+'/contents/d/a%20b.txt','fwd')
    run('enc GET compare/main...a%2Fb',fl,'GET',R+'/compare/main...a%2Fb','fwd')
    run('enc GET git/ref/heads/a%2Fb',fl,'GET',R+'/git/ref/heads/a%2Fb','fwd')
    run('enc dot in declared',fl,'GET',R+'/contents/a%2F..%2Fb','ref')
    run('enc double in declared',fl,'GET',R+'/contents/a%252Fb','ref')
    run('enc NUL in declared',fl,'GET',R+'/contents/a%00b','ref')
    run('und pulls/7%2Fmerge',fl,'PUT',R+'/pulls/7%2Fmerge','ref',body='{}')
    run('und m%65rge',fl,'PUT',R+'/pulls/7/m%65rge','ref',body='{}')
    run('und GET iss%75es',fl,'GET',R+'/iss%75es','ref')
    run('und repos/acme%2Fwidgets',fl,'PUT',P+'/repos/acme%2Fwidgets/pulls/7/merge','ref',body='{}')
    run('und semicolon',fl,'PUT',R+'/pulls/7/merge;a=b','ref',body='{}')
    run('und backslash',fl,'PUT',R+'/pulls/7\\merge','ref',body='{}')
    run('und dot',fl,'PUT',R+'/pulls/7/./merge','ref',body='{}')
    run('und dotdot',fl,'PUT',R+'/pulls/7/x/../merge','ref',body='{}')
    run('canon double slash',fl,'PUT',R+'/pulls/7//merge','ref',body='{}')
    run('canon trailing slash',fl,'PUT',R+'/pulls/7/merge/','ref',body='{}')
    run('form POST ?x=1',fl,'POST',R+'/issues?x=1','ref',body='{}')
    run('form POST bare ?',fl,'POST',R+'/issues?','ref',body='{}')
    run('form DELETE ?y=1',fl,'DELETE',R+'/labels/x?y=1','ref')
    run('form urlencoded body',fl,'POST',R+'/issues','ref',body='title=t',ct='application/x-www-form-urlencoded')
    run('form text/plain body',fl,'POST',R+'/issues','ref',body='x',ct='text/plain')
    run('form octet-stream body',fl,'POST',R+'/issues','ref',body='x',ct='application/octet-stream')
    run('form no body',fl,'POST',R+'/issues/7/lock','fwd')
    run('form JSON charset',fl,'POST',R+'/issues','fwd',body='{"title":"t"}',hdr=('Content-Type: application/json; charset=utf-8',))
    run('form GET ?x=1',fl,'GET',R+'/issues?x=1','fwd')
def envelope(fl):
    host,P,G,_=F[fl]; R=P+'/repos/acme/widgets'
    r=lambda l,e,b,**k: run('env '+l,fl,'POST',G,e,body=b,**k)
    r('top array','ref','['+g(Q)+']'); r('top string','ref',J(Q)); r('top number','ref','7'); r('top null','ref','null')
    r('query number','ref','{"query":7}'); r('query object','ref','{"query":{"a":1}}'); r('query null','ref','{"query":null}'); r('query absent','ref','{"variables":{}}'); r('query empty','ref','{"query":""}')
    r('variables string','ref',J({'query':Q,'variables':'{"a":1}'})); r('variables array','ref',J({'query':Q,'variables':[1]})); r('variables number','ref',J({'query':Q,'variables':1})); r('variables true','ref',J({'query':Q,'variables':True}))
    r('variables null','fwd',J({'query':Q,'variables':None})); r('variables object','fwd',J({'query':Q,'variables':{'a':1}})); r('variables absent','fwd',J({'query':Q}))
    r('operationName number','ref',J({'query':Q,'operationName':1})); r('operationName null','fwd',J({'query':Q,'operationName':None})); r('operationName names none','ref',J({'query':Q,'operationName':'X'}))
    r('duplicate query','ref','{"query":"%s","query":"%s"}'%(Q,Q)); r('duplicate escaped key','ref','{"query":"%s","\\u0071uery":"%s"}'%(Q,Q)); r('duplicate variables','ref','{"query":"%s","variables":{},"variables":{}}'%Q)
    r('duplicate under variables','ref','{"query":"%s","variables":{"a":1,"a":2}}'%Q); r('duplicate deep','ref','{"query":"%s","variables":{"a":[{"b":{"c":1,"c":2}}]}}'%Q)
    r('case Query','ref','{"Query":"%s"}'%Q); r('case both','ref','{"query":"%s","QUERY":"%s"}'%(Q,Q)); r('case Variables','ref','{"query":"%s","Variables":{}}'%Q); r('case operationname','ref','{"query":"%s","operationname":"X"}'%Q)
    r('invalid UTF-8','ref',b'{"query":"'+Q.encode()+b'","variables":{"a":"\xff"}}'); r('lone high surrogate','ref','{"query":"%s","variables":{"a":"\\ud83d"}}'%Q); r('lone low surrogate','ref','{"query":"%s","variables":{"a":"\\ude00"}}'%Q); r('high then char','ref','{"query":"%s","variables":{"a":"\\ud83dx"}}'%Q)
    r('paired surrogate','fwd','{"query":"%s","variables":{"a":"\\ud83d\\ude00"}}'%Q); r('extensions key','fwd',J({'query':Q,'extensions':{'a':1}}))
    r('data after object','ref',g(Q)+'{}'); r('malformed','ref','{"query":')
    r('reencode spacing, escapes, number','fwd','{ "query" : "%s" , "variables" : { "a" : 1e2 , "b" : "\\u0041\\/x" , "c" : [ 1 , 2 ] , "big" : 123456789012345678901234567890 } }'%Q,protos=H1)
    r('two ops','ref',g('query A{viewer{login}} query B{viewer{login}}',operationName='A')); r('query and mutation','ref',g('query A{viewer{login}} mutation B{createIssue(input:{}){clientMutationId}}',operationName='A'))
    r('subscription','ref',g('subscription{x{id}}')); r('mutation root inline fragment','ref',g('mutation{... on Mutation{createIssue(input:{}){clientMutationId}}}')); r('mutation root spread','ref',g('mutation{...F} fragment F on Mutation{createIssue(input:{}){clientMutationId}}'))
    r('query with unused denied fragment','ref',g('query{viewer{login}} fragment F on Mutation{mergePullRequest(input:{}){clientMutationId}}'))
    r('query selecting a denied name','ref',g('query{mergePullRequest(input:{}){clientMutationId}}'))
    r('query nested denied name','ref',g('query{a{b{mergePullRequest}}}'))
    r('query spreading a Mutation fragment','ref',g('query{...F} fragment F on Mutation{addPullRequestReview(input:{}){clientMutationId}}'))
    r('query inline on Mutation','ref',g('query{... on Mutation{addPullRequestReview(input:{}){clientMutationId}}}'))
    r('query fragment on another type naming a denied field','ref',g('query{viewer{...F}} fragment F on User{mergePullRequest}'))
    r('query fragment on Query passes','fwd',g('query{...F} fragment F on Query{viewer{login}}'))
    r('fragment on mutation, lower case','ref',g('query{viewer{login}} fragment F on mutation{x}'))
    r('bare CR','ref',g('# x\rquery{viewer{login}}')); r('U+2028','ref',g('query{viewer{login}} # x\u2028')); r('BOM','ref',g('\ufeffquery{viewer{login}}')); r('syntax error','ref',g('query{')); r('wrong CT','ref',g(Q),ct='text/plain')
    run('env GET',fl,'GET',G+'?query=query%7Bviewer%7Blogin%7D%7D','ref'); run('env URL query',fl,'POST',G+'?x=1','ref',body=g(Q))
    run('env gzip',fl,'POST',G,'ref',body=g(Q),hdr=('Content-Encoding: gzip','Content-Type: application/json'))
    # override headers, on an allowed read and an allowed write
    for h in('X-HTTP-Method-Override: PUT','X-HTTP-Method: PUT','X-Method-Override: PUT','X-Original-URL: /x','X-Rewrite-URL: /x','X-Forwarded-Path: /x','X-Forwarded-Prefix: /x','Upgrade: websocket','Upgrade: h2c','HTTP2-Settings: AAMAAABkAAQAoAAAAAIAAAAA','Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ=='):
        run('ovr GET '+h,fl,'GET',R+'/pulls/7','ref',hdr=(h,),protos=H1 if h.startswith(('Upgrade','HTTP2')) else ('h1','h2'))
        run('ovr POST '+h,fl,'POST',R+'/issues','ref',hdr=(h,),body='{}',protos=H1 if h.startswith(('Upgrade','HTTP2')) else ('h1','h2'))
    run('ovr lower-case header',fl,'GET',R+'/pulls/7','ref',hdr=('x-http-method-override: PUT',))
    run('ovr empty value',fl,'GET',R+'/pulls/7','ref',hdr=('X-HTTP-Method-Override;',))
    run('ovr _method query',fl,'GET',R+'/pulls/7?_method=PUT','ref'); run('ovr %5Fmethod query',fl,'GET',R+'/pulls/7?%5Fmethod=PUT','ref')
    run('ovr _method form',fl,'POST',R+'/issues','ref',body='_method=PUT',ct='application/x-www-form-urlencoded')
    for h in('X-Forwarded-Uri: /x','X-Envoy-Original-Path: /x','X-Original-Method: PUT'):
        run('ovr added '+h,fl,'GET',R+'/pulls/7','ref',hdr=(h,)); run('ovr added POST '+h,fl,'POST',R+'/issues','ref',hdr=(h,),body='{}')
    for h in('Time-Zone: America/New_York','X-Future: 1'):
        run('hdr listed nowhere '+h,fl,'GET',R+'/pulls/7','fwd',hdr=(h,))
    run('hdr creds stripped',fl,'GET',R+'/pulls/7','fwd',hdr=('Cookie: c=AGENT-OWN','X-Api-Key: AGENT-OWN','Job-Token: AGENT-OWN','Deploy-Token: AGENT-OWN','Proxy-Authorization: Basic AGENT-OWN','Sudo: root'),protos=H1)
def generic():
    gen=lambda *a,**k: run(a[0],'gen',*a[1:],**k)
    gen('gen rule DELETE issue','DELETE','/rest/api/2/issue/1','ref'); gen('gen iss%75e','DELETE','/rest/api/2/iss%75e/1','ref'); gen('gen GET iss%75e','GET','/rest/api/2/iss%75e/1','ref'); gen('gen .json','DELETE','/rest/api/2/issue/1.json','ref'); gen('gen trailing slash','DELETE','/rest/api/2/issue/1/','ref'); gen('gen case','DELETE','/REST/api/2/ISSUE/1','ref')
    gen('gen GET issue','GET','/rest/api/2/issue/1','fwd')
    gen('gen POST final graphql, unlisted','POST','/foo/graphql','ref',body='{}'); gen('gen GET final graphql','GET','/foo/graphql','fwd'); gen('gen merge.foo stem','DELETE','/rest/api/2/issue/1.foo','ref')
    gen('gen /graphql2 no family','POST','/graphql2','fwd',body=g(Q)); gen('gen /api/foo no family','POST','/api/foo','fwd',body='{}')
    gen('gen graphql denied name','POST','/graphql','ref',body=g('mutation{addComment(input:{}){clientMutationId}}')); gen('gen graphql allowed','POST','/graphql','fwd',body=g(Q))
    for h in('X-Original-URL: /x','X-Rewrite-URL: /x','X-Forwarded-Path: /x','X-Forwarded-Prefix: /x','X-Forwarded-Uri: /x','X-Envoy-Original-Path: /x','X-Original-Method: PUT','X-HTTP-Method-Override: PUT','X-HTTP-Method: PUT','X-Method-Override: PUT'): gen('gen ovr '+h,'GET','/rest/api/2/project','ref',hdr=(h,))
    gen('gen _method query','GET','/rest/api/2/project?_method=DELETE','ref')
    gen('gen _method form','POST','/rest/api/2/project','ref',body='_method=DELETE',ct='application/x-www-form-urlencoded')
    gen('gen plain form forwarded','POST','/rest/api/2/project','fwd',body='a=b',ct='application/x-www-form-urlencoded')
    bd='--B\r\nContent-Disposition: form-data; name="_method"\r\n\r\nDELETE\r\n--B--\r\n'
    gen('gen _method in multipart streams','POST','/rest/api/2/issue/1/attachments','fwd',body=bd,ct='multipart/form-data; boundary=B')
    gen('gen attachment streams','POST','/rest/api/2/issue/1/attachments','fwd',body=b'\x00\x01'*600000,ct='application/octet-stream')
    gen('gen write with query','POST','/rest/api/2/project?x=1','fwd',body='{}')
    b=lambda *a,**k: run(a[0],'genb',*a[1:],**k)
    b('genb JSON pattern','POST','/x','ref',body='{"a":"hi\\n/approve"}'); b('genb form pattern','POST','/x','ref',body='a=%2Flgtm',ct='application/x-www-form-urlencoded'); b('genb query pattern','POST','/x?a=/approve','ref',body='{}')
    b('genb benign JSON re-encoded','POST','/x','fwd',body='{ "a" : "b" }',protos=H1); b('genb text/plain undecodable passes','POST','/x','fwd',body='/approve',ct='text/plain'); b('genb malformed JSON passes verbatim','POST','/x','fwd',body='{"a":"/approve"',protos=H1); b('genb gzip passes','POST','/x','fwd',body=b'\x1f\x8b',hdr=('Content-Encoding: gzip','Content-Type: application/json'))
    n=lambda *a,**k: run(a[0],'norule',*a[1:],**k)
    n('norule X-Original-URL','GET','/x','fwd',hdr=('X-Original-URL: /y',)); n('norule iss%75e','GET','/iss%75e','fwd'); n('norule X-HTTP-Method-Override','POST','/x','fwd',hdr=('X-HTTP-Method-Override: PUT',),body='{}')
def gitlab():
    P='/api/v4/projects/acme%2Fwidgets'; N='/api/v4/projects/1'; G='/api/graphql'
    r=lambda l,m,p,e,**k: run('gl '+l,'gl',m,p,e,**k)
    for root,rn in((P,'enc'),(N,'num')):
        for m,t in(('POST','merge_requests/7/approve'),('POST','merge_requests/7/unapprove'),('PUT','merge_requests/7/reset_approvals'),('PUT','merge_requests/7/merge'),('PUT','merge_requests/7/rebase'),('POST','merge_requests/7/cancel_merge_when_pipeline_succeeds'),('POST','merge_requests/7/approval_rules'),('PUT','merge_requests/7/approval_rules/1'),('POST','approval_rules'),('POST','approvals'),('POST','repository/commits'),('POST','repository/files/a.txt'),('PUT','repository/files/d%2Fa.txt'),('DELETE','repository/files/a.txt'),('POST','repository/branches'),('DELETE','repository/branches/a%2Fb'),('POST','protected_branches'),('DELETE','protected_branches/main'),('POST','deploy_keys'),('DELETE','deploy_keys/1'),('PUT','deploy_keys/1/enable')):
            r(f'family {rn} {m} {t}',m,root+'/'+t,'ref',body='{}' if m!='DELETE' else None)
    r('family POST /personal_access_tokens/self/rotate','POST','/api/v4/personal_access_tokens/self/rotate','ref',body='{}')
    r('family POST /user/keys','POST','/api/v4/user/keys','ref',body='{}')
    r('family DELETE /user/keys/1','DELETE','/api/v4/user/keys/1','ref')
    r('family POST /deploy_keys','POST','/api/v4/deploy_keys','ref',body='{}')
    for rn,root in(('enc',P),('num',N)):
        r(f'family {rn} POST access_tokens','POST',root+'/access_tokens','ref',body='{}'); r(f'family {rn} POST deploy_tokens','POST',root+'/deploy_tokens','ref',body='{}')
        r(f'family {rn} DELETE access_tokens/1, no tail as written','DELETE',root+'/access_tokens/1','fwd'); r(f'family {rn} DELETE deploy_tokens/1, no tail as written','DELETE',root+'/deploy_tokens/1','fwd')
    for n in('commitCreate','branchCreate','branchDelete'): r('mutation '+n,'POST',G,'ref',body=g('mutation{%s(input:{}){errors}}'%n)); r('alias '+n,'POST',G,'ref',body=g('mutation{x:%s(input:{}){errors}}'%n))
    r('non-root POST /x','POST','/x','ref',body='{}'); r('non-root relative root','POST','/gitlab/api/v4/projects/1/merge_requests/7/approve','ref'); r('non-root GET /x','GET','/x','fwd')
    r('approve.foo','POST',P+'/merge_requests/7/approve.foo','ref'); r('approve.json.json','POST',P+'/merge_requests/7/approve.json.json','ref')
    r('query naming mergeRequestAccept','POST',G,'ref',body=g('query{mergeRequestAccept}'))
    r('GET repository/files/a.txt','GET',P+'/repository/files/a.txt','fwd'); r('GET repository/branches/a%2Fb','GET',P+'/repository/branches/a%2Fb','fwd'); r('GET merge_requests/7/approvals','GET',P+'/merge_requests/7/approvals','fwd')
    r('unencoded project path, approve','POST','/api/v4/projects/acme/widgets/merge_requests/7/approve','fwd')
    r('full encoded with route words','POST',P+'%2Fmerge_requests%2F7%2Fapprove','ref'); r('project approve.json','POST',P+'/merge_requests/7/approve.json','ref'); r('project APPROVE case','POST',P+'/MERGE_REQUESTS/7/APPROVE','ref'); r('appr%6Fve','POST',P+'/merge_requests/7/appr%6Fve','ref')
    r('nested group passes','GET','/api/v4/projects/a%2Fb%2Fc/merge_requests','fwd'); r('lower %2f passes','GET','/api/v4/projects/acme%2fwidgets/merge_requests','fwd'); r('group passes','GET','/api/v4/groups/a%2Fb/projects','fwd')
    r('/api/v4/graphql','POST','/api/v4/graphql','ref',body=g(Q)); r('/api/foo','POST','/api/foo','ref',body='{}'); r('/graphql','POST','/graphql','ref',body=g(Q)); r('/api/v3','POST','/api/v3/projects','ref',body='{}')
    r('mutation accept','POST',G,'ref',body=g('mutation{mergeRequestAccept(input:{projectPath:"a/b",iid:"7",sha:"x"}){errors}}')); r('mutation alias','POST',G,'ref',body=g('mutation{x:mergeRequestAccept(input:{projectPath:"a/b",iid:"7",sha:"x"}){errors}}')); r('mutation other','POST',G,'fwd',body=g('mutation{mergeRequestSetDraft(input:{projectPath:"a/b",iid:"7",draft:false}){errors}}'))
    M=P+'/merge_requests'
    for k,b in(('top','{"merge_when_pipeline_succeeds":true}'),('auto_merge','{"auto_merge":true}'),('nested','{"a":{"b":[{"auto_merge":true}]}}'),('escaped key','{"auto\\u005fmerge":true}'),('case','{"AUTO_MERGE":true}')):
        r('bodykey '+k,'PUT',M+'/7','ref',body=b)
    r('bodykey graphql variable','POST',G,'ref',body=J({'query':'mutation($i:X!){mergeRequestUpdate(input:$i){errors}}','variables':{'i':{'auto_merge':True}}}))
    r('bodykey other key','PUT',M+'/7','fwd',body='{"title":"t"}')
    for t,e in(('/merge','ref'),('/approve','ref'),('/unapprove','ref'),('/rebase','ref'),('/submit_review','ref'),('/target_branch x','ref'),('/run_pipeline','ref'),('/lgtm','ref'),('x\n/merge','ref'),('x\r\n/merge','ref'),('x\r/merge','ref'),('x\u2028/merge','ref'),('x\u0085/merge','ref'),('x\x0b/merge','ref'),('  /MERGE','ref'),('\t/merge','ref'),('/merge the two lists','ref'),('```\n/merge\n```','ref'),('see /merge','fwd'),('/usr/bin/env','fwd'),('/merge_requests/1','fwd'),('/assign_reviewer @bob','fwd'),('\u00a0/merge','fwd'),('\u200b/merge','fwd')):
        r('text '+J(t),'POST',M,e,body=J({'title':'t','description':t}))
    r('text commit comment','POST',P+'/repository/commits/abc/comments','ref',body='{"note":"/merge"}')
    r('text issue note','POST',P+'/issues/1/notes','ref',body='{"body":"/merge"}')
    r('text snippet note','POST',P+'/snippets/1/notes','ref',body='{"body":"/approve"}')
    r('text array value','POST',M,'ref',body='{"a":["x","/merge"]}'); r('text key only','POST',M,'fwd',body='{"/merge":"x"}')
    r('text gql literal','POST',G,'ref',body=g('mutation{createNote(input:{noteableId:"x",body:"/approve"}){errors}}')); r('text gql block','POST',G,'ref',body=g('mutation{createNote(input:{noteableId:"x",body:"""\n/approve\n"""}){errors}}')); r('text gql variable','POST',G,'ref',body=J({'query':'mutation($i:X!){createNote(input:$i){errors}}','variables':{'i':{'body':'/merge'}}}))
    r('text gql default','POST',G,'ref',body=g('mutation($b:String="/merge"){createNote(input:{body:$b}){errors}}')); r('text gql directive','POST',G,'ref',body=g('mutation{createNote(input:{body:"x"})@d(a:"/merge"){errors}}')); r('text gql nested','POST',G,'ref',body=g('mutation{createNote(input:{body:"x"}){errors(a:"/merge")}}')); r('text gql unspread fragment','POST',G,'ref',body=g('mutation{createNote(input:{body:"x"}){errors}} fragment F on X{a(b:"/merge")}'))
    r('text gql query literal','POST',G,'fwd',body=g('query{project(fullPath:"/merge"){id}}'))
    r('form query string','POST',M+'?description=/merge','ref',body='{}'); r('form bare query','POST',M+'?title=t','ref',body='{}'); r('form urlencoded','POST',M,'ref',body='title=t',ct='application/x-www-form-urlencoded'); r('form text/plain','POST',M,'ref',body='x',ct='text/plain'); r('form gzip','POST',M,'ref',body=b'\x1f\x8b',hdr=('Content-Encoding: gzip','Content-Type: application/json')); r('form malformed JSON','POST',M,'ref',body='{"a":')
    r('json duplicate','POST',M,'ref',body='{"a":1,"a":2}'); r('json case variant','POST',M,'ref',body='{"Title":"a","title":"b"}'); r('json lone surrogate','POST',M,'ref',body='{"a":"\\ud83d"}'); r('json invalid UTF-8','POST',M,'ref',body=b'{"a":"\xff"}')
    r('json re-encoded','POST',M,'fwd',body='{ "title" : "\\u0041" , "n" : 1e2 }',protos=H1)
    r('ovr Sudo stripped, Authorization stripped','GET',M,'fwd',hdr=('Sudo: root','Authorization: Bearer AGENT-OWN'),protos=H1)
    r('ovr X-Original-URL','GET',M,'ref',hdr=('X-Original-URL: /x',)); r('ovr method','POST',M,'ref',body='{}',hdr=('X-HTTP-Method-Override: PUT',))
def nest(n): return 'a{'*(n-1)+'a'+'}'*(n-1)
def bounds(fl):
    host,P,G,_=F[fl]
    r=lambda l,e,b,**k: run('bound '+l,fl,'POST',G,e,body=b,protos=H1,**k)
    for n in(4090,4091,4092,4093,4094,4095,4096,4097,4098):
        r(f'query fields {n} (tokens {n+3})','fwd' if n+3<=4096 else 'ref',g('query{'+' '.join('a'*1 for _ in range(n))+'}'))
    for n in(31,32,33):
        r(f'depth {n}','fwd' if n<=32 else 'ref',g('query{'+nest(n)+'}'))
    r('fragment chain, 20+20 levels','ref',g('query{'+'a{'*19+'...F'+'}'*19+'} fragment F on T{'+'b{'*19+'b'+'}'*19+'}'))
    r('fragment chain, 15+15 levels','fwd',g('query{'+'a{'*14+'...F'+'}'*14+'} fragment F on T{'+'b{'*14+'b'+'}'*14+'}'))
    r('fragment cycle','ref',g('query{...F} fragment F on T{a{...F}}'))
    for n in(32,33): r(f'value list depth {n}','fwd' if n<=32 else 'ref',g('query{a(b:'+'['*n+'1'+']'*n+')}'))
    def jn(n): # the envelope is depth 1, variables depth 2
        return '{"query":"%s","variables":%s}'%(Q,'{"a":'*(n-2)+'1'+'}'*(n-2))
    for n in(31,32,33): r(f'JSON depth {n}','fwd' if n<=32 else 'ref',jn(n))
    r('JSON depth 40000','ref','{"query":"%s","variables":%s}'%(Q,'['*40000+']'*40000))
    for n in(16,17): r(f'mutation root fields {n}','fwd' if n<=16 else 'ref',g('mutation{'+' '.join(f'a{i}:addComment(input:{{}}){{clientMutationId}}' for i in range(n))+'}'))
    r('query root fields 200','fwd',g('query{'+' '.join(f'a{i}:viewer{{login}}' for i in range(200))+'}'))
    big=lambda n: '{"query":"%s","variables":{"a":"%s"}}'%(Q,'x'*(n-len('{"query":"%s","variables":{"a":""}}'%Q)))
    r('body 524288','fwd',big(524288)); r('body 524289','ref',big(524289))
    r('body 524289 chunked','ref',big(524289),hdr=('Transfer-Encoding: chunked','Content-Type: application/json'))
    pad=lambda n: 'query{a}#'+'x'*(n-len('query{a}#'))
    r('document 65536','fwd',g(pad(65536))); r('document 65537','ref',g(pad(65537)))
    r('worst tokens, 4,096 of them','fwd',g('query{'+' '.join('a' for _ in range(4093))+'}'))
for gp in groups:
    if gp.startswith('paths:'): paths(gp[6:])
    elif gp.startswith('env:'): envelope(gp[4:])
    elif gp=='generic': generic()
    elif gp=='gitlab': gitlab()
    elif gp.startswith('bounds:'): bounds(gp[7:])
steps.close()
