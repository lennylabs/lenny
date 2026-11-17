import json
P=lambda *f:[{'prefix':'acme/','mode':'required','allowRuntimeHeldCredentials':x} for x in f]
base=lambda **k:{**{'pools':P(True),'released':True,'suspended':False,'current':'USER-FILE-TOKEN','noCredential':'challenge'},**k}
S={'chal':base(),'mint':base(noCredential='mint'),'rotated':base(current='NEW-VALUE-after-rotation'),'suspended':base(suspended=True),'flagoff':base(pools=P(False)),'norelease':base(released=False),'twopools':base(pools=P(True,False)),'twopoolson':base(pools=P(True,True))}
for k,v in S.items(): json.dump({'ghes.example.test':v,'github.com':v},open(f'/t/session-{k}.json','w'),indent=1)
