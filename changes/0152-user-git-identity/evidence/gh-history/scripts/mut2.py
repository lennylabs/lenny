import re,subprocess,sys,json,tarfile,io
repo=sys.argv[1]
def g(*a): return subprocess.run(['git','-C',repo]+list(a),capture_output=True,text=True,errors='replace').stdout
tags=[l.split() for l in g('for-each-ref','--sort=creatordate','--format=%(creatordate:short) %(refname:short)','refs/tags').splitlines()]
tags=[(d,t) for d,t in tags if re.fullmatch(r'v2\.\d+\.\d+',t) and d>='2024-10-01']
p1=re.compile(r'\b([a-z][A-Za-z0-9]+)\s*\(\s*input\s*:\s*\$')
p2=re.compile(r'\bmutation\s+[A-Za-z0-9_]*\s*(?:\([^)]*\))?\s*\{\s*(?:[a-zA-Z0-9_]+\s*:\s*)?([a-z][A-Za-z0-9]+)\s*\(')
p3=re.compile(r'graphql:\\?"([a-z][A-Za-z0-9]+)\(')
res={}
for d,t in tags:
    raw=subprocess.run(['git','-C',repo,'archive','--format=tar',t,'api','pkg','internal','context','git'],capture_output=True).stdout
    s={}
    with tarfile.open(fileobj=io.BytesIO(raw)) as tf:
        for m in tf:
            if not m.isfile() or not m.name.endswith('.go') or m.name.endswith('_test.go'): continue
            txt=tf.extractfile(m).read().decode('utf8','replace')
            for x in p1.findall(txt)+p2.findall(txt):
                s.setdefault(x,set()).add(m.name)
            for mm in re.finditer(r'type\s+\w*\s*struct|var\s+\w+\s+struct',txt): pass
            if 'Mutate(' in txt:
                for x in p3.findall(txt): s.setdefault(x,set()).add(m.name)
    res[t]=(d,{k:sorted(v) for k,v in s.items()})
json.dump(res,open(sys.argv[2],'w'))
prev=None;ch=0
for t,(d,s) in res.items():
    c=set(s)
    if prev is not None and (c-prev or prev-c):
        ch+=1; print(d,t,'+',sorted(c-prev),'-',sorted(prev-c))
    prev=c
print('releases',len(res),'releases changing the set',ch,'latest count',len(prev))
print(' '.join(sorted(prev)))
