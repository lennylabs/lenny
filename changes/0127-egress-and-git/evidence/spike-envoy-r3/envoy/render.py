#!/usr/bin/env python3
"""Render envoy.tmpl.yaml into a variant. Usage: render.py OUT [key=value ...]

Keys:
  hcm=<yaml flow map entries>   e.g. hcm='stream_idle_timeout: 5s'
  route=<yaml>                  e.g. route='max_stream_duration: {max_stream_duration: 5s}'
  filter=on|off                 resolved_address_filter (default on)
  family=V4_ONLY|ALL|V4_PREFERRED (default ALL)
  nomapped=1                    drop ::ffff:0:0/96 and 64:ff9b::/96 from the filter
  hygiene=1                     strip internal headers on forwarded-CONNECT routes
  proxy_a=<ip>                  proxy A address (default 198.18.0.12)
"""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
BLOCK = [
    ("0.0.0.0", 8), ("10.0.0.0", 8), ("100.64.0.0", 10), ("127.0.0.0", 8),
    ("169.254.0.0", 16), ("172.16.0.0", 12), ("192.0.0.0", 24), ("192.168.0.0", 16),
    ("224.0.0.0", 4), ("240.0.0.0", 4),
    ("::", 128), ("::1", 128), ("::ffff:0:0", 96), ("64:ff9b::", 96),
    ("fc00::", 7), ("fe80::", 10), ("ff00::", 8),
]


def main():
    out = sys.argv[1]
    kv = dict(a.split("=", 1) for a in sys.argv[2:])
    t = open(os.path.join(HERE, "envoy.tmpl.yaml")).read()
    hcm = kv.get("hcm", "")
    route = kv.get("route", "")
    block = [b for b in BLOCK if not (kv.get("nomapped") and b[0] in ("::ffff:0:0", "64:ff9b::"))]
    if kv.get("filter", "on") == "on":
        rng = ", ".join(f"{{address_prefix: '{a}', prefix_len: {p}}}" for a, p in block)
        filt = f"resolved_address_filter: {{ranges: [{rng}]}}"
    else:
        filt = ""
    t = t.replace("__HCM_TIMEOUTS__", hcm)
    t = t.replace("__ROUTE_TIMEOUTS__", route)
    t = t.replace("__ADDR_FILTER__", filt)
    t = t.replace("__DNS_FAMILY__", kv.get("family", "ALL"))
    if kv.get("hygiene") == "1":
        t = t.replace("__FWD_HDRS__", "request_headers_to_remove: [x-request-id, x-forwarded-proto, x-lenny-route, x-lenny-upstream, user-agent]")
        t = t.replace("__ROUTER__", "suppress_envoy_headers: true")
    t = t.replace("__FWD_HDRS__", "").replace("__ROUTER__", "")
    t = t.replace("__PROXY_A_ADDR__", kv.get("proxy_a", "198.18.0.12"))
    open(out, "w").write(t)


if __name__ == "__main__":
    main()
