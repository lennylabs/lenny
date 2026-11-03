import json,sys
n=int(sys.argv[2]); f=sys.argv[1]
for l in open(f).readlines()[n:]:
    x=json.loads(l)
    if x.get('gitOperation'): print('netagent',x['op'],x['decision'],x.get('reason','')[:90])
    elif x.get('git'): print('origin',x.get('host'),x.get('kind'),x.get('repo'),x.get('status'),x.get('token') or x.get('tok'),x.get('uri','')[:60])
