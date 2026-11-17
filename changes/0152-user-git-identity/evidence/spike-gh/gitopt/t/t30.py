# t30.py: LFS in GitLab's form, at the wire, with curl. The stub answers with Authorization on an action on the same host.
import json,subprocess,time,datetime,hashlib,sys
steps=open('/out/uc5/git/lfs.steps','w')
def now(): return datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%S.%f')+'000Z'
H='gitlab.example.test'
def run(label,exp,m,path,hdr=(),body=None,host=H,ct=None,show=False):
    a=['curl','-sS','-m','12','--path-as-is','-D','/tmp/hd','-o','/tmp/b','-w','%{http_code}','--cacert','/s/bundle.pem','-x','127.0.0.1:3128','--http1.1','-X',m]
    for h in hdr: a+=['-H',h]
    if ct: a+=['-H','Content-Type: '+ct]
    if body is not None:
        open('/tmp/bd','wb').write(body); a+=['--data-binary','@/tmp/bd']
    a.append(f'https://{host}{path}')
    s=now(); r=subprocess.run(a,capture_output=True,text=True,timeout=16); time.sleep(0.03); e=now()
    b=open('/tmp/b',errors='replace').read(1500); hd=open('/tmp/hd',errors='replace').read()
    steps.write(json.dumps({'label':label,'host':host,'exp':exp,'http':r.stdout.strip(),'err':r.stderr.strip()[:60],'start':s,'end':e})+'\n'); steps.flush()
    leak=[w for w in('LFSTOKEN','Z2l0bGFiLWxmczpMRlNUT0tFTi','ACTION-PRIVATE') if w in b or w in hd]
    print(f"{label:58s} http={r.stdout.strip():4s} clientSees: leak={leak} {b[:330] if show else ''} {[l for l in hd.splitlines() if l.lower().startswith('x-reflect')]}")
    return b
def oid(s): return hashlib.sha256(s.encode()).hexdigest()
def batch(repo,op,o,size=5,show=True,host=H):
    return run(f'batch {repo} {op}','fwd','POST',f'/{repo}.git/info/lfs/objects/batch',body=json.dumps({'operation':op,'transfers':['basic'],'objects':[{'oid':o,'size':size}]}).encode(),ct='application/vnd.git-lfs+json',show=show,host=host,hdr=('Accept: application/vnd.git-lfs+json',))
A=oid('a'); B=oid('b'); C=oid('c'); D=oid('d'); E=oid('e'); F=oid('f')
batch('acme/lfsgl','upload',A)
run('PUT matching transfer','fwd','PUT',f'/acme/lfsgl.git/gitlab-lfs/objects/{A}/5',body=b'hello',ct='application/octet-stream')
batch('acme/lfsgl','download',A)
run('GET matching transfer','fwd','GET',f'/acme/lfsgl.git/gitlab-lfs/objects/{A}')
run('GET matching transfer, client Authorization replaced','fwd','GET',f'/acme/lfsgl.git/gitlab-lfs/objects/{A}',hdr=('Authorization: Bearer AGENT-OWN-TOKEN',))
run('GET other oid, no batch: no credential','fwd401','GET',f'/acme/lfsgl.git/gitlab-lfs/objects/{B}')
run('GET other oid, client Authorization dropped','fwd401','GET',f'/acme/lfsgl.git/gitlab-lfs/objects/{B}',hdr=('Authorization: Bearer AGENT-OWN-TOKEN',))
run('PUT where only GET was held','fwd401','PUT',f'/acme/lfsgl.git/gitlab-lfs/objects/{A}',body=b'x',ct='application/octet-stream')
run('POST to a transfer path','ref','POST',f'/acme/lfsgl.git/gitlab-lfs/objects/{A}',body=b'x',ct='application/octet-stream')
run('same oid on another covered repository','fwd401','GET',f'/acme/lfsexp.git/gitlab-lfs/objects/{A}')
run('transfer outside the readable set','ref','GET',f'/acme/secret.git/gitlab-lfs/objects/{A}')
run('transfer with upper-case oid','ref','GET',f'/acme/lfsgl.git/gitlab-lfs/objects/{A.upper()}')
run('transfer with no .git','ref','GET',f'/acme/lfsgl/gitlab-lfs/objects/{A}')
run('transfer with trailing slash','ref','GET',f'/acme/lfsgl.git/gitlab-lfs/objects/{A}/')
run('transfer on a host that declares no such path','ref','GET',f'/acme/widgets.git/gitlab-lfs/objects/{A}',host='ghes.example.test')
batch('acme/lfsexp','download',C)
time.sleep(2.2)
run('expired action, expires_in 1, after 2.2 s','fwd401','GET',f'/acme/lfsexp.git/gitlab-lfs/objects/{C}')
batch('acme/lfsexp','download',D)
run('unexpired action, immediately','fwd404or200','GET',f'/acme/lfsexp.git/gitlab-lfs/objects/{D}')
batch('acme/lfsevil','download',E)
run('action naming another repository: not held','fwd401','GET',f'/acme/lfsgl.git/gitlab-lfs/objects/{E}')
batch('acme/lfsother','download',F)
batch('acme/lfsvar','download',F)
run('_links with a lower-case authorization: held','fwd401exp','GET',f'/acme/lfsvar.git/gitlab-lfs/objects/{F}')
steps.close()
