# t12table.py <steps>: per command: rc, filter refusal, and writes at the stub. OVER is a refusal of an action the preset does not deny, MISS is a denied action not refused.
import json,sys,re
L='/log/'
na=[json.loads(l) for l in open(L+'na.jsonl')]; og=[json.loads(l) for l in open(L+'origin.jsonl')]
counts={'ok-forwarded':0,'ok-refused-denied':0,'OVER':0,'MISS':0,'no-write-at-stub':0}; rows=[]
for s in map(json.loads,open(sys.argv[1])):
    a,b=s['start'],s['end']; lab=s['label']; denied='-D-' in lab
    refs=[x.get('reason','')[:80] for x in na if a<=x.get('t','')<=b and x.get('decision')=='refused']
    writes=[]
    for x in og:
        if not (a<=x.get('t','')<=b): continue
        if x.get('git'):
            if 'receive' in x.get('kind','') : writes.append('git-push')
            continue
        if 'method' not in x or 'headers' not in x: continue
        ms=[f for g in x.get('gql',[]) for o in (g.get('ops') or []) if o['type']=='mutation' for f in o['fields']]
        if ms: writes+=ms
        elif not x.get('gql') and x['method'] not in('GET','HEAD'): writes.append(x['method']+' '+x['uri'].split('?')[0][:60])
    if denied:
        st='ok-refused-denied' if refs and not any(w for w in writes if re.search(r'merge|Review|Merge|update-branch|Ref|createCommit|reviews|refs',w)) else 'MISS'
    else:
        st='OVER' if refs else ('ok-forwarded' if writes else 'no-write-at-stub')
    counts[st]+=1
    rows.append(f"{st:18s} rc={s['rc']:<3} {lab:40s} writes={','.join(writes)[:90]} refused={refs[0] if refs else ''}")
print('\n'.join(rows)); print('TOTAL',len(rows),counts)
