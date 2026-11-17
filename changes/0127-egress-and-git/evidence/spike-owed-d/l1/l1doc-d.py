#!/usr/bin/env python3
"""Stand-in for the policy document (I4) and the probe credentials (I8), from spike-owed-a, plus pod identities for netagent tests.
/gens?v=1,2 sets the live generations of pod-a. /host?id=&add=&del= edits an identity's list."""
import json,os,subprocess,threading,time,urllib.parse
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
PERIOD=float(os.environ.get("PERIOD","30")); EXP=float(os.environ.get("EXPIRES","3600"))
def vf(p): return subprocess.check_output(["openssl","passwd","-5",p],text=True).strip()
fixed={"p1-acme":("p1-acme.g1","acme-pw-1",["target.oa.test","rebind.oa.test","rebind2.oa.test","meta.oa.test","loop.oa.test"]),
 "probe-a":("probe-a.g2","probe-a-pw-2",["public.oa.test"]),
 "probe-b":("probe-b.g1","probe-b-pw-1",["other.oa.test"]),
 "probe-addr":("probe-addr.g1","probe-addr-pw-1",["127.0.0.1","169.254.169.254","rebind.oa.test","*.wild.oa.test"])}
V={k:vf(v[1]) for k,v in fixed.items()}
podhosts={"pod-a":["target.oa.test","public.oa.test","rebind.oa.test","api.example.test","other.example.test","vcs.example.test"],"pod-b":["other.oa.test"]}
podgens={"pod-a":[1],"pod-b":[1]}
PV={}
def pv(i,g):
    if (i,g) not in PV: PV[(i,g)]=vf(f"{i}-pw-{g}")
    return PV[(i,g)]
ver=[1000]
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
        for i,h in podhosts.items():
            ids.append({"name":i,"hosts":h,"credentials":[{"username":f"{i}.g{g}","verifier":pv(i,g)} for g in podgens[i]]})
        return {"version":f"{n}-{ver[0]}","expiresAt":time.time()+EXP,"credentialsTtlSeconds":7200,"identities":ids}
class H(BaseHTTPRequestHandler):
    def log_message(self,*a): pass
    def do_GET(self):
        global frozen
        u=urllib.parse.urlparse(self.path); q=urllib.parse.parse_qs(u.query)
        if u.path=="/doc": b=doc()
        elif u.path=="/probe":
            with lock: b={"fixed":{k:{"user":v[0],"password":v[1]} for k,v in fixed.items()},"canary":list(hist),"now":time.time(),"periodSeconds":PERIOD}
        elif u.path=="/freeze": frozen=True; b={"frozen":True}
        elif u.path=="/gens":
            with lock: podgens[q["id"][0]]=[int(x) for x in q["v"][0].split(",")]; ver[0]+=1
            b={"gens":podgens}
        elif u.path=="/host":
            with lock:
                i=q["id"][0]
                if "add" in q: podhosts[i].append(q["add"][0])
                if "del" in q: podhosts[i].remove(q["del"][0])
                ver[0]+=1
            b={"hosts":podhosts}
        else: self.send_error(404); return
        d=json.dumps(b).encode(); self.send_response(200); self.send_header("Content-Length",str(len(d))); self.end_headers(); self.wfile.write(d)
ThreadingHTTPServer(("0.0.0.0",8080),H).serve_forever()
