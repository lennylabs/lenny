#!/usr/bin/env python3
"""Squid external_acl_type helper (Lenny I3 HTTP JSON client).
Input line: %>rd %>rP %>a %ul %>h{Proxy-Authorization}
Output: OK|ERR tag=<tenant> log=<session/rule> route=<route> [user= password=] message=..."""
import sys, json, base64, urllib.parse, urllib.request
URL = "http://spike-authz:8080/v1/decide"
def q(v): return urllib.parse.quote(str(v), safe="")
for line in sys.stdin:
    sys.stderr.write("decide.py IN: " + line); sys.stderr.flush()
    f = [urllib.parse.unquote(x) for x in line.split()]
    while len(f) < 5: f.append("-")
    # with auth_param the format is "%>rd %>rP %>a %ul %>h{Proxy-Authorization}"; without it %ul is omitted
    if f[3].startswith("Basic"): host, port, peer, authz = f[:4]; user = "-"
    else: host, port, peer, user, authz = f[:5]
    token = None
    if authz.startswith("Basic "):
        try: token = base64.b64decode(authz[6:]).decode().split(":", 1)[1]
        except Exception: pass
    try:
        req = urllib.request.Request(URL, data=json.dumps({"authority": f"{host}:{port}", "host": host, "port": port,
              "peer": peer, "user": user, "token": token}).encode(), headers={"Content-Type": "application/json"})
        r = json.load(urllib.request.urlopen(req, timeout=2))
    except Exception as e:
        sys.stdout.write(f"BH message={q(e)}\n"); sys.stdout.flush(); continue
    t = r.get("tags", {})
    out = ["OK clt_conn_tag=" + q("lenny-approved-" + r.get("route", "direct")) if r.get("allow") else "ERR"]
    if t.get("tenant"): out.append("tag=" + q(t["tenant"]))
    out.append("log=" + q(f"tenant={t.get('tenant','-')},session={t.get('session','-')},rule={t.get('rule','-')},reason={r.get('reason','-')}"))
    if r.get("route"): out.append("route_=" + q(r["route"]))
    if r.get("upstream_user"): out += ["user=" + q(r["upstream_user"]), "password=" + q(r["upstream_password"])]
    if not r.get("allow"): out.append("message=" + q(r.get("reason", "denied")))
    sys.stdout.write(" ".join(out) + "\n"); sys.stdout.flush()
