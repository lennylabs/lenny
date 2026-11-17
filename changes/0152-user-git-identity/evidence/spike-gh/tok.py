import json,glob,re
T=re.compile(r'[A-Za-z_][A-Za-z0-9_]*|\.\.\.|[{}():,$!@=\[\]]|"(?:[^"\\]|\\.)*"|-?\d+')
m=0;w=None
for f in glob.glob('/o/raw/capture-gh*/*.jsonl'):
    for l in open(f):
        for g in json.loads(l).get('gql',[]):
            n=len(T.findall(g['doc']))
            if n>m: m,w=n,(f.split('/')[-1],(g.get('ops') or [{}])[0].get('name'))
print('largest gh document by token count (regex count):',m,w)
