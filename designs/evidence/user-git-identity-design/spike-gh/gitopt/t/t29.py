# t29.py: EG's git path rules, row by row, with a hard curl timeout. "ref" means nothing reaches the stub.
import json,subprocess,time,datetime,sys,gzip
steps=open('/out/uc5/git/rows.steps','w')
def now(): return datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%S.%f')+'000Z'
def run(label,host,m,path,exp,hdr=(),body=None,scheme='https',ct=None,proto='--http1.1'):
    a=['curl','-sS','-m','12','--path-as-is','-o','/tmp/b','-w','%{http_code}','--cacert','/s/bundle.pem','-x','127.0.0.1:3128',proto,'-X',m]
    for h in hdr: a+=['-H',h]
    if ct: a+=['-H','Content-Type: '+ct]
    if body is not None:
        open('/tmp/bd','wb').write(body); a+=['--data-binary','@/tmp/bd']
    a.append(f'{scheme}://{host}{path}')
    s=now()
    try: r=subprocess.run(a,capture_output=True,text=True,timeout=16); code=r.stdout.strip(); err=r.stderr.strip()[:70]
    except subprocess.TimeoutExpired: code='timeout'; err=''
    time.sleep(0.03); e=now()
    try: b=open('/tmp/b',errors='replace').read(120)
    except: b=''
    steps.write(json.dumps({'label':label,'host':host,'exp':exp,'http':code,'err':err,'resp':b,'start':s,'end':e})+'\n'); steps.flush()
G='ghes.example.test'; R='/acme/widgets.git'
RP='application/x-git-receive-pack-request'
AUTH='Authorization: Bearer AGENT-OWN-TOKEN'
# classification and the strict form, on each kind of terminated host
for host,pre in((G,''),('gitlab.example.test',''),('api.example.test',''),('other.example.test',''),('api.github.com','')):
    for l,m,p,ct in(('trailing slash','POST',R+'/git-receive-pack/',RP),('.json','POST',R+'/git-receive-pack.json',RP),('double slash','POST','/acme//widgets.git/git-receive-pack',RP),('leading double slash','POST','//acme/widgets.git/git-receive-pack',RP),('%2d','POST',R+'/git%2dreceive-pack',RP),('%252d','POST',R+'/git%252dreceive-pack',RP),('upper case','POST',R+'/GIT-RECEIVE-PACK',RP),('mixed case info/refs','GET',R+'/Info/Refs?service=git-upload-pack',None),('%2F in info/refs','GET',R+'/info%2Frefs?service=git-upload-pack',None),('semicolon','GET',R+'/info/refs;a=b?service=git-upload-pack',None),('dot segment','GET','/acme/./widgets.git/info/refs?service=git-upload-pack',None),('dotdot','GET','/acme/x/../widgets.git/info/refs?service=git-upload-pack',None),('encoded dotdot','GET','/acme/x/%2e%2e/widgets.git/info/refs?service=git-upload-pack',None),('INFO/LFS','POST',R+'/INFO/LFS/objects/batch','application/vnd.git-lfs+json'),('bare .git/','GET','/x/.git/config',None),('marker deep in a path','GET','/a/b/c/info/refs/d',None)):
        run(f'strict {host} {l}',host,m,p,'ref',ct=ct,body=b'0000' if m=='POST' else None,hdr=(AUTH,))
# a marker match on an API path never reaches inject
for host,p in((G,'/api/v3/repos/acme/widgets/contents/docs/info/refs'),(G,'/api/v3/repos/acme/.git/config'),('api.github.com','/repos/acme/widgets/contents/.git/config'),('api.github.com','/repos/acme/widgets/contents/git-upload-pack.md')):
    run(f'collateral GET {host}{p}',host,'GET',p,'ref')
run('collateral PUT contents/.git/config','api.github.com','PUT','/repos/acme/widgets/contents/.git/config','ref',body=b'{}',ct='application/json')
run('not a marker, GET gitlog.txt','api.example.test','GET','/x/gitlog.txt','fwd')
# query
for l,q in(('no query',''),('two services','?service=git-upload-pack&service=git-receive-pack'),('repeat same','?service=git-upload-pack&service=git-upload-pack'),('other value','?service=foo'),('Service case','?Service=git-upload-pack'),('extra key','?service=git-upload-pack&x=1'),('encoded value','?service=git%2dupload-pack'),('empty','?service=')):
    run('query '+l,G,'GET',R+'/info/refs'+q,'ref')
run('query upload-pack',G,'GET',R+'/info/refs?service=git-upload-pack','fwd'); run('query receive-pack',G,'GET',R+'/info/refs?service=git-receive-pack','fwd')
# overrides
for h in('X-HTTP-Method-Override: POST','X-HTTP-Method: POST','X-Method-Override: POST','X-Original-Method: POST','X-Original-URL: /x','X-Rewrite-URL: /x','X-Forwarded-Path: /x','X-Forwarded-Prefix: /x','X-Forwarded-Uri: /x','X-Envoy-Original-Path: /x','Upgrade: websocket','HTTP2-Settings: AAMAAABkAAQAoAAAAAIAAAAA','Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ=='):
    run('override '+h,G,'GET',R+'/info/refs?service=git-upload-pack','ref',hdr=(h,))
run('headers: only what git sends',G,'GET',R+'/info/refs?service=git-upload-pack','fwd',hdr=('X-Evil: 1','Cookie: c=AGENT-OWN','Private-Token: AGENT-OWN','X-Api-Key: AGENT-OWN',AUTH,'Git-Protocol: version=2'))
# receive-pack
def pk(line): return b'%04x'%(len(line)+4)+line
Z=b'0'*40; O=b'1'*40
good=pk(Z+b' '+O+b' refs/heads/lenny/rp\0 report-status\n')+b'0000'
main=pk(Z+b' '+O+b' refs/heads/main\0 report-status\n')+b'0000'
cert=pk(b'push-cert\0 report-status\n')+pk(b'certificate version 0.1\n')+pk(b'pusher a\n')+pk(b'\n')+pk(Z+b' '+O+b' refs/heads/main\n')+pk(b'push-cert-end\n')+b'0000'
opt=pk(Z+b' '+O+b' refs/heads/lenny/rp\0 report-status push-options\n')+b'0000'
run('receive-pack plain, allowed ref',G,'POST',R+'/git-receive-pack','fwd',ct=RP,body=good)
run('receive-pack main',G,'POST',R+'/git-receive-pack','ref',ct=RP,body=main)
run('receive-pack gzip, allowed ref',G,'POST',R+'/git-receive-pack','ref',ct=RP,body=gzip.compress(good),hdr=('Content-Encoding: gzip',))
run('receive-pack gzip, main',G,'POST',R+'/git-receive-pack','ref',ct=RP,body=gzip.compress(main),hdr=('Content-Encoding: gzip',))
run('receive-pack deflate',G,'POST',R+'/git-receive-pack','ref',ct=RP,body=b'x',hdr=('Content-Encoding: deflate',))
run('receive-pack push-cert',G,'POST',R+'/git-receive-pack','ref',ct=RP,body=cert)
run('receive-pack push option',G,'POST',R+'/git-receive-pack','ref',ct=RP,body=opt)
run('receive-pack wrong type',G,'POST',R+'/git-receive-pack','ref',ct='application/json',body=good)
run('receive-pack with a query',G,'POST',R+'/git-receive-pack?x=1','ref',ct=RP,body=good)
run('receive-pack GET',G,'GET',R+'/git-receive-pack','ref')
# smart HTTP on a bound host with no pool
for host in('api.example.test','other.example.test'):
    run(f'bound {host} fetch public, client credential',host,'GET','/pub/lib.git/info/refs?service=git-upload-pack','fwd',hdr=(AUTH,))
    run(f'bound {host} fetch private, no secret attached',host,'GET','/acme/widgets.git/info/refs?service=git-upload-pack','fwd',hdr=(AUTH,))
    run(f'bound {host} push advert',host,'GET','/acme/widgets.git/info/refs?service=git-receive-pack','ref',hdr=(AUTH,))
    run(f'bound {host} push',host,'POST','/acme/widgets.git/git-receive-pack','ref',ct=RP,body=good,hdr=(AUTH,))
    run(f'bound {host} LFS upload',host,'POST','/acme/widgets.git/info/lfs/objects/batch','ref',ct='application/vnd.git-lfs+json',body=b'{"operation":"upload","objects":[]}')
    run(f'bound {host} other request takes inject',host,'GET','/x','fwd')
run('plain absolute-form, covered',G,'GET',R+'/info/refs?service=git-upload-pack','fwd',scheme='http')
run('plain absolute-form, covered, receive-pack main',G,'POST',R+'/git-receive-pack','ref',ct=RP,body=main,scheme='http')
run('plain absolute-form, bound, fetch','api.example.test','GET','/pub/lib.git/info/refs?service=git-upload-pack','fwd',scheme='http',hdr=(AUTH,))
run('plain absolute-form, bound, push','api.example.test','POST','/acme/widgets.git/git-receive-pack','ref',ct=RP,body=good,scheme='http')
run('plain absolute-form, spelling',G,'POST',R+'/git-receive-pack/','ref',ct=RP,body=main,scheme='http')
run('h2 spelling',G,'POST',R+'/git-receive-pack/','ref',ct=RP,body=main,proto='--http2')
run('h2 receive-pack main',G,'POST',R+'/git-receive-pack','ref',ct=RP,body=main,proto='--http2')
steps.close()
