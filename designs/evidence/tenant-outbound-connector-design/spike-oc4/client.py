# Raw CONNECT client through Envoy. Prints one JSON line per case.
import socket, json, sys, time, base64
ENVOY = (sys.argv[1], 3128)
def connect(authority, send=b'hello', read_for=2.0):
    s = socket.create_connection(ENVOY, timeout=10)
    s.sendall(f'CONNECT {authority} HTTP/1.1\r\nHost: {authority}\r\nProxy-Authorization: Basic {base64.b64encode(b"pod:pod-secret").decode()}\r\n\r\n'.encode())
    buf = b''
    while b'\r\n\r\n' not in buf:
        d = s.recv(4096)
        if not d: break
        buf += d
    head, _, rest = buf.partition(b'\r\n\r\n')
    lines = head.decode(errors='replace').split('\r\n')
    status = lines[0]; hdrs = [l for l in lines[1:]]
    body = rest
    t0 = time.time(); closed_after = None
    if ' 200 ' in status:
        if send: s.sendall(send)
        s.settimeout(read_for)
        try:
            while True:
                d = s.recv(4096)
                if not d: closed_after = round(time.time() - t0, 2); break
                body += d
        except socket.timeout:
            pass
    else:
        s.settimeout(2)
        try:
            while True:
                d = s.recv(4096)
                if not d: break
                body += d
        except socket.timeout: pass
    s.close()
    return {'authority': authority, 'status': status, 'headers': hdrs, 'body': body.decode(errors='replace')[:200], 'closed_after_s': closed_after}
cases = sys.argv[2:]
for c in cases:
    if c == 'tick':
        r = connect('tick.acme.internal:443', send=None, read_for=20)
        r['ticks'] = r['body'].count('tick'); r['body'] = r['body'][:40]
    else:
        r = connect(f'{c}.acme.internal:443')
    r['case'] = c
    print(json.dumps(r), flush=True)
