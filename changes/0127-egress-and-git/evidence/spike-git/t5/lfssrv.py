# Logging server for LFS: answers the batch API with actions on https://storage.example.com,
# logs every request (including CONNECT when used as an HTTP proxy), 404 otherwise.
import http.server, sys, json
LOG = sys.argv[2]
class H(http.server.BaseHTTPRequestHandler):
    def _h(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(n) if n else b""
        rec = {"port": self.server.server_address[1], "m": self.command, "path": self.path,
               "accept": self.headers.get("Accept"), "auth": self.headers.get("Authorization")}
        if body.startswith(b"{"):
            j = json.loads(body); rec["op"] = j.get("operation"); rec["ref"] = j.get("ref")
        open(LOG, "a").write(json.dumps(rec) + "\n")
        if self.path.endswith("/info/lfs/objects/batch"):
            objs = [{"oid": o["oid"], "size": o["size"], "authenticated": True,
                     "actions": {j["operation"]: {"href": "https://storage.example.com/lfs/" + o["oid"], "header": {"X-Test": "1"}}}}
                    for o in j["objects"]]
            out = json.dumps({"transfer": "basic", "objects": objs}).encode()
            self.send_response(200); self.send_header("Content-Type", "application/vnd.git-lfs+json")
            self.send_header("Content-Length", str(len(out))); self.end_headers(); self.wfile.write(out); return
        self.send_response(404); self.send_header("Content-Length", "0"); self.end_headers()
    do_GET = do_POST = do_PUT = do_CONNECT = _h
    def log_message(self, *a): pass
http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
