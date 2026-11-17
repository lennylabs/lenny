# Stand-in outbound gateway replica: TLS-only CONNECT listener on 8443.
import socket, ssl, threading, json, sys, time
cert = sys.argv[1]; name = sys.argv[2]
log = open(f'/raw/gw-{name}.jsonl', 'a', buffering=1)
def rec(**k): k['t'] = time.time(); k['gw'] = name; log.write(json.dumps(k) + '\n')
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER); ctx.load_cert_chain(cert)
sni = {}
def cb(sock, server_name, c): sni[id(sock)] = server_name
ctx.sni_callback = cb
ctx.set_alpn_protocols(['http/1.1'])
raw = socket.socket(); raw.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1); raw.bind(('0.0.0.0', 8443)); raw.listen(64)
plain = socket.socket(); plain.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1); plain.bind(('0.0.0.0', 8080)); plain.listen(64)
def handle(c, tls):
    try:
        f = c.makefile('rb')
        line = f.readline().decode(errors='replace').strip()
        hdrs = {}
        while True:
            h = f.readline().decode(errors='replace')
            if h in ('\r\n', '\n', ''): break
            k, _, v = h.partition(':'); hdrs.setdefault(k.strip().lower(), []).append(v.strip())
        auth = line.split(' ')[1] if ' ' in line else ''
        rec(event='request', tls=tls, sni=sni.get(id(c)) if tls else None, alpn=c.selected_alpn_protocol() if tls else None, line=line, headers=hdrs)
        host = auth.split(':')[0]
        if host.startswith('noagent.'):
            c.sendall(b'HTTP/1.1 503 Service Unavailable\r\nContent-Length: 14\r\nConnection: close\r\n\r\nno_live_agent\n'); return
        if host.startswith('grant407.'):
            c.sendall(b'HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm="gw-secret-realm"\r\nX-Agent-Detail: leaked\r\nContent-Length: 20\r\nConnection: close\r\n\r\nagent-detail-leaked\n'); return
        if host.startswith('gw403.'):
            c.sendall(b'HTTP/1.1 403 Forbidden\r\nX-Agent-Detail: leaked\r\nContent-Length: 20\r\nConnection: close\r\n\r\nagent-detail-leaked\n'); return
        c.sendall(b'HTTP/1.1 200 Connection Established\r\n\r\n')
        if host.startswith('tick.'):
            for i in range(60):
                c.sendall(f'tick {i}\n'.encode()); time.sleep(1)
            return
        data = c.recv(4096)
        c.sendall(b'echo:' + data)
        time.sleep(0.5)
    except Exception as e:
        rec(event='error', tls=tls, err=repr(e))
    finally:
        try: c.close()
        except Exception: pass
def serve_tls():
    while True:
        s, a = raw.accept()
        def wrap(s=s):
            try:
                c = ctx.wrap_socket(s, server_side=True)
            except Exception as e:
                rec(event='handshake_failed', err=repr(e)); s.close(); return
            handle(c, True)
        threading.Thread(target=wrap, daemon=True).start()
def serve_plain():
    while True:
        s, a = plain.accept(); threading.Thread(target=handle, args=(s, False), daemon=True).start()
threading.Thread(target=serve_plain, daemon=True).start()
serve_tls()
