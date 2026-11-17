# cmpcap.py: (1) pr create with the filter against without it, request by request. (2) every mutation and non-GET that reached the stub in the filtered runs.
import json,glob,os,sys
def load(f): return [json.loads(l) for l in open(f)]
def norm(r,ignore_hdr=('Content-Length',)):
    return (r['method'],r['uri'],sorted((k,tuple(v)) for k,v in r['headers'].items() if k not in ignore_hdr),r.get('body',''))
for fl in ('ghes','ghcom'):
    same=diff=0; 
    for f in sorted(glob.glob(f'/o/raw/{sys.argv[1]}-{fl}/pr-create-*.jsonl')):
        c=os.path.basename(f); a=load(f'/o/raw/capture-{fl}/{c}'); b=load(f)
        key=lambda r:(r['method'],r['uri'],r.get('body',''))
        a=sorted(a,key=key); b=sorted(b,key=key)
        if [norm(x) for x in a]==[norm(x) for x in b]: same+=1; print(' same ',fl,c,len(b),'requests; real token at the stub:',all('REAL' in x['headers'].get('Authorization',[''])[0] for x in b),'; placeholder at the stub:',any('lenny-injected' in json.dumps(x['headers']) for x in b))
        else:
            diff+=1; print(' DIFF ',fl,c,len(a),len(b))
            for x,y in zip(a,b):
                if norm(x)!=norm(y): print('   a',str(norm(x))[:400]); print('   b',str(norm(y))[:400]); break
    print(fl,'pr create identical:',same,'different:',diff)
for d in sys.argv[1:]:
  for fl in ('ghes','ghcom'):
    seen={}
    for f in sorted(glob.glob(f'/o/raw/{d}-{fl}/*.jsonl')):
        c=os.path.basename(f)[:-6]
        for r in load(f):
            if r.get('gql'):
                for g in r['gql']:
                    for o in g.get('ops',[]):
                        if o['type']!='query':
                            for x in o['fields']: seen.setdefault('GQL '+o['type']+' '+x,set()).add(c)
            elif r['method']!='GET': seen.setdefault(r['method']+' '+r['uri'],set()).add(c)
    print(f'== {d}-{fl}: writes that reached the stub')
    for k,v in sorted(seen.items()): print('  ',k,sorted(v))
