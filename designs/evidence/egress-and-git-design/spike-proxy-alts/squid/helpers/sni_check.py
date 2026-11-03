#!/usr/bin/env python3
# external ACL for ssl_bump step2: input "%>rd %ssl::>sni"; OK when SNI equals CONNECT host.
import sys, urllib.parse
for line in sys.stdin:
    f = [urllib.parse.unquote(x) for x in line.split()] + ["-", "-"]
    sys.stderr.write(f"sni_check.py IN: connect={f[0]} sni={f[1]}\n"); sys.stderr.flush()
    sys.stdout.write(("OK" if f[0].lower() == f[1].lower() else "ERR log=sni-mismatch") + "\n"); sys.stdout.flush()
