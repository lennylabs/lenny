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


def _window_scrubber(secret: bytes, ph: bytes):
    """Streaming scrubber: holds back len(secret)-1 bytes so a secret split across chunks is still caught."""
    keep = len(secret) - 1
    buf = bytearray()
    def f(chunk: bytes) -> bytes:
        nonlocal buf
        if chunk == b"":                      # end of stream: flush the held-back tail
            out = bytes(buf).replace(secret, ph); buf = bytearray(); return out
        buf += chunk
        data = bytes(buf).replace(secret, ph)
        if len(data) <= keep:
            buf = bytearray(data); return b""
        out, buf = data[:-keep], bytearray(data[-keep:])
        return out
    return f

def responseheaders(flow: http.HTTPFlow):
    b = B.get(flow.request.pretty_host)
    if not b:
        return
    ct = flow.response.headers.get("content-type", "")
    if "text/event-stream" in ct or flow.request.query.get("stream") == "1":
        if flow.response.headers.get("content-encoding"):
            return  # compressed stream: leave buffered (scrubbed after decode)
        flow.response.stream = _window_scrubber(b["secret"].encode(), b["placeholder"].encode())

def websocket_message(flow: http.HTTPFlow):
    b = B.get(flow.request.pretty_host)
    if not b:
        return
    m = flow.websocket.messages[-1]
    if not m.from_client and b["secret"].encode() in m.content:
        m.content = m.content.replace(b["secret"].encode(), b["placeholder"].encode())
