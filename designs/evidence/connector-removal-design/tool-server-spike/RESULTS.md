# Tool-server spike: native MCP through a netagent stand-in

Date: 2026-11-15. Host: macOS 27.2 (arm64), local loopback only. No cloud resources, no Docker, and no edits under `/Users/joan/projects/lenny`. All paths below are relative to this directory:
`/private/tmp/claude-501/-Users-joan-projects-lenny/c1165b35-7597-45a5-b0fc-caf23493501b/scratchpad/tool-server-spike/`.

## 1. Versions and the MCP protocol state

| Component | Version | Source |
|---|---|---|
| MCP specification, current revision | `2026-07-28` | https://modelcontextprotocol.io/specification/versioning ("The current protocol version is 2026-07-28") |
| TypeScript client / server SDK (v2 line) | `@modelcontextprotocol/client` 2.4.0, `@modelcontextprotocol/server` 2.4.0, `@modelcontextprotocol/node` 2.1.2 | npm `latest`, checked 2026-11-15 |
| TypeScript SDK v1 line (handshake-era server) | `@modelcontextprotocol/sdk` 1.32.2 | npm `latest` |
| Python SDK | `mcp` 2.3.0 (uses `httpx2` 2.16.0) | PyPI latest |
| Node / Go / Python / OpenSSL | 22.22.0 / 1.26.3 / 3.13.13 / 3.6.2 | `logs/setup/versions.log` |

The revision changes the problem the owner described. `2026-07-28` (changelog: https://modelcontextprotocol.io/specification/2026-07-28/changelog) removes the `initialize` handshake, `Mcp-Session-Id`, the HTTP GET stream, DELETE, SSE resumability (`Last-Event-ID`), and server-to-client JSON-RPC requests on SSE. Elicitation, sampling, and roots become Multi Round-Trip Requests: the server returns `resultType: "input_required"` and the client re-issues the call with a new request id. Every POST carries `MCP-Protocol-Version`, `Mcp-Method`, and, for `tools/call`, `Mcp-Name` headers that mirror the body ("so that intermediaries ... can route and inspect requests without parsing the body", https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http). Tasks moved out of core into the `io.modelcontextprotocol/tasks` extension (https://modelcontextprotocol.io/extensions/tasks/overview).

Both SDKs are dual-era. The TypeScript client defaults to the handshake era (`versionNegotiation.mode` defaults to `'legacy'`, which sends 2025-11-25). The Python client defaults to `mode="auto"` (it probes `server/discover`). A netagent MCP rule therefore has to handle both eras for the foreseeable future, and this spike exercised both: the "legacy" upstream is a stateful SDK 1.32.2 server (sessions, GET stream, DELETE, in-memory event store), and the "modern" upstream is an SDK 2.4.0 `createMcpHandler` server (stateless 2026-07-28, also serving 2025-era clients statelessly).

**JSON-RPC batching.** No current revision permits it. The body of a POST "MUST be a single JSON-RPC request or notification" in 2026-07-28 (transport page above); batching was removed in 2025-06-18. Measured behavior differs by server: the SDK 2.4.0 server rejects a batch containing requests with 400 (`logs/04-edge-cases/curl-edge.out`, case 5), while the SDK 1.32.2 server still accepts and executes a batch on a 2025-11-25 session and on a 2025-03-26 session (cases 12 and 13). A proxy must therefore either parse batches or refuse them; it cannot assume they never occur.

## 2. What was built

| Part | File | Notes |
|---|---|---|
| netagent stand-in | `src/proxy/main.go` (Go stdlib only; binary `bin/proxy`) | HTTP CONNECT proxy. Default-deny on unbound hosts. Terminates TLS for bound hosts with a generated per-pod CA (`certs/proxy-ca.pem`, ECDSA P-256), re-originates TLS to the upstream with the upstream CA. Inspection stage: 1 MiB bounded body reader, JSON-RPC classification, method and notification allowlists (fail closed), `tools/call` deny/allow by tool name, header/body cross-check (`Mcp-Method`, `Mcp-Name` incl. base64 sentinel, `MCP-Protocol-Version`), client JSON-RPC responses admitted only for ids of server-initiated requests seen on a stream. Response side: event-by-event SSE parser that flushes each event, `tools/list` filtering, audit correlation, credential redaction. Credential injection (strips any client `Authorization`, sets `Bearer <token>`). Approval hold with keepalive modes `off`, `comment` (SSE comment every 5 s), and `progress` (proxy-authored `notifications/progress` every 5 s on the request's `progressToken`). Fault injection (cut an SSE stream after N events). |
| Config writer | `src/mkcfg.py` | Allowed methods: `initialize`, `ping`, `server/discover`, `tools/list`, `tools/call`, `resources/list`, `resources/templates/list`, `prompts/list`, `subscriptions/listen`. Allowed notifications: `notifications/initialized`, `notifications/cancelled`, `notifications/roots/list_changed`. Denied tool: `danger`. |
| Upstreams | `src/server/server-legacy.mjs` (SDK 1.32.2; port 8443 SSE mode, port 8444 JSON mode), `src/server/server-modern.mjs` (SDK 2.4.0; port 9443) | HTTPS with `certs/upstream.pem`. Tools: `fast`, `fast_progress` (one progress then result), `slow` (progress per step), `structured` (outputSchema + structuredContent), `confirm_action` (elicitation; legacy: `elicitInput` on the SSE stream; modern: `inputRequired`), `danger`, `whoami` (returns SHA-256 prefix of the Authorization header the server received), `echo_auth_error` (fails and echoes the Authorization header, simulating a leaky server). `EXTRA_TOOLS=N` adds padding tools. |
| Clients | `src/clients/ts-client.mjs` (TS 2.4.0), `src/clients/py_client.py` (Python mcp 2.3.0, run with `-I`) | Proxy set through `NODE_USE_ENV_PROXY=1 HTTPS_PROXY=... NODE_EXTRA_CA_CERTS=certs/proxy-ca.pem` (Node) and `HTTPS_PROXY=... SSL_CERT_FILE=certs/proxy-ca.pem` (Python, SDK default httpx2 client). Clients trust only the proxy CA, so a successful call proves it traversed the proxy. |
| Drivers | `src/servers-up.sh`, `src/proxy-up.sh`, `src/curl-edge.sh`, `src/hold-matrix.sh`, `src/bench.sh`, `src/summarize.py` | |

mitmproxy 12.3.1 (current PyPI release) was not used. A held call that the proxy keeps alive with its own SSE events requires the proxy to commit the client response before the upstream request exists and then splice the upstream response into it; a small Go program does that directly, has no third-party dependencies, and is closer to what netagent would be.

Bound hosts: `mcp-legacy.lenny.test:8443`, `mcp-legacyjson.lenny.test:8444`, `mcp-modern.lenny.test:9443`. The proxy resolves them to 127.0.0.1 itself; the clients never resolve them.

## 3. Answers

### Q1. Request-side parsing and response streaming

Measured, both eras, both client SDKs (`logs/01-functional-ts`, `logs/02-functional-py`, `logs/04-edge-cases`, `logs/06-resumability`):

- Single JSON-RPC message per POST: parsed and classified on every request (request, notification, client response). Request-side inspection costs p50 10.2 µs, p95 17.1 µs, p99 27.8 µs per POST (n=6,636; `logs/08-overhead/inspect-ns.txt`).
- Batches: parsed and evaluated element by element. A batch with a denied element is refused as a whole; a fully allowed batch is forwarded (case 4 and 5; legacy cases 12 and 13 executed and audited the batched `tools/call`). Simplification: the refusal carries only the offending element's error rather than one response per element.
- GET (2025-11-25 server stream), DELETE, `Mcp-Session-Id`, and `Last-Event-ID` pass through. GET to the 2026-07-28 server returns the server's own 405 (case 8).
- `MCP-Protocol-Version`, `Mcp-Method`, `Mcp-Name`: present on every 2026-07-28 POST from both clients. The proxy decides on the body and rejects a header/body mismatch with -32020 (`HeaderMismatch`), including a lying `Mcp-Name` (case 1), a version mismatch (case 11), and a base64-sentinel name (case 3, allowed). A client that omits the headers is still judged on the body (case 2).
- Responses as JSON and as SSE: both seen and handled. The 2026-07-28 server answers JSON unless the tool emits a notification first, then SSE. The 1.32.2 server always answers SSE (with a priming event that has an `id` and empty `data`) unless JSON mode is set.
- Progress notifications stream through event by event. `slow` (3 steps over 3 s) delivered all 3 progress events and the result to both clients in both eras; held calls delivered 14 (70 s) and 60 (300 s) proxy-authored progress events at 5 s spacing, which also shows the proxy flushes per event.
- Server-to-client requests (2025-11-25 elicitation on the POST's SSE stream): passed through; the proxy recorded `server-request elicitation/create id 0` and admitted the client's JSON-RPC response POST for that id only (`logs/01-functional-ts/proxy-main.events.jsonl`). An unsolicited client response is refused with 403 (case 6). In 2026-07-28 the same tool runs as MRTR: two `tools/call` POSTs with different ids, which the audit shows as `input_required` followed by `ok`.
- Resumability (2025-11-25): the proxy cut the `slow` SSE stream after 3 events. Both clients reconnected with GET and `Last-Event-ID`; the server replayed the rest; both clients received all 4 progress events and the result. The proxy correlated the response that arrived on the GET stream (`response_transport: sse-get`, `stream_broken_before_response: true`). This requires the correlation key to be `(Mcp-Session-Id, request id)` across streams.
- 2026-07-28 has no resumption. On the same cut, the Python client failed immediately (`MCPError -32000 "SSE stream ended without a response"`); the TypeScript client did not notice and waited for its 60 s request timeout. Neither re-issued the request. The proxy writes a `stream-ended-without-response` audit record in that case.

Failures and gaps: the SDK 1.32.2 server in JSON mode cannot deliver an elicitation request (the call hangs to the client timeout). This reproduces without the proxy (`ts-client` direct to `localhost:8444`) and is an upstream SDK limitation. The SSE parser handles `\n` and `\r\n` line endings; bare `\r` was not implemented.

### Q2. Rule form and refusals

Rule used: method allowlist plus notification allowlist (everything else refused), `tools/call` denylist by tool name (allowlist mode implemented, not exercised), body-authoritative with header cross-check. Unknown methods (`evil/method`) and notifications (`notifications/evil`) were refused; lifecycle and listing methods worked.

Refusal shape, measured with `danger` and `evil/method` (`logs/03-refusal-modes`). In every mode the session stayed usable: the following `tools/call fast` succeeded.

| Proxy refusal | TS client 2.4.0 | Python mcp 2.3.0 |
|---|---|---|
| HTTP 200, JSON-RPC error with the request id (`-32010` tool denied, `-32601` method) | throws `ProtocolError` with `code` and `message` | raises `MCPError` with `code` and `message` |
| HTTP 403 with the same JSON-RPC error body | throws `SdkHttpError` `CLIENT_HTTP_NOT_IMPLEMENTED`, "Error POSTing to endpoint: {json}"; the JSON-RPC code is only inside the message string | parses the body and raises `MCPError -32010` (same as above) |
| HTTP 200, `CallToolResult` with `isError: true` and text "Refused by the Lenny egress policy: ..." (`tools/call` only) | returns a normal result | returns a normal result |

For the model, the `isError` tool result is the only form that reaches it as tool output without depending on harness error handling: both SDKs return it as an ordinary result, and harnesses already place `isError` results in the transcript. A JSON-RPC error becomes an exception whose rendering is up to the harness; this was not measured with a real harness. Recommended: `isError` result for denied tools, JSON-RPC error with the request id (HTTP 200) for denied methods, and HTTP 403 only for refusals that are not requests (notifications, stray responses, oversize bodies use 413).

`tools/list` filtering works on JSON and SSE responses: with `danger` denied, both eras listed 507 of 508 tools without it (`logs/05-toolslist-filter/rawlist.out`). Cost, with 508 tools (178 KB body), measured in the proxy: decode, filter, and re-encode is p50 6.0 ms (JSON) and 6.8 ms (SSE) against 1.6 ms and 1.9 ms for parse-only inspection; end to end, `tools/list` p50 rose from 9.5 to 14.6 ms (legacy SSE) and from 14.8 to 19.2 ms (modern JSON). On SSE the proxy must buffer one whole event (the event carrying the list) before forwarding; it does not buffer the stream. The implementation uses generic `map[string]any` decoding; a streaming filter would be cheaper. Two caveats: 2026-07-28 list results carry `ttlMs`/`cacheScope` and the TS 2.4.0 client caches `tools/list`, so a policy change takes effect after the client's cache expires; and hiding a tool is not enforcement, so the `tools/call` rule must still deny it.

### Q3. Audit

One record per `tools/call` is written to `proxy-*.audit.jsonl` with tool, request id, session, era, `arg_bytes`, status (`ok`, `tool-error`, `rpc-error <code>`, `input_required`, `denied`, `http-<status>`, `stream-ended-without-response`, `client-gone-during-hold`), `duration_ms`, `held_ms`, `progress_events`, `structured`, `result_bytes`, `response_transport` (`json`, `sse`, `sse-get`, `hold`), and `stream_broken_before_response`. Correlation is by request id: per request for sessionless traffic, by `(Mcp-Session-Id, id)` for 2025-11-25 sessions, so a response that arrives on a resumed GET stream is matched. Measured on JSON, SSE, resumed SSE, batched, denied, and MRTR calls (examples in `logs/01-functional-ts/proxy-main.audit.jsonl`, `logs/04-edge-cases/proxy-edge.audit.jsonl`, `logs/06-resumability/proxy-cut.audit.jsonl`).

Gaps: an MRTR call produces one record per round (different ids); joining them needs `requestState` or the tool name plus timing. Progress events that arrive on a resumed GET stream are not counted against the call. A client that leaves exactly when the hold is released produces no record (seen once in the 300 s run).

### Q4. Approval hold

Matrix: hold of 10, 70, and 300 s on `tools/call fast`, both eras, both clients (`logs/07-approval-hold/summary.md`, per-run output in `run-*.out`). Keepalive modes: `off` (proxy writes nothing until release), `comment` (SSE response committed at once, `: awaiting approval` every 5 s), `progress` (SSE committed, `notifications/progress` on the request's token every 5 s, progress values 1e-6, 2e-6, ... so a later upstream value still increases).

| Client configuration | 10 s off | 70 s off | 300 s off | 70 s comment | 300 s comment | 70 s progress | 300 s progress |
|---|---|---|---|---|---|---|---|
| TS 2.4.0 defaults (no `onprogress`) | ok 10.0 s | timeout at 60.0 s | timeout at 60.0 s | timeout at 60.0 s | timeout at 60.0 s | n/a (no token sent) | n/a |
| TS `onprogress`, no `resetTimeoutOnProgress` | | | | | | timeout at 60.0 s | timeout at 60.0 s |
| TS `onprogress` + `resetTimeoutOnProgress: true` | | | | | | ok 70.0 s, 14 progress | ok 300.0 s, 60 progress |
| Python mcp 2.3.0 defaults | ok 10.0 s | ok 70.0 s | fails at 300 s: `httpx2.ReadTimeout` raised as an unhandled `ExceptionGroup` | ok 70.0 s | ok 300.0 s | | |
| Python with `progress_callback` | | | | | | ok 70.0 s, 14 progress | ok 300.0 s, 60 progress |

Each cell was run once per era; legacy and modern results were identical, so they are merged.

SDK defaults, from the installed sources: TS `DEFAULT_REQUEST_TIMEOUT_MSEC = 60000`; `resetTimeoutOnProgress` exists and defaults to `false`; `maxTotalTimeout` exists with no default. Python `read_timeout_seconds` defaults to `None` (no SDK-level request timeout); the transport's httpx2 client has 30 s connect/write/pool and a 300 s read timeout, which any byte (an SSE comment included) resets. The TS client sends a `progressToken` only when the caller passes `onprogress`, so the proxy cannot emit progress for a default TS caller; in that case it falls back to comments, which the TS timer ignores.

Conclusions. A hold of a few seconds is safe. Beyond 60 s, the TS SDK caller decides the outcome: it must pass `onprogress` and `resetTimeoutOnProgress: true`, and then proxy-authored progress keeps the call alive indefinitely. Python survives any hold if the proxy sends a comment or progress event at least every 300 s. Claude Code documents (not measured here; https://code.claude.com/docs/en/mcp) a per-request timer of max(60 s, the server's `timeout`, `MCP_TIMEOUT`) for HTTP servers, a hard per-call wall clock that "progress notifications from the server don't extend", and a 5-minute idle window that a progress notification does reset. Proxy-authored progress is protocol-compliant only within limits: the spec says the server MAY send progress and values MUST increase, so the proxy must use values below anything the upstream will send, and on 2025-11-25 sessions a proxy event carries no SSE `id` (it is not replayed on resumption).

On client timeout, both TS and Python closed the HTTP request; the proxy saw the disconnect (`hold-client-gone` at 59.99 s), did not forward the call, and audited `client-gone-during-hold`. The proxy therefore never executes a call that the client has abandoned during the hold.

Tasks: not tested. In 2026-07-28 tasks are the `io.modelcontextprotocol/tasks` extension; the extension's server guide says "Never return a task to a client that did not declare support", and the client declares support in its per-request capabilities. Neither installed client SDK declares or implements it: no `io.modelcontextprotocol/tasks` string exists in `@modelcontextprotocol/client` 2.4.0, `@modelcontextprotocol/core` 2.4.0, or Python `mcp` 2.3.0. A stand-in could turn a held call into a task only for a client that declares the extension, and would then have to serve `tasks/get`, `tasks/update`, and `tasks/cancel` itself, own a durable task id, forward the original call upstream on approval, and store the result. That is a server role rather than a filter role, so the spike reports it as not tested and not available with the current official SDK clients.

### Q5. Token injection

Measured in every run. The clients sent either no `Authorization` header or `Bearer lenny-injected`; the proxy recorded what each client sent (`client_authorization` in the event logs: 96 of 96 requests in `logs/03-refusal-modes` carried the placeholder). The upstreams received the bound token on every request: across that directory, 60 of 60 legacy requests had fingerprint `2952db4ca17d` (SHA-256 prefix of `Bearer lnytok_LEGACY_...`) and 36 of 36 modern requests had `c7963a8d9a35`; the placeholder's fingerprint `8d19093179e9` never reached an upstream. Each binding has its own token, so the token is scoped to the bound host:port; an unbound host is refused at CONNECT with 403 (case 10).

Echo: `echo_auth_error` makes the upstream return its received Authorization header inside a tool error. With redaction on, the proxy replaced the token with the placeholder in JSON bodies, SSE event data, and response headers, and both clients saw `upstream rejected credential: Bearer lenny-injected` (all four client/era combinations, plus the functional runs). With redaction off (`proxy-noredact`), both clients saw `Bearer lnytok_LEGACY_7d1e5f0a9b3c42e8` and `Bearer lnytok_MODERN_c4a8e2f61d0b9375`. Injection alone therefore does not keep the credential from the pod; response redaction is required. Redaction matches the literal token; an upstream that re-encodes it (base64, partial echo, a token split across two SSE events) defeats it, and that was not tested.

### Q6. Overhead

`logs/08-overhead/summary.md`, raw in `bench.jsonl`. Sequential calls on one client, 20 warm-up calls, n=200 per run, 3 runs per cell; values are the median across runs. Paths: direct (client trusts the upstream CA), passthru (proxy with TLS termination and token injection, inspection off), inspect (inspection on: parsing, rules, audit, redaction; no `tools/list` filtering).

| Client | Response | direct p50 / p95 | passthru p50 / p95 | inspect p50 / p95 | inspect − direct p50 / p95 |
|---|---|---|---|---|---|
| TS 2.4.0, 2026-07-28 | JSON (`fast`) | 0.623 / 0.889 ms | 0.849 / 1.199 | 0.755 / 1.200 | +0.13 / +0.31 ms |
| TS 2.4.0, 2026-07-28 | SSE (`fast_progress`, 1 progress + result) | 0.748 / 1.310 | 1.001 / 1.560 | 0.952 / 1.299 | +0.20 / −0.01 ms |
| TS 2.4.0, 2025-11-25 session | SSE (`fast`, priming event + result) | 2.110 / 2.487 | 2.232 / 2.668 | 2.243 / 2.555 | +0.13 / +0.07 ms |
| Python 2.3.0, 2026-07-28 | JSON (`fast`) | 0.851 / 1.137 | 1.097 / 1.374 | 1.134 / 1.463 | +0.28 / +0.33 ms |
| Python 2.3.0, 2026-07-28 | SSE (`fast_progress`) | 2.935 / 3.501 | 2.645 / 3.130 | 2.746 / 3.252 | −0.19 / −0.25 ms |

The proxy hop adds about 0.1 to 0.3 ms per call on loopback. Inspection on versus off is within run-to-run noise (−0.09 to +0.10 ms at p50), consistent with the 10 µs median measured inside the proxy. The negative Python SSE delta is noise from the direct path; the absolute numbers are dominated by the client SDKs. These are loopback numbers with keep-alive connections and small bodies; they exclude a real network, the gVisor/Kata network path, and large tool results.

## 4. Measured versus reasoned

Measured: every number and behavior quoted from `logs/`. Reasoned only: how a harness renders JSON-RPC errors to the model; Claude Code timeout behavior (documentation only); Tasks conversion; redaction against encoded tokens; performance on non-loopback networks. Not run: Claude Code or any other harness CLI (a tool call would need a model turn on the user's existing login, so it was left out), HTTP/2 between client and proxy (the proxy offers only `http/1.1` via ALPN; both SDKs used HTTP/1.1), `subscriptions/listen` long-lived streams, and sampling/roots.

Failures encountered during the spike: the first upstream CA lacked a `keyUsage` extension, which Python 3.13's strict X.509 verification rejects ("CA cert does not include key usage extension"); it was reissued with `keyUsage=critical,keyCertSign,cRLSign` and the overhead benchmark was rerun (the failed run is kept as `logs/08-overhead/bench-run1-pydirect-failed-cert.jsonl`). netagent's per-pod CA must carry `keyUsage` for Python clients. The leaf certificate was reissued once to add the `mcp-legacyjson.lenny.test` SAN.

Design notes for netagent that follow from the results:

1. The MCP rule needs both eras: body-authoritative JSON-RPC parsing, header/body cross-checks for 2026-07-28, session-scoped correlation and client-response gating for 2025-11-25, and batch handling.
2. Denials of tools should be `isError` tool results; method and protocol violations should be JSON-RPC errors with the request id.
3. Response-side redaction of the injected credential is required in addition to injection.
4. Holds above 60 s fail for default TS SDK callers; proxy-authored progress only helps callers that send a `progressToken` and enable `resetTimeoutOnProgress`.
5. The inspection stage is cheap (about 10 µs per request); `tools/list` filtering of large catalogs costs milliseconds per list with generic JSON handling.

## 5. Commands

Setup (`logs/setup/`):

```
npm install --ignore-scripts @modelcontextprotocol/client@2.4.0 @modelcontextprotocol/server@2.4.0 @modelcontextprotocol/node@2.1.2 @modelcontextprotocol/sdk@1.32.2 zod   # in vendor/node
uv venv -p 3.13 vendor/py2/.venv && VIRTUAL_ENV=vendor/py2/.venv uv pip install mcp==2.3.0 httpx
openssl req -x509 ... upstream-ca ; openssl x509 -req ... upstream.pem    # certs/, leaf SANs in certs/leaf.ext
cd src/proxy && go vet . && go build -o ../../bin/proxy .
```

Runs (each writes to its own `logs/NN-*` directory):

```
src/servers-up.sh <logdir>                              # 8443 legacy SSE, 8444 legacy JSON, 9443 modern
src/proxy-up.sh <logdir> <name> <port> [key=value ...]  # writes proxy-<name>.config.json, starts bin/proxy
# 01/02 functional:  node ts-client.mjs functional https://<host>/mcp <legacy|modern>
#                    vendor/py2/.venv/bin/python -I py_client.py functional https://<host>/mcp <legacy|modern>
# 03 refusal modes:  proxies refusal=jsonrpc|http403|tool-error, redactToken=false; scenario "deny" with {"sendPlaceholder":true}
# 04 edge cases:     src/curl-edge.sh 18080
# 05 tools/list:     EXTRA_TOOLS=500 servers; proxies filterToolsList=true|false, inspect=false; scenarios rawlist, listbench
# 06 resumability:   proxy cutTool=slow cutAfterEvents=3; scenario resume
# 07 approval hold:  src/hold-matrix.sh logs/07-approval-hold
# 08 overhead:       src/bench.sh logs/08-overhead; python3 -I src/summarize.py logs/07-approval-hold logs/08-overhead/bench.jsonl
```

Cleanup: all spike processes (`bin/proxy`, `server-legacy.mjs`, `server-modern.mjs`, clients) were stopped. Docker was not used, so no `lenny-tsspike-` containers or images exist; the kind cluster `lenny-e2e` was not touched. One temporary file (`/tmp/lenny-tsspike-big.json`, case 7) was created and deleted by `src/curl-edge.sh`.
