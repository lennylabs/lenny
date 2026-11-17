# t4table.py: evasion steps side by side: exit code, what netagent decided, and what reached the origin with which token.
import json,re,glob,os
def parse(tag):
    d={}; cur=None
    for l in open(f'/out/t4/{tag}.join'):
        m=re.match(r'^([a-z0-9-]+): rc=(\d+)',l)
        if m: cur=m.group(1); d[cur]={'rc':m.group(2),'n':[], 'o':[], 'e':[]}; continue
        if cur is None: continue
        s=l.strip()
        if s.startswith('netagent git'): 
            d[cur]['n'].append('R' if ' refused ' in s else 'A')
        elif s.startswith('netagent refused'): d[cur]['n'].append('R')
        elif s.startswith('origin'):
            t=re.search(r'token=(\S+)',s); st=re.search(r'status=(\S+)',s); d[cur]['o'].append(f"{t.group(1)[:7] if t else 'api'}:{st.group(1) if st else ''}")
        elif s.startswith('egress'): d[cur]['e'].append(s.split()[-1])
    return d
tags=['Tb-gh-cli','Tb-gh-deb','P-gh-cli','P-gh-deb','L-gh-cli']
T={t:parse(t) for t in tags if os.path.exists(f'/out/t4/{t}.join')}
keys=list(T[tags[0]].keys())
for k in keys:
    print(k)
    for t in tags:
        x=T.get(t,{}).get(k)
        if x: print(f"   {t:10s} rc={x['rc']:3s} na={''.join(sorted(set(x['n']))) or '-'} origin={','.join(sorted(set(x['o'])))[:80] or '-'} egress={','.join(sorted(set(x['e']))) or '-'}")
