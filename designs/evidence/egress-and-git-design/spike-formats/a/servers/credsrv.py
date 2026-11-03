# Fake pod-local credential agent: serves AWS container-credentials JSON.
import http.server, json, os, sys, time, datetime, itertools
PORT = int(os.environ.get("PORT", "8080"))
TOKEN_FILE = os.environ.get("TOKEN_FILE", "/tok/token")
EXPIRY = int(os.environ.get("EXPIRY_SECONDS", "180"))
NAME = os.environ.get("NAME", "srv")
ctr = itertools.count(1)
def log(**kw):
    kw["t"] = datetime.datetime.now(datetime.timezone.utc).strftime("%H:%M:%S.%f")[:-3]
    print(json.dumps(kw), flush=True)
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        got = self.headers.get("Authorization")
        try:
            want = open(TOKEN_FILE).read()
        except Exception as e:
            want = None
        ok = got is not None and want is not None and got == want.strip()
        if not ok:
            log(ev="fetch", path=self.path, auth=got, want=want, result=401, ua=self.headers.get("User-Agent"))
            self.send_response(401); self.send_header("Content-Type","application/json"); self.end_headers()
            self.wfile.write(b'{"code":"Unauthorized","message":"bad token"}'); return
        n = next(ctr)
        exp = datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(seconds=EXPIRY)
        akid = "AKID%s%04d" % (NAME.upper().replace("-", ""), n)
        body = {"AccessKeyId": akid, "SecretAccessKey": "secret%d" % n,
                "Token": "sesstok-%s-%d" % (NAME, n),
                "Expiration": exp.strftime("%Y-%m-%dT%H:%M:%SZ")}
        log(ev="fetch", path=self.path, auth=got, result=200, akid=akid, exp=body["Expiration"], ua=self.headers.get("User-Agent"))
        b = json.dumps(body).encode()
        self.send_response(200); self.send_header("Content-Type","application/json")
        self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
http.server.ThreadingHTTPServer(("0.0.0.0", PORT), H).serve_forever()
