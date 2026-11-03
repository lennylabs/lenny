#!/usr/bin/env python3
"""Lab target: port 9000 streams one line per second for 300s; port 9001 accepts and stays silent."""
import asyncio
async def stream(r, w):
    try:
        for i in range(300):
            w.write(f"tick {i}\n".encode()); await w.drain(); await asyncio.sleep(1)
    except Exception: pass
    w.close()
async def idle(r, w):
    try: await asyncio.sleep(300)
    except Exception: pass
    w.close()
async def main():
    a = await asyncio.start_server(stream, "0.0.0.0", 9000); b = await asyncio.start_server(idle, "0.0.0.0", 9001)
    async with a, b: await asyncio.gather(a.serve_forever(), b.serve_forever())
asyncio.run(main())
