import re,subprocess,sys,json
repo=sys.argv[1]
def g(*a): return subprocess.run(['git','-C',repo]+list(a),capture_output=True,text=True,errors='replace').stdout
tags=[l.split() for l in g('for-each-ref','--sort=creatordate','--format=%(creatordate:short) %(refname:short)','refs/tags').splitlines()]
tags=[(d,t) for d,t in tags if re.fullmatch(r'v2\.\d+\.\d+',t) and d>='2024-10-01']
pat=re.compile(r'\b([a-z][A-Za-z0-9]+)\(\s*input:\s*\$')
res={}
for d,t in tags:
    files=[f for f in g('ls-tree','-r','--name-only',t).splitlines() if f.endswith('.go') and not f.endswith('_test.go') and (f.startswith('api/') or f.startswith('pkg/') or f.startswith('internal/'))]
    s=set()
    out=g('grep','-h','-E',r'\(\s*input:\s*\$',t,'--','*.go',':!*_test.go')
    for m in pat.finditer(out): s.add(m.group(1))
    res[t]=(d,sorted(s))
json.dump(res,open(sys.argv[2],'w'),indent=0)
prev=None
for t,(d,s) in res.items():
    if prev is not None:
        a=set(s)-prev; r=prev-set(s)
        if a or r: print(d,t,'+',sorted(a),'-',sorted(r))
    prev=set(s)
print('releases',len(res),'latest count',len(prev))
print('\n'.join(prev and sorted(prev)))
