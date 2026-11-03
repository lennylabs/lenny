# t18.py <policy-label>: every extracted route and mutation, on the GHES and github.com presets. REST rows use the templates from the OpenAPI description.
import json,subprocess,time,datetime,sys
pol=sys.argv[1]; steps=open(f'/out/review/{pol}-routes.steps','w')
def now(): return datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%S.%f')+'000Z'
R=json.load(open('/t/routes.json')); M=json.load(open('/t/mutations.json'))
F={'ghes':('ghes.example.test','/api/v3','/api/graphql'),'com':('api.github.com','','/graphql')}
def go(label,fl,m,path,body,cls):
    host=F[fl][0]
    a=['curl','-sS','-m','10','--http1.1','-o','/tmp/b','-w','%{http_code}','--cacert','/s/bundle.pem','-x','127.0.0.1:3128','-X',m,'-H','Authorization: token lenny-injected','-H','Content-Type: application/json','--data-binary',body,f'https://{host}{path}']
    s=now()
    try: code=subprocess.run(a,capture_output=True,text=True,timeout=14).stdout.strip()
    except subprocess.TimeoutExpired: code='timeout'
    time.sleep(0.03); e=now()
    steps.write(json.dumps({'label':label,'class':cls,'proto':'h1','fl':fl,'method':m,'path':path,'http':code,'err':'','resp':'','start':s,'end':e})+'\n'); steps.flush()
for fl in F:
    for r in R: go(r['method']+' '+r['template'].replace('/repos/{owner}/{repo}',''),fl,r['method'],F[fl][1]+r['path'],'{}',r['class'])
    for n in M: go('mutation '+n,fl,'POST',F[fl][2],json.dumps({'query':'mutation{%s(input:{}){clientMutationId}}'%n}),'mutation')
steps.close()
