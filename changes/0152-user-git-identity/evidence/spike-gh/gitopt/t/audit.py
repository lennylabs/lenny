# audit.py: every credential the origin received, by host and repository, and any that went where it should not.
import json,collections
rows=collections.Counter(); bad=[]
for l in open('/log/origin.jsonl'):
    x=json.loads(l)
    if x.get('git'):
        t=x.get('token','') or ''; h=x['host'].lower().split(':')[0]; r=x.get('repo','')
        cls='none' if not t else 'mint-read' if t.startswith('MINT-R') else 'mint-write' if t.startswith('MINT-W') else 'mirror-sa' if t=='MIRROR-SA' else 'agent-own' if 'AGENT-OWN' in t else 'placeholder' if 'lenny-injected' in t else 'other:'+t[:24]
        rows[(h,cls)]+=1
        if cls.startswith('mint'):
            want=r.replace('/','_')
            if not t.endswith('-'+want): bad.append(('mint for another repo',h,r,t))
            if h not in ('ghes.example.test','github.com','gitlab.example.test','nossh.example.test'): bad.append(('mint on an uncovered host',h,r,t))
        if cls=='mirror-sa' and h!='mirror.example.test': bad.append(('mirror token off the mirror',h,r))
        if cls=='placeholder': bad.append(('placeholder reached the origin',h,r))
    elif 'method' in x and 'headers' in x:
        a=(x['headers'].get('Authorization') or x['headers'].get('Private-Token') or [''])[0]; h=x['host'].lower().split(':')[0]
        cls='none' if not a else 'GH-REAL' if 'GH-REAL' in a else 'GHCOM-REAL' if 'GHCOM-REAL' in a else 'GL-REAL' if 'GL-REAL' in a else 'placeholder' if 'lenny-injected' in a else 'other:'+a[:24]
        rows[(h+' API',cls)]+=1
        exp={'ghes.example.test':'GH-REAL','api.github.com':'GHCOM-REAL','gitlab.example.test':'GL-REAL'}.get(h)
        if cls.endswith('REAL') and cls!=exp: bad.append(('API token on the wrong host',h,cls))
        if cls=='placeholder': bad.append(('placeholder at the API',h,x['uri'][:50]))
for k,v in sorted(rows.items()): print(f"{k[0]:32s} {k[1]:18s} {v}")
print("findings:",len(bad))
for b in bad[:40]: print(b)
