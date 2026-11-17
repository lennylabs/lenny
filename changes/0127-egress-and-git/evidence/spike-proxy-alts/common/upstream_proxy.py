#!/usr/bin/env python3
"""Tiny upstream CONNECT proxy standing in for a tenant's corporate proxy.
usage: upstream_proxy.py NAME PORT [user:pass]   (logs the Proxy-Authorization it receives)"""
import asyncio, base64, sys, time
NAME, PORT = sys.argv[1], int(sys.argv[2])
REQ = ("Basic " + base64.b64encode(sys.argv[3].encode()).decode()) if len(sys.argv) > 3 else None

async def pipe(r, w):
    try:
        while (d := await r.read(65536)): w.write(d); await w.drain()
    except Exception: pass
    finally:
        try: w.close()
        except Exception: pass

async def handle(r, w):
    try: head = await r.readuntil(b"\r\n\r\n")
    except Exception: w.close(); return
    lines = head.decode(errors="replace").split("\r\n")
    method, target = lines[0].split(" ")[:2]
    hdrs = {l.split(":", 1)[0].lower(): l.split(":", 1)[1].strip() for l in lines[1:] if ":" in l}
    auth = hdrs.get("proxy-authorization")
    print(f"[{NAME}] {time.strftime('%X')} {method} {target} proxy-authorization={auth!r} "
          f"decoded={base64.b64decode(auth.split()[1]).decode() if auth and auth.startswith('Basic ') else None}", flush=True)
    if REQ and auth != REQ:
        w.write(b"HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=up\r\nContent-Length: 0\r\n\r\n")
        await w.drain(); w.close(); return
    host, port = target.rsplit(":", 1)
    try:
        ur, uw = await asyncio.open_connection(host.strip("[]"), int(port))
    except Exception as e:
        w.write(b"HTTP/1.1 502 Bad Gateway\r\n\r\n"); await w.drain(); w.close(); return
    w.write(f"HTTP/1.1 200 Connection established\r\nX-Upstream: {NAME}\r\n\r\n".encode()); await w.drain()
    await asyncio.gather(pipe(r, uw), pipe(ur, w))

async def main():
    s = await asyncio.start_server(handle, "0.0.0.0", PORT)
    print(f"[{NAME}] listening {PORT} require={REQ!r}", flush=True)
    async with s: await s.serve_forever()
asyncio.run(main())
