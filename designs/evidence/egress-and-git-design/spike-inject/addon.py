"""Spike addon: plain-HTTP upgrade, header attachment, and response scrubbing for bound hosts.

Bindings mirror the design's credential bindings: host -> (header, value, placeholder).
Hosts not listed are never intercepted (mitmproxy allow_hosts), so this addon only sees bound hosts.
"""
import base64, json, os
from mitmproxy import http, ctx

B = json.load(open("/cfg/bindings.json"))  # {host: {"header":..., "value":..., "placeholder":...}}

def request(flow: http.HTTPFlow):
    b = B.get(flow.request.pretty_host)
    if not b:
        return
    if flow.request.scheme == "http":           # plain-HTTP transport: upgrade on the way out
        flow.request.scheme = "https"
        flow.request.port = 443
    flow.request.headers[b["header"]] = b["value"]   # overwrite, never substitute inside text

def response(flow: http.HTTPFlow):
    b = B.get(flow.request.pretty_host)
    if not b or flow.response is None:
        return
    secret, ph = b["secret"], b["placeholder"]
    for k, v in list(flow.response.headers.items(multi=True)):
        if secret in v:
            flow.response.headers[k] = v.replace(secret, ph)
    if flow.response.stream:
        return
    try:
        body = flow.response.get_text(strict=False)
    except Exception:
        return
    if body and secret in body:
        flow.response.text = body.replace(secret, ph)
        ctx.log.info(f"SCRUBBED {flow.request.pretty_host}{flow.request.path}")
