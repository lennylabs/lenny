# Counts GraphQL mutations in octokit/graphql-schema history and in GitHub's current public schema.
import re,subprocess,sys
repo,cur=sys.argv[1],sys.argv[2]
def g(*a): return subprocess.run(['git','-C',repo]+list(a),capture_output=True,text=True,errors='replace').stdout
def muts(t):
    t=re.sub(r'"""[\s\S]*?"""','',t)
    m=re.search(r'^type Mutation\b[^{]*\{',t,re.M)
    i=m.end(); j=t.index('\n}',i)
    return set(re.findall(r'^\s{2}([a-zA-Z0-9_]+)\s*(?:\(|:)',t[i:j],re.M))
pts=[]
for s in ['2021-01-01','2022-01-01','2023-01-01','2024-01-01','2025-01-01']:
    h=g('rev-list','-1','--before='+s,'HEAD','--','schema.graphql').strip()
    pts.append((s,muts(g('show',h+':schema.graphql'))))
h=g('rev-list','-1','HEAD','--','schema.graphql').strip()
pts.append((g('log','-1','--format=%ad','--date=short',h).strip(),muts(g('show',h+':schema.graphql'))))
pts.append(('docs.github.com/public/fpt/schema.docs.graphql, fetched 2026-11-03',muts(open(cur,errors='replace').read())))
fam=re.compile(r'Review|Merge|Enqueue|Dequeue|Approve|Deployment|ProtectionRule|Ruleset|Ref$|Refs|CommitOnBranch|Branch|Queue|Bypass|Revert',re.I)
prev=None
for n,s in pts:
    print(n,'total',len(s))
    if prev is not None:
        a=sorted(s-prev);r=sorted(prev-s)
        print('  added',len(a),'removed',len(r))
        print('  added, name matches review, merge, or ref family:',[x for x in a if fam.search(x)])
    prev=s
