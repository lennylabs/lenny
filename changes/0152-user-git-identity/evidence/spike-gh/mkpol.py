import json
b=json.load(open('/o/policy-base.json'))
json.dump(b,open('/o/policy-v1.json','w'),indent=1)
for v in b.values(): v['lexStrict']=True; v['maxDepth']=32
json.dump(b,open('/o/policy-strict.json','w'),indent=1)
for v in b.values(): v['canonicalForward']=True
json.dump(b,open('/o/policy-canon.json','w'),indent=1)
