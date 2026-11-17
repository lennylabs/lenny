import ssl,sys,asyncio,urllib.request,importlib.metadata as m
print("python",sys.version.split()[0],ssl.OPENSSL_VERSION,"requests",m.version("requests"),"urllib3",m.version("urllib3"),"httpx",m.version("httpx"),"h2",m.version("h2"),"aiohttp",m.version("aiohttp"),file=sys.stderr)
import requests,httpx,aiohttp
B="/c/bundle.pem"
U=sys.argv[1:]
def err(e):
    s=repr(e)
    for k in ("certificate verify failed","CERTIFICATE_VERIFY_FAILED"):
        i=s.find(k)
        if i>=0: return s[i:i+150]
    return s[:150]
def rep(n,u,f):
    try:
        print(n,u,"OK",f())
    except Exception as e:
        print(n,u,"REFUSED",err(e))
def hx(u,h2):
    with httpx.Client(verify=ssl.create_default_context(cafile=B),http2=h2,http1=not h2 or True,timeout=8) as c:
        r=c.get(u); return f"{r.status_code} {r.http_version}"
async def aio(u):
    async with aiohttp.ClientSession() as s:
        async with s.get(u,ssl=ssl.create_default_context(cafile=B),timeout=aiohttp.ClientTimeout(total=8)) as r:
            return f"{r.status}"
for u in U:
    rep("python-requests",u,lambda: requests.get(u,verify=B,timeout=8).status_code)
    rep("python-httpx-h1",u,lambda: hx(u,False))
    rep("python-httpx-h2",u,lambda: hx(u,True))
    rep("python-aiohttp",u,lambda: asyncio.run(aio(u)))
    rep("python-urllib",u,lambda: urllib.request.urlopen(u,context=ssl.create_default_context(cafile=B),timeout=8).status)
