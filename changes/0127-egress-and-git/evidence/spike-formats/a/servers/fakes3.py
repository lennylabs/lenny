# Fake S3: logs SigV4 Authorization header and returns minimal ListBuckets XML.
import http.server, json, re, datetime
XML = b'<?xml version="1.0" encoding="UTF-8"?><ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Owner><ID>o</ID><DisplayName>o</DisplayName></Owner><Buckets><Bucket><Name>spike-bucket</Name><CreationDate>2026-01-01T00:00:00.000Z</CreationDate></Bucket></Buckets></ListAllMyBucketsResult>'
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def handle_any(self):
        n = int(self.headers.get("Content-Length") or 0)
        if n: self.rfile.read(n)
        a = self.headers.get("Authorization") or ""
        m = re.search(r"Credential=([^/]+)/", a)
        print(json.dumps({"t": datetime.datetime.now(datetime.timezone.utc).strftime("%H:%M:%S.%f")[:-3],
            "ev": "s3", "method": self.command, "path": self.path, "akid": m.group(1) if m else None,
            "sigv4": a.startswith("AWS4-HMAC-SHA256"),
            "sectok": self.headers.get("X-Amz-Security-Token"), "ua": (self.headers.get("User-Agent") or "")[:80]}), flush=True)
        self.send_response(200); self.send_header("Content-Type", "application/xml")
        self.send_header("Content-Length", str(len(XML))); self.end_headers(); self.wfile.write(XML)
    do_GET = do_PUT = do_POST = do_HEAD = handle_any
http.server.ThreadingHTTPServer(("0.0.0.0", 9000), H).serve_forever()
