import json
def first(f,pred):
    for l in open(f):
        r=json.loads(l)
        if pred(r): return r['body']
c='/o/raw/capture-ghes/'
isop=lambda n:(lambda r:any(o.get('name')==n for g in r.get('gql',[]) for o in g.get('ops',[])))
B={'create':first(c+'pr-create-basic.jsonl',isop('PullRequestCreate')),'largest':first(c+'pr-view-json-all.jsonl',isop('PullRequestByNumber')),'status':first(c+'pr-status.jsonl',isop('PullRequestStatus'))}
q="mutation PullRequestCreate($input: CreatePullRequestInput!){createPullRequest(input:$input){pullRequest{id url}}}"
def mk(body): return json.dumps({'query':q,'variables':{'input':{'repositoryId':'R','title':'t','body':body,'baseRefName':'main','headRefName':'feature'}}})
B['body65k']=mk('x'*65536)
B['bound258k']=mk('<'*43000).replace('<','\\u003c') if False else None
B['bound258k']=json.dumps({'query':q,'variables':{'input':{'repositoryId':'R','body':'\u00e9'*43000*1}}},ensure_ascii=True)
d='query{viewer{login}}'
base=json.dumps({'query':'#\n'+d})
B['comment256k']=json.dumps({'query':'#'+'x'*(262144-len(base))+'\n'+d})
tok='{viewer{'+' a'*130000+'}}'
B['tokens256k']=json.dumps({'query':tok})
for k,v in B.items():
    open(f'/o/run/trust/body-{k}.json','w').write(v); print(k,len(v))
