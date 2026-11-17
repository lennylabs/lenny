#!/usr/bin/env python3
"""mar.py PROFILE URL...: drives Firefox over Marionette, with no other package, and prints what each navigation reached.
The result is read from the page: a document served by the test server, or Firefox's own error page with its code."""
import json,os,socket,subprocess,sys,time,re
profile=sys.argv[1]; urls=sys.argv[2:]
env=dict(os.environ); env["MOZ_HEADLESS"]="1"
log=open("/tmp/ff.log","ab")
p=subprocess.Popen(["firefox-esr","--headless","--marionette","--no-remote","--profile",profile],env=env,stdout=log,stderr=log)
s=None
for i in range(240):
    try:
        s=socket.create_connection(("127.0.0.1",2828),timeout=2); break
    except OSError: time.sleep(0.5)
if s is None: print("no marionette"); p.kill(); sys.exit(3)
s.settimeout(40)
def rd():
    b=b""
    while b":" not in b: b+=s.recv(1)
    n,_,rest=b.partition(b":"); n=int(n); d=rest
    while len(d)<n: d+=s.recv(n-len(d))
    return json.loads(d)
rd()
mid=[0]
def cmd(name,params={}):
    mid[0]+=1
    m=json.dumps([0,mid[0],name,params]).encode(); s.sendall(str(len(m)).encode()+b":"+m)
    r=rd(); return r[2],r[3]
err,res=cmd("WebDriver:NewSession",{"capabilities":{"alwaysMatch":{"acceptInsecureCerts":False,"pageLoadStrategy":"normal"}}})
print("session:",(res or {}).get("capabilities",{}).get("browserVersion"),err and err.get("message"))
cmd("WebDriver:SetTimeouts",{"pageLoad":15000})
for u in urls:
    t=time.time()
    err,res=cmd("WebDriver:Navigate",{"url":u})
    e1=(err or {}).get("message","")
    time.sleep(0.5)
    _,cu=cmd("WebDriver:GetCurrentURL")
    _,src=cmd("WebDriver:GetPageSource")
    cu=(cu or {}).get("value",""); src=(src or {}).get("value","")
    # the error page carries its code in its document URI and in its error info
    extra=""
    er2,r2=cmd("WebDriver:ExecuteScript",{"script":"var o={uri:document.documentURI,title:document.title}; try{o.net=document.getNetErrorInfo()}catch(e){}; try{o.cert=document.getFailedCertSecurityInfo().errorCodeString}catch(e){}; return JSON.stringify(o)","args":[],"sandbox":None})
    if not er2 and r2 and r2.get("value"):
        extra=r2["value"]
    if er2: extra="script error "+str(er2.get("message"))[:80]
    served=re.search(r"served ([a-z-]+)",src)
    print(json.dumps({"url":u,"result":"served "+served.group(1) if served else "NOT SERVED","code":(re.search(r"(SEC_ERROR_[A-Z_]+|SSL_ERROR_[A-Z_]+|MOZILLA_PKIX_ERROR_[A-Z_]+|NS_ERROR_[A-Z_]+)",extra) or [None,""])[1],"info":extra[:230],"error":e1[:150],"landing":cu[:120],"secs":round(time.time()-t,2),**({"body":re.sub(r"<[^>]+>","",src)[:200]} if os.environ.get("SHOWSRC") else {})}))
    sys.stdout.flush()
cmd("Marionette:Quit") if False else None
p.terminate()
try: p.wait(10)
except Exception: p.kill()
