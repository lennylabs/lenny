import json,sys
na=[json.loads(l) for l in open('/log/na.jsonl')]; og=[json.loads(l) for l in open('/log/origin.jsonl')]
for s in map(json.loads,open(sys.argv[1])):
    a,b=s['start'],s['end']; c={}
    for x in na:
        if a<=x.get('t','')<=b and x.get('gitOperation') and (x.get('op','').startswith('lfs')):
            k=x['op']+':'+x['decision']; c[k]=c.get(k,0)+1
    tr={}
    for x in og:
        if a<=x.get('t','')<=b and (x.get('glTransfer') or x.get('lfsAuthObject')):
            k=x.get('host','')[:9]+' '+x.get('method','')+' '+str(x.get('status','')); tr[k]=tr.get(k,0)+1
    from datetime import datetime
    dt=(datetime.fromisoformat(b[:26])-datetime.fromisoformat(a[:26])).total_seconds()
    print(f"{s['label']:30s} rc={s['rc']:<3} {dt:5.1f}s netagent={c} stub={tr}")
