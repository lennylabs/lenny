#!/usr/bin/env python3
"""render5.py policy.json OUTDIR VARIANT: the reference Squid rendering from round 6, plus one deliberate defect per variant."""
import json,os,sys,re
doc=json.load(open(sys.argv[1])); out=sys.argv[2]; V=sys.argv[3]
# ::ffff:0:0/96 is absent: the first run showed that Squid then refuses every IPv4 destination.
FULL="127.0.0.0/8 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16 100.64.0.0/10 0.0.0.0/8 ::1 64:ff9b::/96 fc00::/7 fe80::/10"
MAPPED=FULL+" ::ffff:0:0/96"
RULES={"correct":FULL,"fakeauth":FULL,"refuseall":FULL,"refuse-hard":FULL,"notls":FULL,"wrongca":FULL,"droplits":None,"droplits-rfc1918":"10.0.0.0/8 172.16.0.0/12 192.168.0.0/16","partialaddr":"127.0.0.0/8 169.254.0.0/16 172.30.50.0/24","noaddr":None,"addrafter":FULL,"noauth":FULL,"noauth-list":FULL,"correct-n":FULL,"mapped-rule":MAPPED,"openlist":FULL,"openlist-norule":None,"droplits-n":None,"droplits-rfc1918-n":"10.0.0.0/8 172.16.0.0/12 192.168.0.0/16","partialaddr-n":"127.0.0.0/8 169.254.0.0/16 172.30.50.0/24"}
rule=RULES[V]
expired=os.path.exists(out+"/expired")
pw,acls,allows,union=[],[],[],[]
lit=re.compile(r"^[0-9.]+$")
for i in doc["identities"]:
    n=i["name"]; hosts=i["hosts"]
    if V.startswith("droplits"): hosts=[h for h in hosts if not lit.match(h)]
    users=[]
    for c in i["credentials"]:
        pw.append(f"{c['username']}:{c['verifier']}"); users.append(c["username"])
    open(f"{out}/hosts-{n}.tmp","w").write("\n".join(h.replace("*.",".") for h in hosts)+"\n"); os.replace(f"{out}/hosts-{n}.tmp",f"{out}/hosts-{n}.txt")
    union+=hosts
    acls+=[f"acl id_{n} proxy_auth {' '.join(users)}",f'acl hosts_{n} dstdomain {"-n " if V.endswith("-n") else ""}"{out}/hosts-{n}.txt"']
    allows.append(f"http_access allow CONNECT id_{n} hosts_{n} tls_ports")
open(f"{out}/passwd.tmp","w").write("\n".join(pw)+"\n"); os.replace(f"{out}/passwd.tmp",f"{out}/passwd")
port={"notls":"http_port 3128","wrongca":"https_port 3128 tls-cert=/c1/unk/tls.crt tls-key=/c1/unk/tls.key"}.get(V,"https_port 3128 tls-cert=/c/proxy/tls.crt tls-key=/c/proxy/tls.key")
c=[port,"pid_filename /tmp/squid.pid","cache deny all","cache_log stdio:/dev/stdout","access_log stdio:/tmp/access.log","logfile_rotate 0","connect_timeout 4 seconds",
 "acl CONNECT method CONNECT","acl tls_ports port 443"]
if rule: c.append("acl private_dst dst "+rule)
if V=="noauth":
    if rule: c.append("http_access deny private_dst")
    c+=["http_access allow CONNECT tls_ports","http_access deny all"]
elif V=="noauth-list":
    open(f"{out}/union.txt","w").write("\n".join(sorted({h.replace('*.','.') for h in union}))+"\n")
    c+=[f'acl union dstdomain "{out}/union.txt"']
    if rule: c.append("http_access deny private_dst")
    c+=["http_access allow CONNECT union tls_ports","http_access deny all"]
else:
    helper="/usr/lib/squid/basic_fake_auth" if V=="fakeauth" else f"/usr/lib/squid/basic_ncsa_auth {out}/passwd"
    c+=[f"auth_param basic program {helper}","auth_param basic children 2","auth_param basic realm lenny-level1",f"auth_param basic credentialsttl {doc.get('credentialsTtlSeconds',7200)} seconds","acl authed proxy_auth REQUIRED"]
    if V=="refuse-hard": c.append("http_access deny all")
    c+=acls if not expired else []
    if expired: c.append("http_access deny all")
    if rule and V!="addrafter": c.append("http_access deny private_dst")
    c.append("http_access deny !authed")
    if V=="refuseall": c.append("http_access deny all")
    if V.startswith("openlist"): c.append("http_access allow CONNECT authed tls_ports")
    if not expired: c+=allows
    if V=="addrafter": c.append("http_access deny private_dst")
    c.append("http_access deny all")
open(f"{out}/squid.tmp","w").write("\n".join(c)+"\n"); os.replace(f"{out}/squid.tmp",f"{out}/squid.conf")
