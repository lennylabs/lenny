# join.py <steps.jsonl> [--brief]: per step, what netagent decided, what the origin received, and what egress saw.
import json,sys,os,base64
L='/log/'
def rd(f):
    p=L+f
    return [json.loads(l) for l in open(p)] if os.path.exists(p) else []
na=[x for x in rd('na.jsonl')]; og=rd('origin.jsonl'); eg=rd('egress.jsonl')
for s in map(json.loads,open(sys.argv[1])):
    a,b=s['start'],s['end']
    win=lambda xs:[x for x in xs if a<=x.get('t',x.get('T',''))<=b]
    print(f"{s['label']}: rc={s['rc']}")
    for x in win(na):
        if x.get('gitOperation'): print(f"   netagent git[{x['entry']}] {x['host']} {x['repo']} {x['op']} {x['decision']} {x['reason'][:110]} {[c['Ref'] for c in (x.get('refs') or [])] or ''} {' '.join(x.get('wire') or []) if x.get('op') in ('git-receive-pack',) else ''}")
        elif x.get('conn')=='' and x.get('decision')!='refused' and 'method' in x: print(f"   netagent plain {x['method']} {x['host']}{x['uri'][:60]} decision={x.get('decision')}")
        elif x.get('decision')=='refused': print(f"   netagent refused {x['method']} {x['host']}{x['uri'][:70]}: {x['reason'][:90]}")
    for x in win(og):
        if x.get('git'): print(f"   origin {x['proto']} {x['method']} {x['host']} {x['uri'][:70]} token={x['token'][:32] or '-'} status={x.get('status')} body={x.get('bodyBytes')} te={x.get('te')} ce={x.get('ce') or ''}")
        elif x.get('lfsObject'): print(f"   origin LFS object {x['method']} {x['host']} bytes={x.get('bytes')} status={x.get('status','')}")
        elif 'method' in x: print(f"   origin API {x['method']} {x['host']} {x['uri'][:50]} auth={x['headers'].get('Authorization',x['headers'].get('Private-Token',['-']))[0][:24]} {[f for g in x.get('gql',[]) for o in g.get('ops',[]) if o['type']=='mutation' for f in o['fields']] or ''}")
    for x in win(eg):
        if x['code']!=200: print(f"   egress {x['target']} {x['code']}")
