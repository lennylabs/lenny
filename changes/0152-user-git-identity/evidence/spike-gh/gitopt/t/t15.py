# t15.py <policy-label>: the review rows. Each runs over HTTP/1.1 and HTTP/2 through the egress endpoint with a hard curl timeout; netagent's decision and the origin's record join by time.
import json,subprocess,sys,time,datetime,os
pol=sys.argv[1]; steps=open(f'/out/review/{pol}.steps','w')
def now(): return datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%S.%f')+'000Z'
F={'ghes':('ghes.example.test','/api/v3','/api/graphql','Authorization: token lenny-injected'),'com':('api.github.com','','/graphql','Authorization: token lenny-injected'),'ghe':('api.acme.ghe.com','','/graphql','Authorization: token lenny-injected'),'gl':('gitlab.example.test','/api/v4','/api/graphql','Private-Token: lenny-injected'),'generic':('api.example.test','','/graphql','Authorization: Bearer lenny-injected')}
J=lambda o:json.dumps(o)
D='mutation{addPullRequestReview(input:{pullRequestId:"P",event:APPROVE}){clientMutationId}}'
def run(label,fl,m,path,hdr=(),body=None):
    host,_,_,auth=F[fl]
    for proto,flag in (('h1','--http1.1'),('h2','--http2')):
        a=['curl','-sS','-m','10','--path-as-is','-o','/tmp/b','-w','%{http_code}','--cacert','/s/bundle.pem','-x','127.0.0.1:3128',flag,'-X',m,'-H',auth]
        for h in hdr: a+=['-H',h]
        if body is not None:
            open('/tmp/bd','wb').write(body if isinstance(body,bytes) else body.encode()); a+=['--data-binary','@/tmp/bd']
            if not any(h.lower().startswith('content-type') for h in hdr): a+=['-H','Content-Type: application/json']
        a.append(f'https://{host}{path}')
        s=now()
        try:
            r=subprocess.run(a,capture_output=True,text=True,timeout=14); code=r.stdout.strip(); err=r.stderr.strip()[:80]
        except subprocess.TimeoutExpired: code='timeout'; err=''
        time.sleep(0.03)
        e=now()
        try: b=open('/tmp/b',errors='replace').read(160)
        except: b=''
        steps.write(J({'label':label,'proto':proto,'fl':fl,'method':m,'path':path,'http':code,'err':err,'resp':b[:120],'start':s,'end':e})+'\n'); steps.flush()
for fl in ('ghes','com','ghe'):
    host,P,G,_=F[fl]; R=P+'/repos/a/b'
    jb=J({'event':'APPROVE'})
    g=J({'query':D})
    # 1 path spellings
    run('path r%65views',fl,'POST',R+'/pulls/1/r%65views',body=jb)
    run('path m%65rge',fl,'PUT',R+'/pulls/1/m%65rge',body='{}')
    run('path reviews/',fl,'POST',R+'/pulls/1/reviews/',body=jb)
    run('path merge/',fl,'PUT',R+'/pulls/1/merge/',body='{}')
    run('path reviews.json',fl,'POST',R+'/pulls/1/reviews.json',body=jb)
    run('path merge.json',fl,'PUT',R+'/pulls/1/merge.json',body='{}')
    run('path git/refs.json',fl,'POST',R+'/git/refs.json',body='{}')
    run('path %2F in merge',fl,'PUT',R+'/pulls/1%2Fmerge',body='{}')
    run('path double-encoded',fl,'PUT',R+'/pulls/1/m%2565rge',body='{}')
    run('gql graphq%6C',fl,'POST',G[:-1]+'%6C',body=g)
    run('gql trailing slash',fl,'POST',G+'/',body=g)
    run('gql .json',fl,'POST',G+'.json',body=g)
    run('gql double slash',fl,'POST','/'+G,body=g)
    # 3 coverage
    run('coverage gql denied',fl,'POST',G,body=g)
    run('coverage gql allowed',fl,'POST',G,body=J({'query':'query{viewer{login}}'}))
    run('coverage rest merge',fl,'PUT',R+'/pulls/1/merge',body='{}')
    run('coverage rest review',fl,'POST',R+'/pulls/1/reviews',body=jb)
    run('coverage rest ref',fl,'POST',R+'/git/refs',body='{}')
    run('unmatched /graphql2',fl,'POST','/graphql2',body=g)
    run('unmatched /graphql/foo',fl,'POST','/graphql/foo',body=g)
    run('unmatched /graphqlx',fl,'POST','/graphqlx',body=g)
    run('unmatched /api/graphqlx',fl,'POST','/api/graphqlx',body=g)
    run('unmatched /api/foo',fl,'POST','/api/foo',body='{}')
    run('unmatched /api/v3/graphql',fl,'POST','/api/v3/graphql',body=g)
    run('unmatched /api/graphql on this host',fl,'POST','/api/graphql',body=g)
    run('unmatched /graphql on this host',fl,'POST','/graphql',body=g)
    run('unmatched /api/v9 merge',fl,'PUT','/api/v9/repos/a/b/pulls/1/merge',body='{}')
    # legit encoded paths
    run('legit label with a space',fl,'PATCH',R+'/labels/good%20first',body='{}')
    run('legit contents read',fl,'GET',R+'/contents/dir%2Ffile.txt')
    run('legit branch with a slash',fl,'GET',R+'/branches/feat%2Fx')
    run('legit tag release',fl,'GET',R+'/releases/tags/v1%2F2')
    run('legit undeclared escape',fl,'GET',R+'/issues/1/comments%20')
    # 5 overrides
    for h,v in (('X-Original-URL',R+'/pulls/1/merge'),('X-Rewrite-URL',R+'/pulls/1/merge'),('X-HTTP-Method-Override','PUT'),('X-HTTP-Method-Override','GET')):
        run(f'override {h}: {v[:12]} on POST issue',fl,'POST',R+'/issues',hdr=[h+': '+v],body='{}')
    run('override method on POST merge',fl,'POST',R+'/pulls/1/merge',hdr=['X-HTTP-Method-Override: PUT'],body='{}')
    run('Sudo on a GitHub host',fl,'GET',R+'/pulls/1',hdr=['Sudo: root'])
# GitLab
P='/api/v4'; M=P+'/projects/1/merge_requests/7'; Me=P+'/projects/acme%2Fwidgets/merge_requests/7'; G='/api/graphql'
gm=J({'query':'mutation{mergeRequestAccept(input:{projectPath:"a/b",iid:"7",sha:"x"}){errors}}'})
for name,m,p in (('appr%6Fve','POST',M+'/appr%6Fve'),('approve.json','POST',M+'/approve.json'),('approve/','POST',M+'/approve/'),('unapprove.json','POST',M+'/unapprove.json'),('merge.json','PUT',M+'/merge.json'),('m%65rge','PUT',M+'/m%65rge'),('merge/','PUT',M+'/merge/'),('rebase.json','PUT',M+'/rebase.json'),('encoded project, appr%6Fve','POST',Me+'/appr%6Fve'),('encoded project, approve','POST',Me+'/approve'),('full-encoded tail','POST',P+'/projects/acme%2Fwidgets%2Fmerge_requests%2F7%2Fapprove')):
    run('gl path '+name,'gl',m,p,body='{}')
run('gl gql graphq%6C','gl','POST',G[:-1]+'%6C',body=gm)
run('gl gql trailing slash','gl','POST',G+'/',body=gm)
run('gl gql denied','gl','POST',G,body=gm)
run('gl unmatched /api/v3 approve','gl','POST','/api/v3/projects/1/merge_requests/7/approve',body='{}')
run('gl unmatched /api/foo','gl','POST','/api/foo',body='{}')
run('gl unmatched /graphql','gl','POST','/graphql',body=gm)
run('gl legit project id','gl','GET',P+'/projects/acme%2Fwidgets')
run('gl legit nested group','gl','GET',P+'/projects/acme%2Fsub%2Fwidgets/merge_requests/7')
run('gl legit lower-case %2f','gl','GET',P+'/projects/acme%2fwidgets/merge_requests/7')
run('gl legit file path','gl','GET',P+'/projects/1/repository/files/dir%2Ffile.txt?ref=main')
run('gl legit branch','gl','GET',P+'/projects/1/repository/branches/feat%2Fx')
run('gl legit undeclared escape','gl','GET',P+'/projects/1/issues/1%20')
for h,v in (('X-Original-URL',M+'/approve'),('X-Rewrite-URL',M+'/approve'),('X-HTTP-Method-Override','PUT'),('Sudo','root'),('Sudo','1')):
    run(f'gl override {h}: {v[:10]} on GET','gl','GET',P+'/projects/1',hdr=[h+': '+v])
run('gl Authorization beside the binding','gl','GET',P+'/projects/1',hdr=['Authorization: Bearer AGENT-OWN'])
run('gl override PUT on POST merge','gl','POST',M+'/merge',hdr=['X-HTTP-Method-Override: PUT'],body='{}')
# Generic bound host with no preset
for h,v in (('X-Original-URL','/x'),('X-Rewrite-URL','/x'),('X-HTTP-Method-Override','PUT'),('Sudo','root')):
    run(f'generic override {h}','generic','GET','/x',hdr=[h+': '+v])
# 2 inline strings, on GitLab and on GitHub
T=[('createNote newline escape','mutation{createNote(input:{noteableId:"X",body:"ok\\n/merge"}){clientMutationId}}',None),
 ('createNote CR escape','mutation{createNote(input:{noteableId:"X",body:"ok\\r/merge"}){clientMutationId}}',None),
 ('addComment newline approve','mutation{addComment(input:{subjectId:"X",body:"\\n/approve"}){clientMutationId}}',None),
 ('addComment unicode slash','mutation{addComment(input:{subjectId:"X",body:"ok\\n\\u002fapprove"}){clientMutationId}}',None),
 ('createNote unicode newline and slash','mutation{createNote(input:{noteableId:"X",body:"ok\\u000a\\u002fmerge"}){clientMutationId}}',None),
 ('createNote leading','mutation{createNote(input:{noteableId:"X",body:"/merge"}){clientMutationId}}',None),
 ('createNote block string','mutation{createNote(input:{noteableId:"X",body:"""ok\n/merge"""}){clientMutationId}}',None),
 ('createNote block leading','mutation{createNote(input:{noteableId:"X",body:"""\n  /approve\n"""}){clientMutationId}}',None),
 ('createNote list argument','mutation{createNote(input:{noteableId:"X",bodies:["a","b\\n/merge"]}){clientMutationId}}',None),
 ('createNote variable','mutation($b:String!){createNote(input:{noteableId:"X",body:$b}){clientMutationId}}',{'b':'ok\n/merge'}),
 ('createNote nested variable','mutation($i:CreateNoteInput!){createNote(input:$i){clientMutationId}}',{'i':{'noteableId':'X','body':'ok\n/approve'}}),
 ('createNote variable default','mutation($b:String="ok\\n/merge"){createNote(input:{noteableId:"X",body:$b}){clientMutationId}}',None),
 ('createNote fragment','mutation{...F} fragment F on Mutation{createNote(input:{noteableId:"X",body:"ok\\n/merge"}){clientMutationId}}',None),
 ('createNote alias','mutation{x:createNote(input:{noteableId:"X",body:"ok\\n/merge"}){clientMutationId}}',None),
 ('createNote directive argument','mutation{createNote(input:{noteableId:"X",body:"ok"}) @x(y:"a\\n/merge"){clientMutationId}}',None),
 ('createNote nested field argument','mutation{createNote(input:{noteableId:"X",body:"ok"}){note(q:"a\\n/merge"){id}}}',None),
 ('createNote benign','mutation{createNote(input:{noteableId:"X",body:"see /merge in the docs"}){clientMutationId}}',None)]
for label,doc,v in T:
    b={'query':doc}
    if v: b['variables']=v
    for fl,G in (('gl','/api/graphql'),('com','/graphql'),('ghes','/api/graphql')):
        run(f'text {label}',fl,'POST',G,body=J(b))
steps.close()
