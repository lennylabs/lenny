import json,sys
n=int(sys.argv[2]); f=sys.argv[1]
for l in open(f).readlines()[n:]:
    try: x=json.loads(l)
    except: continue
    if x.get('gitOperation') and x.get('op','').startswith('lfs'): print('netagent',x['op'],x['decision'],x.get('reason','')[:80])
    elif x.get('glTransfer') or x.get('lfsAuthObject') or x.get('lfsObject'): print('stub',x.get('host'),x.get('method'),'glTransfer' if x.get('glTransfer') else 'authObject' if x.get('lfsAuthObject') else 'object',(x.get('auth') or '-')[:44],x.get('status',''))
