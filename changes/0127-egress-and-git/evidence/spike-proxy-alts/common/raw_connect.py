#!/usr/bin/env python3
"""Send raw CONNECT lines (exact authority bytes) and print the proxy's status line.
usage: raw_connect.py PROXYHOST:PORT TOKEN AUTHORITY..."""
import socket, sys, base64
ph, pp = sys.argv[1].rsplit(":", 1); tok = sys.argv[2]
cred = base64.b64encode(f"agent:{tok}".encode()).decode()
for a in sys.argv[3:]:
    try:
        s = socket.create_connection((ph, int(pp)), timeout=20)
        s.sendall(f"CONNECT {a} HTTP/1.1\r\nHost: {a}\r\nProxy-Authorization: Basic {cred}\r\n\r\n".encode())
        st = s.recv(4096).split(b"\r\n")[0].decode(errors="replace"); s.close()
    except Exception as e: st = f"ERR {e}"
    print(f"{a:45s} -> {st}", flush=True)
