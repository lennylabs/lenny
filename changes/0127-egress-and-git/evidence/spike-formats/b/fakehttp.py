# Fake HTTP server for .netrc tests. Paths /t/<tag>/... return 401 (Basic challenge)
# without Authorization and 200/404 with it. Logs every request.
import http.server, json, datetime, base64
def pkt(s): b = s.encode(); return b"%04x" % (len(b) + 4) + b
ADV = (pkt("# service=git-upload-pack\n") + b"0000" +
       pkt("1111111111111111111111111111111111111111 refs/heads/main\x00side-band-64k\n") + b"0000")
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        a = self.headers.get("Authorization")
        dec = None
        if a and a.startswith("Basic "):
            try: dec = base64.b64decode(a[6:]).decode()
            except Exception: dec = "?"
        parts = self.path.split("/")
        tag = parts[2] if len(parts) > 2 and parts[1] == "t" else None
        print(json.dumps({"t": datetime.datetime.now(datetime.timezone.utc).strftime("%H:%M:%S.%f")[:-3],
              "tag": tag, "path": self.path, "auth": a, "basic": dec,
              "ua": (self.headers.get("User-Agent") or "")[:60]}), flush=True)
        if not a:
            self.send_response(401); self.send_header("WWW-Authenticate", 'Basic realm="fake"')
            self.send_header("Content-Length", "0"); self.end_headers(); return
        if "info/refs" in self.path:
            self.send_response(200); self.send_header("Content-Type", "application/x-git-upload-pack-advertisement")
            self.send_header("Content-Length", str(len(ADV))); self.end_headers(); self.wfile.write(ADV); return
        if "/simple/" in self.path:
            b = b"<html><body></body></html>"
            self.send_response(200); self.send_header("Content-Type", "text/html")
            self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b); return
        if "/plain" in self.path:
            b = b"ok\n"
            self.send_response(200); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b); return
        self.send_response(404); self.send_header("Content-Length", "0"); self.end_headers()
    do_HEAD = do_GET
import os, ssl, threading
if os.path.exists("/b/tls/srv.crt"):
    ts = http.server.ThreadingHTTPServer(("0.0.0.0", 443), H)
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER); ctx.load_cert_chain("/b/tls/srv.crt", "/b/tls/srv.key")
    ts.socket = ctx.wrap_socket(ts.socket, server_side=True)
    threading.Thread(target=ts.serve_forever, daemon=True).start()
http.server.ThreadingHTTPServer(("0.0.0.0", 80), H).serve_forever()
