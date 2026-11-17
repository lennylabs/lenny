import os, requests, httpx, asyncio, aiohttp
U="https://api.example.test/acme/check"; H={"Authorization":"Bearer "+os.environ["ACME_API_KEY"]}
print("requests", requests.get(U, headers=H).text.strip())
with httpx.Client(trust_env=True) as c: print("httpx", c.get(U, headers=H).text.strip(), c.get(U).http_version)
try:
    with httpx.Client(http2=True) as c: r=c.get(U, headers=H); print("httpx-h2", r.text.strip(), r.http_version)
except Exception as e: print("httpx-h2 n/a:", type(e).__name__, str(e)[:60])
async def a():
    async with aiohttp.ClientSession(trust_env=True) as s:
        async with s.get(U, headers=H) as r: print("aiohttp", (await r.text()).strip())
asyncio.run(a())
