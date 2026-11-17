# t22.py: the time and concurrency bounds, through the egress endpoint.
import subprocess,time,socket,json
Q='{"query":"query{viewer{login}}","variables":{"a":"%s"}}'%('x'*40000)
open('/tmp/slow','w').write(Q)
C=['curl','-sS','-m','45','--http1.1','-o','/dev/null','-w','%{http_code}','--cacert','/s/bundle.pem','-x','127.0.0.1:3128','-H','Authorization: token lenny-injected','-H','Content-Type: application/json','--data-binary','@/tmp/slow','--limit-rate','4k','https://api.github.com/graphql']
t=time.time(); ps=[subprocess.Popen(C,stdout=subprocess.PIPE,text=True) for _ in range(6)]
time.sleep(0.2)
for p in ps:
    o=p.communicate(timeout=60)[0]; print('concurrent slow body:',o,round(time.time()-t,1))
t=time.time(); open('/tmp/s2','w').write('{"query":"query{viewer{login}}","variables":{"a":"%s"}}'%('x'*200000))
r=subprocess.run(C[:C.index('@/tmp/slow')]+['@/tmp/s2','--limit-rate','2k','https://api.github.com/graphql'],capture_output=True,text=True,timeout=60)
print('slow body past 30 s: http',r.stdout,'after',round(time.time()-t,1),'s',r.stderr[:80])
