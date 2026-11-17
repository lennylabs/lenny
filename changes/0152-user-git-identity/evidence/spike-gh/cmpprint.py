import json
a=json.load(open('/o/raw/corpus-printed-go.json')); r=json.load(open('/o/raw/corpus-printed-ruby.json')); o=json.load(open('/o/raw/corpus-go.json'))
def s(x): return 'ERR' if 'err' in x else ' | '.join(f"{p['type'][0]}:{','.join(p['fields'])}" for p in x.get('ops',[]))
n=0
for i in sorted(a):
    ga=s(o[i]['gqlparser']); gp=s(a[i]['gqlparser']); rb=s(r[i])
    flag='' if ga==gp==rb else 'DIFF'
    n+=bool(flag)
    if flag or i.startswith('cr-'): print(i,'| original gqlparser:',ga,'| printed gqlparser:',gp,'| printed graphql-ruby:',rb,flag)
print('documents',len(a),'differences',n)
