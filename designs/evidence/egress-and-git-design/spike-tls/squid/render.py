#!/usr/bin/env python3
"""Render a Lenny level 1 policy document (I4) to Squid configuration. Stands in for a deployer's sync job.
render.py policy.json OUTDIR [ssrf=1]"""
import json, os, sys
doc = json.load(open(sys.argv[1])); out = sys.argv[2]; ssrf = "ssrf=1" in sys.argv
pw, acls, rules = [], [], []
for i in doc["identities"]:
    n = i["name"]
    users = []
    if "credentials" in i:
        for c in i["credentials"]:
            pw.append(f"{c['username']}:{c['verifier']}"); users.append(c["username"])
    else:
        for v in i["credentialVerifiers"]:
            pw.append(f"{n}:{v}"); users.append(n)
    open(f"{out}/hosts-{n}.txt", "w").write("\n".join(h.replace("*.", ".") for h in i["hosts"]) + "\n")
    acls += [f"acl id_{n} proxy_auth {' '.join(users)}", f'acl hosts_{n} dstdomain "{out}/hosts-{n}.txt"']
    rules.append(f"http_access allow CONNECT id_{n} hosts_{n} tls_ports")
open(f"{out}/passwd", "w").write("\n".join(pw) + "\n")
conf = f"""
{"https_port 3128 tls-cert=/c/good/tls.crt tls-key=/c/good/tls.key" if "tls=1" in sys.argv else "http_port 3128"}
pid_filename /tmp/squid.pid
cache deny all
cache_log stdio:/dev/stdout
access_log stdio:/tmp/access.log
logfile_rotate 0
auth_param basic program /usr/lib/squid/basic_ncsa_auth {out}/passwd
auth_param basic children 2
auth_param basic realm lenny-level1
auth_param basic credentialsttl {doc.get('credentialsTtlSeconds', 7200)} seconds
acl CONNECT method CONNECT
acl tls_ports port 443
acl authed proxy_auth REQUIRED
{"acl private_dst dst 127.0.0.0/8 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16 100.64.0.0/10 0.0.0.0/8 ::1 fc00::/7 fe80::/10" if ssrf else ""}
{"http_access deny private_dst" if ssrf else ""}
http_access deny !authed
"""
open(f"{out}/squid.conf", "w").write(conf + "\n".join(acls) + "\n" + "\n".join(rules) + "\nhttp_access deny all\n")
