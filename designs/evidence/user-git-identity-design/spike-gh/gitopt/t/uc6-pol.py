# uc6-pol.py: revision 6 deltas over the revision 5 presets.
import json
src=open('/t/uc5-pol.py').read()
src=src.replace("P+'/access_tokens',P+'/deploy_tokens']","P+'/access_tokens/**',P+'/deploy_tokens/**']")
src=src.replace("'/t/policy-uc5.json'","'/t/policy-uc6-base.json'").replace("'/t/policy-uc5-oracle.json'","'/t/policy-uc6-oracle.json'").replace("/t/vcs5","/t/vcs6").replace("/t/bindings-uc5","/t/bindings-uc6-base").replace("/t/s5-","/t/s6x-")
src=src.replace("'acme/lfsevil','acme/lfsothera'):","'acme/lfsevil','acme/lfsothera','acme/lfsfail','acme/lfsfail401'):")
exec(src)
d=json.load(open('/t/policy-uc6-base.json'))
gh=d['ghes']['denyMutations']; gl=d['gitlab']['denyMutations']; un=sorted(set(gh)|set(gl))
for k in('ghes','ghcom','gitlab'): d[k]['denyMutations']=un
d['genericc']={'mode':'deny','matcher':'uc4','restPrefix':'/','stripHeaders':['Authorization','Cookie'],'uc4':{'default':'deny','rules':[{'action':'allow','methods':['GET','POST'],'path':'/rest/api/*/project'},{'action':'allow','methods':['GET','DELETE'],'path':'/files/*'},{'action':'deny','methods':['DELETE'],'path':'/files/report.tar'}]}}
json.dump(d,open('/t/policy-uc6.json','w'),indent=1)
b=json.load(open('/t/bindings-uc4.json'))
b+=[{"hosts":["gh-on-gl.example.test"],"header":"Authorization","value":"token WF1-REAL-aa01","secret":"WF1-REAL-aa01","filter":"ghes"},
    {"hosts":["gl-on-ghes.example.test"],"header":"Private-Token","value":"WF2-REAL-bb02","secret":"WF2-REAL-bb02","filter":"gitlab"},
    {"hosts":["gen-c.example.test"],"header":"Authorization","value":"Bearer GENC-REAL-cc03","secret":"GENC-REAL-cc03","filter":"genericc"}]
json.dump(b,open('/t/bindings-uc6.json','w'),indent=1)
v=json.load(open('/t/vcs6.json'))
r=json.loads(json.dumps(v)); r.append({"host":"assets.example.test","noPool":True,"readable":[],"writable":[],"refPatterns":[],"user":"x-access-token"})
json.dump(r,open('/t/vcs6-restricted.json','w'),indent=1)
Pl=lambda *f:[{'prefix':'acme/','mode':'required','allowRuntimeHeldCredentials':x} for x in f]
base=lambda **k:{**{'named':True,'pools':Pl(True),'released':True,'suspended':False,'current':'USER-FILE-CURRENT','handling':2,'routeDigest':{'git':'gitdigest1','egress':'egressdigest1'}},**k}
S={'all':base(),'rotated':base(current='USER-FILE-ROTATED'),'suspended':base(suspended=True),'flagcleared':base(pools=Pl(False)),'norelease':base(released=False),'twopools':base(pools=Pl(True,False)),'twopoolson':base(pools=Pl(True,True)),'notnamed':base(named=False)}
for k,x in S.items(): json.dump({'ghes.example.test':x,'github.com':x,'gitlab.example.test':x},open(f'/t/s6-{k}.json','w'),indent=1)
print('union',len(un))
