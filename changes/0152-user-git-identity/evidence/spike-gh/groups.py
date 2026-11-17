import re,collections
for fl in ('ghes','ghcom'):
    g=collections.Counter(); v=collections.Counter(); s=collections.Counter(); bad=[]
    for l in open(f'/o/raw/attack-{fl}.txt'):
        m=re.match(r'(\S+)\s+(\S+)\s+(.{1,64}?)\s+step=(\d+) exp=(\w+)\s+status=(\d+)\s+filter=(\S*)\s+stub=(\d+) cred=(\w+)',l)
        if not m: continue
        gr=m.group(3).split(' / ')[0]; g[(gr,m.group(5),m.group(1))]+=1; v[m.group(1)]+=1
        s[(m.group(5),m.group(1),'status0' if m.group(6)=='0' else 'filter' if m.group(7)=='refused' else 'http'+m.group(6)[0])]+=1
    print('==',fl,dict(v))
    for k,c in sorted(g.items()): print(' ',k,c)
    for k,c in sorted(s.items()): print('  how',k,c)
