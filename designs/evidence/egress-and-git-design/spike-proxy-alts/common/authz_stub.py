#!/usr/bin/env python3
"""Decision-service stub standing in for lenny-egress-authz (I3, HTTP JSON form).
POST /v1/decide {"authority":"host:port","host":..,"port":..,"peer":"ip","user":..,"token":..}
 -> {"allow":bool,"route":"direct|upA|upB","tags":{...},"upstream_user":..,"upstream_password":..}
POST /v1/revoke {"token":..}; GET /v1/stats"""
import json, fnmatch, threading, sys, time
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler

LEASES = {
  "lease-A": {"tenant": "acme",   "session": "s-a", "hosts": ["*"],                      "route": "direct"},
  "lease-B": {"tenant": "globex", "session": "s-b", "hosts": ["example.com", "spike-lab"], "route": "upB",
              "upstream_user": "tenantB", "upstream_password": "secretB"},
  "lease-C": {"tenant": "initech","session": "s-c", "hosts": ["example.com"],             "route": "upA"},
}
REVOKED = set(); CALLS = []; LOCK = threading.Lock()

class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def _send(self, code, obj):
        b = json.dumps(obj).encode(); self.send_response(code)
        self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(b)))
        self.end_headers(); self.wfile.write(b)
    def do_GET(self):
        with LOCK: self._send(200, {"calls": len(CALLS), "last": CALLS[-10:], "revoked": sorted(REVOKED)})
    def do_POST(self):
        req = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
        if self.path == "/v1/revoke":
            with LOCK: REVOKED.add(req["token"])
            return self._send(200, {"ok": True})
        if self.path == "/v1/reset":
            with LOCK: REVOKED.clear(); CALLS.clear()
            return self._send(200, {"ok": True})
        tok, host = req.get("token"), req.get("host", "")
        lease = LEASES.get(tok)
        if lease is None or tok in REVOKED:
            resp = {"allow": False, "reason": "revoked" if tok in REVOKED else "unknown-lease"}
        elif not any(fnmatch.fnmatch(host, p) for p in lease["hosts"]):
            resp = {"allow": False, "reason": "host-not-allowed", "tags": {"tenant": lease["tenant"]}}
        else:
            resp = {"allow": True, "route": lease["route"],
                    "tags": {"tenant": lease["tenant"], "session": lease["session"], "rule": "r1"}}
            for k in ("upstream_user", "upstream_password"):
                if k in lease: resp[k] = lease[k]
        with LOCK: CALLS.append({"t": round(time.time(), 2), "req": req, "resp": resp})
        print(json.dumps({"decide": req, "resp": resp}), flush=True)
        self._send(200, resp)

if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", int(sys.argv[1]) if len(sys.argv) > 1 else 8080), H).serve_forever()
