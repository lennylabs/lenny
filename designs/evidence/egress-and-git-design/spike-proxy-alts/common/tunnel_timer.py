#!/usr/bin/env python3
"""CONNECT through PROXY to TARGET with a lease, read until EOF, report elapsed seconds and bytes.
usage: tunnel_timer.py PROXYHOST:PORT TARGET:PORT TOKEN [maxsecs]"""
import socket, sys, time, base64
ph, pp = sys.argv[1].rsplit(":", 1); target, tok = sys.argv[2], sys.argv[3]
mx = float(sys.argv[4]) if len(sys.argv) > 4 else 200
s = socket.create_connection((ph, int(pp))); s.settimeout(mx)
cred = base64.b64encode(f"agent:{tok}".encode()).decode()
s.sendall(f"CONNECT {target} HTTP/1.1\r\nHost: {target}\r\nProxy-Authorization: Basic {cred}\r\n\r\n".encode())
t0 = time.time(); buf = b""
while b"\r\n\r\n" not in buf:
    d = s.recv(4096)
    if not d: break
    buf += d
status = buf.split(b"\r\n")[0].decode(errors="replace"); n = len(buf.split(b"\r\n\r\n", 1)[-1])
try:
    while (d := s.recv(4096)): n += len(d)
    how = "EOF"
except socket.timeout: how = "client-timeout"
except ConnectionResetError: how = "RST"
print(f"target={target} status={status!r} closed_by={how} elapsed={time.time()-t0:.1f}s bytes={n}")
