# Raw checks inside the pod's network namespace: authority rules, hop-by-hop, WebSocket. Output is one line per check.
import socket, ssl, base64, os, json, sys
CA='/s/trust.pem'
def proxy():
    s=socket.create_connection(('127.0.0.1',3128),timeout=8); return s
def rd_head(s):
    b=b''
    while b'\r\n\r\n' not in b:
        d=s.recv(4096)
        if not d: break
        b+=d
    return b
def connect(auth):
    s=proxy(); s.sendall(f'CONNECT {auth} HTTP/1.1\r\nHost: {auth}\r\n\r\n'.encode()); h=rd_head(s)
    if b' 200 ' not in h.split(b'\r\n')[0]: return None,h
    return s,h
def tls(s,host,alpn=('http/1.1',)):
    c=ssl.create_default_context(cafile=CA); c.set_alpn_protocols(list(alpn))
    return c.wrap_socket(s,server_hostname=host)
def req_tls(auth,sni,raw):
    s,h=connect(auth)
    if s is None: return h.split(b'\r\n')[0].decode()
    t=tls(s,sni); t.sendall(raw); t.settimeout(8)
    b=b''
    try:
        while True:
            d=t.recv(65536)
            if not d: break
            b+=d
            if b'\r\n\r\n' in b and (b'0\r\n\r\n' in b or b'Content-Length: 0' in b): break
            if b.endswith(b'\n') and b'Content-Length' in b: break
    except Exception as e: pass
    return b.decode(errors='replace')
def plain(raw):
    s=proxy(); s.sendall(raw); s.settimeout(8); b=b''
    try:
        while True:
            d=s.recv(65536)
            if not d: break
            b+=d
            if b'\r\n\r\n' in b and b.endswith(b'\r\n\r\n') or b'0\r\n\r\n' in b: break
    except Exception: pass
    return b.decode(errors='replace')
def show(name,r):
    first=r.split('\r\n')[0] if r else '(no response)'
    auth=''
    for l in r.split('\n'):
        if '"Authorization"' in l or 'X-Echo-Authorization' in l: auth=l.strip()[:80]; break
    i=r.find('"Authorization"')
    if i>=0: auth=r[i:i+60].replace('\n',' ')
    print(f'{name}: {first} | {auth}')
show('T1 terminated, Host equals bound host, placeholder sent', req_tls('api.example.test:443','api.example.test',b'GET /echo HTTP/1.1\r\nHost: api.example.test\r\nAuthorization: Bearer lenny-injected\r\nConnection: close\r\n\r\n'))
show('T2 terminated, CONNECT authority in upper case', req_tls('API.EXAMPLE.TEST:443','api.example.test',b'GET /echo HTTP/1.1\r\nHost: api.example.test\r\nConnection: close\r\n\r\n'))
show('T3 terminated, Host attacker.example inside the api connection', req_tls('api.example.test:443','api.example.test',b'GET /echo HTTP/1.1\r\nHost: attacker.example\r\nAuthorization: Bearer lenny-injected\r\nConnection: close\r\n\r\n'))
show('T4 terminated, Host api.example.test:8443 (port differs)', req_tls('api.example.test:443','api.example.test',b'GET /echo HTTP/1.1\r\nHost: api.example.test:8443\r\nConnection: close\r\n\r\n'))
show('T5 terminated, absolute-form bound target, Host disagrees', req_tls('api.example.test:443','api.example.test',b'GET https://api.example.test/echo HTTP/1.1\r\nHost: attacker.example\r\nConnection: close\r\n\r\n'))
show('T6 terminated, absolute-form attacker target, Host says bound', req_tls('api.example.test:443','api.example.test',b'GET https://attacker.example/echo HTTP/1.1\r\nHost: api.example.test\r\nConnection: close\r\n\r\n'))
show('T7 terminated, two Host headers', req_tls('api.example.test:443','api.example.test',b'GET /echo HTTP/1.1\r\nHost: api.example.test\r\nHost: attacker.example\r\nConnection: close\r\n\r\n'))
show('T8 terminated, Host names another bound host (ghes)', req_tls('api.example.test:443','api.example.test',b'GET /echo HTTP/1.1\r\nHost: ghes.example.test\r\nConnection: close\r\n\r\n'))
show('T9 pinned, Host differs', req_tls('pinned.example.test:443','pinned.example.test',b'GET /echo HTTP/1.1\r\nHost: attacker.example\r\nConnection: close\r\n\r\n'))
show('T10 pinned, same host', req_tls('pinned.example.test:443','pinned.example.test',b'GET /echo HTTP/1.1\r\nHost: pinned.example.test\r\nConnection: close\r\n\r\n'))
show('P1 plain upgrade, absolute-form', plain(b'GET http://api.example.test/echo HTTP/1.1\r\nHost: api.example.test\r\nProxy-Connection: keep-alive\r\nConnection: close, X-Hop\r\nX-Hop: drop-me\r\n\r\n'))
r=plain(b'GET http://api.example.test/echo HTTP/1.1\r\nHost: api.example.test\r\nProxy-Connection: keep-alive\r\nConnection: close, X-Hop\r\nX-Hop: drop-me\r\nX-Keep: stays\r\n\r\n')
print('P1b hop-by-hop at upstream: Proxy-Connection present=%s X-Hop present=%s X-Keep present=%s proto=%s' % ('Proxy-Connection' in r, 'X-Hop' in r, 'X-Keep' in r, 'HTTP/2.0' if 'HTTP/2.0' in r else 'HTTP/1.1' if '"HTTP/1.1"' in r else '?'))
show('P2 plain, absolute-form, Host disagrees', plain(b'GET http://api.example.test/echo HTTP/1.1\r\nHost: attacker.example\r\nConnection: close\r\n\r\n'))
show('P3 plain, absolute-form attacker target, Host says bound', plain(b'GET http://attacker.example/echo HTTP/1.1\r\nHost: api.example.test\r\nConnection: close\r\n\r\n'))
show('P4 plain, origin-form on the proxy port', plain(b'GET /echo HTTP/1.1\r\nHost: api.example.test\r\nConnection: close\r\n\r\n'))
show('P5 plain to a VCS-covered bound host, info/refs', plain(b'GET http://vcs.example.test/r.git/info/refs?service=git-receive-pack HTTP/1.1\r\nHost: vcs.example.test\r\nConnection: close\r\n\r\n'))
show('P6 plain to an unbound host, attaches nothing', plain(b'GET http://other.example.test/echo HTTP/1.1\r\nHost: other.example.test\r\nAuthorization: Bearer lenny-injected\r\nConnection: close\r\n\r\n'))
# Terminated git paths on a VCS host
show('V1 terminated VCS host, git-upload-pack refused', req_tls('vcs.example.test:443','vcs.example.test',b'POST /r.git/git-upload-pack HTTP/1.1\r\nHost: vcs.example.test\r\nContent-Length: 0\r\nConnection: close\r\n\r\n'))
show('V2 terminated VCS host, REST path allowed', req_tls('vcs.example.test:443','vcs.example.test',b'GET /r.git/HEAD HTTP/1.1\r\nHost: vcs.example.test\r\nConnection: close\r\n\r\n'))
# WebSocket
def ws(path,host='api.example.test'):
    s,h=connect(host+':443')
    t=tls(s,host); key=base64.b64encode(os.urandom(16)).decode()
    t.sendall(f'GET {path} HTTP/1.1\r\nHost: {host}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Extensions: permessage-deflate\r\nAuthorization: Bearer lenny-injected\r\n\r\n'.encode())
    t.settimeout(3); b=rd_head(t); print(' ws status:',b.split(b'\r\n')[0].decode(),'extensions-offered:', b'Sec-Websocket-Extensions' in b or b'Sec-WebSocket-Extensions' in b)
    if b' 101 ' not in b: return
    i=b.find(b'\r\n\r\n')+4; buf=b[i:]
    def frames(buf):
        out=[]
        while len(buf)>=2:
            n=buf[1]&0x7f; off=2
            if n==126: n=int.from_bytes(buf[2:4],'big'); off=4
            if len(buf)<off+n: break
            out.append((buf[0],buf[off:off+n])); buf=buf[off+n:]
        return out,buf
    msgs=[]
    try:
        while len(msgs)<2:
            d=t.recv(4096)
            if not d: break
            buf+=d; fs,buf=frames(buf); msgs+=fs
    except Exception: pass
    # one client frame, masked
    p=b'ping-from-client'; m=os.urandom(4); t.sendall(bytes([0x81,0x80|len(p)])+m+bytes(c^m[j%4] for j,c in enumerate(p)))
    try:
        while True:
            d=t.recv(4096)
            if not d: break
            buf+=d; fs,buf=frames(buf); msgs+=fs
            if any(x[1].startswith(b'echo:') for x in msgs): break
    except Exception: pass
    for op,x in msgs: print('  frame op=%#x %r' % (op,x[:60]))
print('W1 WebSocket, reflected in a text frame'); ws('/ws')
print('W2 WebSocket, reflected split across fragments'); ws('/ws?frag=1')
print('W3 WebSocket on a pinned host'); ws('/ws','pinned.example.test')
