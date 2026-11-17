import json,sys
ne,no=int(sys.argv[1]),int(sys.argv[2])
print('CONNECTs at the egress stand-in:')
for l in open('/log/egress.jsonl').readlines()[ne:]:
    x=json.loads(l); print('  ',x.get('target'),'code',x.get('code'),'marker',x.get('marker'),'assert',repr(x.get('assert')))
g,a=set(),set()
for l in open('/log/origin.jsonl').readlines()[no:]:
    x=json.loads(l)
    if x.get('git'): g.add(x.get('remote'))
    elif 'method' in x and x.get('remote'): a.add(x.get('remote'))
print('upstream connections at the stub: git',sorted(g),'api',sorted(a),'shared',sorted(g&a))
