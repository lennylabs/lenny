# Logging HTTP server: records method, path, and selected headers; answers 404 (or a minimal LFS batch response).
import http.server, sys, json
LOG = sys.argv[2] if len(sys.argv) > 2 else "/dev/stdout"
class H(http.server.BaseHTTPRequestHandler):
    def _h(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(n) if n else b""
        with open(LOG, "a") as f:
            f.write(json.dumps({"m": self.command, "path": self.path,
                "ua": self.headers.get("User-Agent"),
                "accept": self.headers.get("Accept"),
                "gp": self.headers.get("Git-Protocol"),
                "auth": self.headers.get("Authorization"),
                "body": body[:400].decode("utf-8", "replace")}) + "\n")
        self.send_response(404); self.send_header("Content-Length", "0"); self.end_headers()
    do_GET = do_POST = do_PUT = _h
    def log_message(self, *a): pass
http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
