# Injection spike results (round 3b, 2026-11-01)

Subject: mitmproxy 12.3.1 (Python 3.15, OpenSSL 4.0.3) standing in for `netagent`, configured to intercept only bound hosts (`allow_hosts`), with `cfg/addon.py` for the plain-HTTP upgrade, header overwrite, and response scrubbing. Fake upstream `bin/fakeup` (source `src/cmd/fakeup`) serves `api`, `ghes`, `registry`, `pypi`, and `other` `.example.test` with a separate "public" test CA (`pub/`). Clients ran on an internal network (`inj-pod`) that reaches only mitmproxy; mitmproxy and the upstream share a second internal network (`inj-up`). Bindings: `cfg/bindings.json`.

Clients trusted `shared/trust.pem` (public test CA, standing in for system roots, plus the mitmproxy CA). Java used `shared/trust.p12`.

| Test | Result | Evidence |
| :-- | :-- | :-- |
| Direct egress from client network | Blocked | curl: resolving timed out |
| Bound host, placeholder header | PASS | upstream got `Bearer ACME-REAL-…`; client sent `lenny-ph-acme` |
| Bound host, no header | PASS | header attached anyway (overwrite) |
| Plain-HTTP upgrade (`http://api…` via `http_proxy`) | PASS | upstream got TLS request with real secret; no CA needed on client. curl reads only lowercase `http_proxy`. `Proxy-Connection` header leaked upstream (netagent must strip hop-by-hop headers). |
| Unbound host | PASS | not intercepted (issuer = public CA); placeholder forwarded unchanged |
| Placeholder in query to bound host | PASS | not replaced |
| HTTP/2 | PASS | curl, Go, Java negotiated h2 with mitmproxy and upstream |
| Scrub: plain, forced gzip, negotiated gzip, deflate, 5-byte chunks | PASS | real secret never visible to client; reflected header scrubbed |
| Scrub: base64 / hex reflection | Defeated (expected) | decoded body reveals secret |
| SSE with buffered scrubbing | FAIL for streaming | all events arrived together at +2.05 s |
| SSE with window scrubber (holds back len(secret)-1 bytes) | PASS | events at +0.42, 0.82, 1.23, 1.63, 2.04 s, scrubbed; split-chunk reflection also scrubbed |
| WebSocket | PASS after fix | upgrade works; reflected secret in a server frame reached client until `websocket_message` scrubbing was added |
| CA persistence across restart | PASS | same SHA-256 fingerprint before and after `docker restart` (confdir on a volume) |
| curl | PASS | |
| Python requests, httpx (h1), aiohttp `trust_env=True` | PASS | httpx h2 not tested (h2 package absent) |
| Node 24 fetch (`NODE_USE_ENV_PROXY=1`, `NODE_EXTRA_CA_CERTS`) | PASS | |
| Go net/http (`SSL_CERT_FILE`) | PASS | HTTP/2.0 |
| Java 21 HttpClient (`JAVA_TOOL_OPTIONS` proxy + PKCS12 truststore) | PASS | HTTP_2 |
| gh 2.103.3 (`GH_HOST`, `GH_ENTERPRISE_TOKEN=lenny-ph-gh`, `SSL_CERT_FILE`) | PASS | upstream got `token GH-REAL-…` |
| npm 10 (`.npmrc` `_authToken=lenny-ph-npm`, `NODE_EXTRA_CA_CERTS`) | PASS | package installed |
| pip (index URL `pypiuser:lenny-ph-pypi@…`, `PIP_CERT`) | PASS | upstream got Basic with real secret; package installed |
| Latency, new connection per request | +2.7 ms p50 | passthrough 3.9 ms p50, intercepted 6.6 ms p50 |
| Memory | 61 MiB RSS | mitmproxy (Python); not representative of a Go netagent |

Not tested: rustls/webpki-roots clients, pinning clients, request signing (SigV4), mTLS client certs. These are known not to work with injection and are documented as limits.

Cleanup: containers `inj-mitm`, `inj-fake`, networks `inj-pod`, `inj-up`, and image `inj-py` removed. Pulled images kept.
