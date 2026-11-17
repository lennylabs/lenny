#!/usr/bin/env python3
"""Measure how long a proxy keeps admitting new CONNECTs after the decision service revokes a lease,
and how long a reinstated lease stays denied. usage: revocation_probe.py PROXY:PORT TOKEN TARGET"""
import socket, sys, time, base64, json, urllib.request
proxy, tok, target = sys.argv[1], sys.argv[2], sys.argv[3]
ph, pp = proxy.rsplit(":", 1); cred = base64.b64encode(f"agent:{tok}".encode()).decode()
def post(path, obj):
    urllib.request.urlopen(urllib.request.Request("http://spike-authz:8080" + path, json.dumps(obj).encode(), {"Content-Type": "application/json"}))
def calls(): return json.load(urllib.request.urlopen("http://spike-authz:8080/v1/stats"))["calls"]
def probe():
    s = socket.create_connection((ph, int(pp)), timeout=10)
    s.sendall(f"CONNECT {target} HTTP/1.1\r\nHost: {target}\r\nProxy-Authorization: Basic {cred}\r\n\r\n".encode())
    code = s.recv(4096).split(b" ")[1].decode(); s.close(); return code
post("/v1/reset", {})
print("warm:", probe(), "decision calls:", calls())
post("/v1/revoke", {"token": tok}); t0 = time.time()
while (c := probe()) == "200": time.sleep(0.5)
print(f"revoked: still admitted for {time.time()-t0:.1f}s, then {c}; decision calls: {calls()}")
post("/v1/reset", {}); t1 = time.time()
while (c := probe()) != "200": time.sleep(0.5)
print(f"reinstated: still denied for {time.time()-t1:.1f}s, then {c}")
