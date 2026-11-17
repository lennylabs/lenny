# analyze.py <dir>: one line per case with the routes and top-level fields, plus a union of headers, sizes, and compression.
import json,sys,glob,os,re,collections
d=sys.argv[1]
hdrs=collections.Counter(); biggest=[]; ce=collections.Counter(); protos=collections.Counter(); bodies=collections.Counter()
table=[]
for f in sorted(glob.glob(d+'/*.jsonl'),key=os.path.getmtime):
    case=os.path.basename(f)[:-6]; seq=[]
    for line in open(f):
        r=json.loads(line); protos[r['proto']]+=1
        for k in r['headers']: hdrs[k]+=1
        ce[','.join(r['headers'].get('Content-Encoding',['(none)']))]+=1
        bodies[r.get('decodedFrom') or 'plain']+=1
        uri=r['uri']
        if r.get('gql'):
            for g in r['gql']:
                for o in g.get('ops',[]):
                    fs=sorted(set(o['fields']))
                    seq.append(f"GQL {o['type']} {o.get('name','') or '(anon)'}: {','.join(fs)}")
                biggest.append((len(g['doc']),r['bodyLen'],case,(g.get('ops') or [{}])[0].get('name','')))
        else:
            seq.append(f"{r['method']} {uri} [{r['status']}]")
    rc=open(f[:-6]+'.out').read().strip().splitlines()[-1] if os.path.exists(f[:-6]+'.out') else ''
    table.append((case,rc,seq))
for c,rc,seq in table:
    print(f"### {c} ({rc})")
    for s in seq: print("  "+s)
print("\n=== headers at the stub (count)"); 
for k,v in hdrs.most_common(): print(f"  {k}: {v}")
print("=== Content-Encoding of requests:",dict(ce)); print("=== protocols at the stub:",dict(protos))
print("=== largest GraphQL documents (doc bytes, body bytes, case, op)")
for x in sorted(biggest,reverse=True)[:8]: print("  ",x)
muts=collections.defaultdict(set); qs=collections.defaultdict(set); rest=collections.defaultdict(set)
for c,rc,seq in table:
    for s in seq:
        m=re.match(r'GQL (\w+) (\S+): (.*)',s)
        if m:
            for x in m.group(3).split(','):
                (muts if m.group(1)=='mutation' else qs)[x].add(c)
        else:
            rest[re.sub(r' \[\d+\]$','',s)].add(c)
print("=== union of mutation fields"); [print(' ',k,sorted(v)) for k,v in sorted(muts.items())]
print("=== union of query fields"); [print(' ',k,len(v),'cases') for k,v in sorted(qs.items())]
print("=== union of REST routes"); [print(' ',k,sorted(v)) for k,v in sorted(rest.items())]
