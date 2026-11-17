import json,sys
for f in sys.argv[1:]:
    for l in open(f):
        r=json.loads(l)
        for g in r.get('gql',[]):
            if g.get('ops') and g['ops'][0]['type']=='mutation':
                print(f.split('/')[-1], g['doc'][:300].replace('\n',' '), json.dumps(g.get('variables'))[:300])
