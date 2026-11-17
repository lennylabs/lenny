import json,collections
c=collections.Counter()
for l in open('/log/origin.jsonl'):
    try: x=json.loads(l)
    except: continue
    s=json.dumps(x.get('hdrs') or x.get('headers') or {})+str(x.get('token') or x.get('tok') or '')+str(x.get('auth') or '')
    import base64,re
    for b in re.findall(r'Basic ([A-Za-z0-9+/=]+)',s):
        try: s+=base64.b64decode(b).decode(errors='replace')
        except: pass
    for k in('USER-FILE-OLD-HELD','OTHER-TOKEN','AGENT-OWN','USER-FILE-CURRENT','USER-FILE-ROTATED','LFSTOKEN','ACME-REAL','OTHER-REAL','NOSSH-REAL'):
        if k in s: c[(k,x.get('host'))]+=1
for k,v in sorted(c.items()): print(v,k)
