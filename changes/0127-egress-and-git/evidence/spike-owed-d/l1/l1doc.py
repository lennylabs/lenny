#!/usr/bin/env python3
"""Stand-in for the policy document (I4) and the probe credentials (I8). It rotates a canary generation and a canary host every PERIOD seconds.
At rotation n the canary identity holds generations n and n-1 and hosts canary-(n%8) and canary-((n-1)%8), and generation n-2 is retired."""
import json,os,subprocess,threading,time
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
PERIOD=float(os.environ.get("PERIOD","30")); EXP=float(os.environ.get("EXPIRES","3600"))
def vf(p): return subprocess.check_output(["openssl","passwd","-5",p],text=True).strip()
fixed={"p1-acme":("p1-acme.g1","acme-pw-1",["target.oa.test","rebind.oa.test","rebind2.oa.test","meta.oa.test","loop.oa.test"]),
 "probe-a":("probe-a.g2","probe-a-pw-2",["public.oa.test"]),
 "probe-b":("probe-b.g1","probe-b-pw-1",["other.oa.test"]),
 "probe-addr":("probe-addr.g1","probe-addr-pw-1",["127.0.0.1","169.254.169.254","rebind.oa.test","*.wild.oa.test"])}
V={k:vf(v[1]) for k,v in fixed.items()}
lock=threading.Lock(); n=1; frozen=False; hist=[]
def cpw(g): return f"canary-pw-{g}"
CV={}
def cv(g):
    if g not in CV: CV[g]=vf(cpw(g))
    return CV[g]
def host(g): return f"canary-{g%8}.oa.test"
def rotate(first=False):
    global n
    with lock:
        if not first: n+=1
        cv(n)
        if n>1: cv(n-1)
        hist.append({"n":n,"at":time.time(),"user":f"probe-canary.g{n}","password":cpw(n),"host":host(n)+":443",
          "retired":({"user":f"probe-canary.g{n-2}","password":cpw(n-2)} if n>2 else None)})
        del hist[:-4]
rotate(True)
def loop():
    while True:
        time.sleep(PERIOD)
        if not frozen: rotate()
threading.Thread(target=loop,daemon=True).start()
def doc():
    with lock:
        ids=[{"name":k,"hosts":v[2],"credentials":[{"username":v[0],"verifier":V[k]}]} for k,v in fixed.items()]
        gens=[g for g in (n,n-1) if g>0]
        ids.append({"name":"probe-canary","hosts":sorted({host(g) for g in gens}),"credentials":[{"username":f"probe-canary.g{g}","verifier":cv(g)} for g in gens]})
        return {"version":n,"expiresAt":time.time()+EXP,"credentialsTtlSeconds":7200,"identities":ids}
class H(BaseHTTPRequestHandler):
    def log_message(self,*a): pass
    def do_GET(self):
        global frozen
        if self.path=="/doc": b=doc()
        elif self.path=="/probe":
            with lock: b={"fixed":{k:{"user":v[0],"password":v[1]} for k,v in fixed.items()},"canary":list(hist),"now":time.time(),"periodSeconds":PERIOD}
        elif self.path=="/freeze": frozen=True; b={"frozen":True}
        else: self.send_error(404); return
        d=json.dumps(b).encode(); self.send_response(200); self.send_header("Content-Length",str(len(d))); self.end_headers(); self.wfile.write(d)
ThreadingHTTPServer(("0.0.0.0",8080),H).serve_forever()
