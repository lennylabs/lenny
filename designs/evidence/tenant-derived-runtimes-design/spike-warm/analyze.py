import json,sys,collections
rows=[json.loads(l) for l in open(sys.argv[1])]
agg=collections.OrderedDict()
for r in rows:
    ua=r.get('ua','')
    if 'phase=' not in ua: continue
    ph=ua.split('phase=')[1]
    a=agg.setdefault(ph,{'req':0,'packKB':0,'haves':0,'wants':0,'proto':set(),'status':set(),'cmd':set()})
    a['req']+=1; a['haves']+=r['haves']; a['wants']+=r['wants']; a['proto'].add(r.get('gitProtocol') or 'v0'); a['status'].add(r['status'])
    if r['path'].endswith('git-upload-pack'): a['packKB']+=r['respBytes']/1024
    if r.get('command'): a['cmd'].add(r['command'])
hosts=[]
for ph in agg:
    h=ph.rsplit('-',1)[0]
    if h not in hosts: hosts.append(h)
print(f"{'host':10} {'phase':9} {'reqs':>4} {'proto':9} {'status':9} {'wants':>5} {'haves':>5} {'packKB':>8}")
for h in hosts:
    for p in ['shallow12','fill','delta','base','noref','shallow1']:
        a=agg.get(f'{h}-{p}')
        if not a: continue
        print(f"{h:10} {p:9} {a['req']:>4} {','.join(sorted(a['proto'])):9} {','.join(map(str,sorted(a['status']))):9} {a['wants']:>5} {a['haves']:>5} {a['packKB']:>8.1f}")
