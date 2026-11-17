import requests, time, asyncio
t0=time.time()
with requests.get("https://api.example.test/sse", stream=True, timeout=20) as r:
    for line in r.iter_lines():
        if line: print(f"+{time.time()-t0:.2f}s", line.decode()[:90])
import websockets
async def ws():
    async with websockets.connect("wss://api.example.test/ws", proxy="http://netagent:8080") as c:
        print("WS", await c.recv()); await c.send("ping"); print("WS", await c.recv())
asyncio.run(ws())
