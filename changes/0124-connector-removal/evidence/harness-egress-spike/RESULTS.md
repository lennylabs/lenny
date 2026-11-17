# Harness egress spike: Claude Code, Codex, and the MCP SDKs behind a netagent stand-in

Date: 2026-11-15. Host: macOS 27.2 (arm64), loopback only. This spike used no cloud resources and no Docker. It made no logins, used none of the owner's Claude or OpenAI credentials, and made no model calls. Nothing under `/Users/joan/projects/lenny` was edited. Paths below are relative to `/private/tmp/claude-501/-Users-joan-projects-lenny/c1165b35-7597-45a5-b0fc-caf23493501b/scratchpad/harness-egress-spike/`.

## Versions

| Component | Version |
|---|---|
| Claude Code | 2.1.323 (`/Users/joan/.local/share/claude/versions/2.1.323`, a compiled single binary) |
| Codex CLI | codex-cli 0.136.0 |
| TypeScript MCP client | `@modelcontextprotocol/client` 2.4.0 on Node v22.22.0 (from the tool-server spike) |
| Python MCP client | `mcp` 2.3.0 with `httpx2` 2.16.0 on Python 3.13.13 (from the tool-server spike venv, run with `-I`) |
| Test servers | `@modelcontextprotocol/server` 2.4.0 (stateless, 2026-07-28 and earlier) and `@modelcontextprotocol/sdk` 1.32.2 (stateful, 2025-era, used for elicitation) |
| Go / OpenSSL | go1.26.3 / OpenSSL 3.6.2 |

The full list is in `logs/setup/versions.log`.

## Setup

- **Proxy.** `bin/proxy` is the Go netagent stand-in from `../tool-server-spike/src/proxy`, extended in `src/proxy/main.go` with two features. The first is an `event: connect` record for every CONNECT, carrying the host and User-Agent. The second is a `tunnels` map that passes a host through as an opaque CONNECT tunnel with no TLS termination. Hosts with a binding get TLS termination with the per-pod CA (`../tool-server-spike/certs/proxy-ca.pem`), MCP inspection, and `Authorization: Bearer <bound token>` injection. Every other host is refused at CONNECT with 403. The proxy listens on `127.0.0.1:18090`. The configuration is written by `src/mkcfg.py`, and a copy of each run's configuration is stored as `logs/*/*/proxy.config.json`.
- **Hostnames.** The proxy resolves the `*.lenny.test` names itself, and the clients cannot resolve them. A successful connection to a `*.lenny.test` URL therefore went through the proxy.
- **Upstreams.**
  - `src/server/auth-front.mjs` on 9543 accepts only the bound token. Any other request gets 401 with `WWW-Authenticate: Bearer resource_metadata=...`. It logs every request (including OAuth discovery probes) and every TLS handshake failure, and forwards accepted requests to the 2026-07-28 server on 9443.
  - `src/server/elicit-server.mjs` on 9643 runs SDK 1.32.2. Its tools are `elicit_form`, `elicit_url` (`elicitation/create` with `mode: "url"`), and `url_required` (throws `UrlElicitationRequiredError`, -32042).
- **Routes through the proxy.**

  | URL | Proxy treatment | Upstream |
  |---|---|---|
  | `https://mcp-modern.lenny.test:9543/mcp` | Injected binding: TLS terminated, token injected | auth-front |
  | `https://mcp-legacy.lenny.test:9543/mcp` | Opaque tunnel with no injection, so the client receives the upstream's 401 | auth-front |
  | `https://mcp-modern.lenny.test:9443/mcp` | Injected binding | Modern server, which needs no auth |
  | `https://mcp-modern.lenny.test:9643/mcp` | Injected binding | Elicitation server |
  | `https://localhost:9443/mcp` | Unbound, so CONNECT is refused | Modern server, reachable directly only when the client bypasses the proxy (NO_PROXY test) |

- **Isolation.**
  - Claude Code ran through `src/cc.sh`, which uses `env -i` with `HOME=home/claude` and `CLAUDE_CONFIG_DIR=cfg/claude`, plus `DISABLE_AUTOUPDATER=1`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, and `DISABLE_TELEMETRY=1`. Run c14 left out those three variables.
  - Codex ran through `src/cx.sh`, which uses `env -i` with `HOME=home/codex` and `CODEX_HOME=cfg/codex*`.
  - Neither config directory holds credentials.
- **Run directories.** `src/exp.sh` starts fresh servers and a fresh proxy for each run, runs the command with a 90 s cap, and stops everything. Each run directory under `logs/` holds the following files:
  - `cmd.txt`: the exact command, exit code, and elapsed time.
  - `stdout.txt` and `stderr.txt`.
  - `proxy.events.jsonl`: every CONNECT and every MCP request with the client's `Authorization`.
  - The upstream logs.

## Q1. Claude Code 2.1.323

Measured with `claude mcp get <name>` and `claude mcp list`. Both run a live health check: an MCP handshake and `tools/list`, with no model call and no login (`logs/01-claude`). Claude Code connected on protocol 2026-07-28 (`server/discover`, `subscriptions/listen`, `tools/list`). It fell back to `initialize` 2025-11-25 against the 2025-era server (c15).

| Run | Environment | Result | Proxy log |
|---|---|---|---|
| c1 | `HTTPS_PROXY`, `NODE_EXTRA_CA_CERTS`=proxy CA | ✔ Connected | CONNECT `mcp-modern.lenny.test:9543`; upstream saw the bound token (fp `328ad322a7eb`) |
| c2 | `HTTPS_PROXY`, no CA | ✘ `SELF_SIGNED_CERT_IN_CHAIN` | CONNECT seen |
| c3 | `HTTPS_PROXY`, `SSL_CERT_FILE`=proxy CA | ✘ `SELF_SIGNED_CERT_IN_CHAIN` | CONNECT seen |
| c4 | no proxy, `NODE_EXTRA_CA_CERTS` | ✘ `ENOTFOUND mcp-modern.lenny.test` | none |
| c5 | lowercase `https_proxy` | ✔ Connected | CONNECT seen |
| c6 | `HTTP_PROXY` only | ✔ Connected (`HTTP_PROXY` is used for https URLs) | CONNECT seen |
| c10 | `localhost` URL, no `NO_PROXY` | ✘ `ERR_PROXY_TUNNEL ... 403` | CONNECT `localhost:9443` denied |
| c11 | `localhost` URL, `NO_PROXY=localhost,127.0.0.1` | ✔ Connected directly | no CONNECT |
| c12 | `NO_PROXY=.lenny.test` | ✘ `ENOTFOUND` (direct attempt, so suffix matching works) | none |

**a. Proxy variables.** Claude Code honours `HTTPS_PROXY`, `https_proxy`, and `HTTP_PROXY` for MCP HTTP traffic. It honours `NO_PROXY`, including host and domain-suffix entries.

**b. CA variables.** Claude Code honours `NODE_EXTRA_CA_CERTS`. It does not use `SSL_CERT_FILE` for its own MCP connections (c3). The binary references `SSL_CERT_FILE` only when it builds environments for subprocesses. Documentation: https://code.claude.com/docs/en/network-config lists `NODE_EXTRA_CA_CERTS` and `CLAUDE_CODE_CERT_STORE` (default `bundled,system`), and does not list `SSL_CERT_FILE`. `CLAUDE_CODE_CERT_STORE` was not tested.

**c. Injection and OAuth.**

- Behind an injected binding, both entry forms connected. The upstream received only the bound token, and the placeholder never reached it.
  - With a placeholder header `Authorization: Bearer lenny-injected` (c1).
  - With no header (c7).
- No OAuth activity occurs when injection succeeds, because Claude Code never sees a 401.
- **No header, upstream 401** (c8, through the opaque tunnel). Claude Code started OAuth on its own in the following order:
  1. `GET /.well-known/oauth-protected-resource/mcp`
  2. `GET /.well-known/oauth-authorization-server`
  3. `GET /.well-known/openid-configuration`
  4. `POST /register` (dynamic client registration)
  5. A retry with `initialize`, followed by the same discovery sequence again.

  Result: `✘ Failed to connect — Dynamic Client Registration rejected (HTTP 401)`. All of these requests went to the MCP host through the proxy. `mcp get` opened no browser.
- **Placeholder header, upstream 401** (c9). Claude Code made no discovery request. It reported: `Server rejected the configured Authorization header (HTTP 401) ... OAuth fallback is disabled when headers.Authorization is set.`
- **Recommendation.** A configured placeholder `Authorization` header keeps Claude Code from starting OAuth or dynamic client registration against an upstream when an injected credential is rejected. The documentation states the same thing (https://code.claude.com/docs/en/mcp, "Authenticate with remote MCP servers": a 401 or 403 on a server whose `Authorization` header is configured "doesn't flag the server ... Claude Code reports the connection as failed instead").

**Other egress.** Without the nonessential-traffic variables, `mcp get` also opened a CONNECT to `api.anthropic.com:443`, which the proxy refused, and the MCP check still passed (c14). With `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, the MCP hosts were the only egress.

## Q2. Codex CLI 0.136.0

`codex mcp list` and `codex mcp get` do not connect to MCP servers.

- `mcp get` prints configuration only (x2).
- `mcp list` computes an auth column, using an OAuth discovery probe for entries without a bearer token (x1, x12).

Codex has a no-model, no-login path that does connect: `codex app-server` over stdio JSON-RPC, using `initialize` and then `mcpServerStatus/list`. The driver is `src/appserver_probe.py` and the summaries come from `src/cxrun.sh` (`logs/02-codex`). Codex used protocol 2025-06-18 (`initialize`, `notifications/initialized`, `tools/list`) and sent no User-Agent on MCP requests.

| Run | Environment | Injected entries (`inj-*`) | `localhost` entry |
|---|---|---|---|
| x3 | `HTTPS_PROXY`, `NO_PROXY=localhost,127.0.0.1`, `SSL_CERT_FILE`=both CAs | connected, 8 tools | connected directly |
| x4 | `HTTPS_PROXY`, no CA | TLS failure ("error sending request") | failed |
| x5 | `HTTPS_PROXY`, `CODEX_CA_CERTIFICATE`=both CAs | connected | connected directly |
| x6 | no proxy, `SSL_CERT_FILE` | failed (DNS) | connected directly |
| x7 | `HTTPS_PROXY`, no `NO_PROXY` | connected | CONNECT `localhost:9443` denied |
| x9 | lowercase `https_proxy` and `no_proxy` | connected | connected directly |
| x10 | `HTTP_PROXY` only | failed (direct DNS); `HTTP_PROXY` is not used for https | connected directly |
| x11 | `NODE_EXTRA_CA_CERTS` only | TLS failure | failed |

**a. Proxy variables.** Codex honours `HTTPS_PROXY`, `https_proxy`, `NO_PROXY`, and `no_proxy` for streamable HTTP MCP. It does not fall back to `HTTP_PROXY` for https URLs.

**b. CA variables.** Codex honours `SSL_CERT_FILE` and `CODEX_CA_CERTIFICATE`, and ignores `NODE_EXTRA_CA_CERTS`. Documentation: https://developers.openai.com/codex/auth ("When `CODEX_CA_CERTIFICATE` is unset, Codex falls back to `SSL_CERT_FILE`").

**c. Injection and OAuth.**

- The three injected entries connected with the bound token at the upstream:
  - `http_headers = { Authorization = "Bearer lenny-injected" }`
  - no header
  - `bearer_token_env_var` with the variable set (x8)
- With `bearer_token_env_var` and the variable unset, the entry fails to start (x3).
- On a 401 (tunnel entries), Codex logs `AuthRequired(...)` and marks the server failed. It does not start OAuth during a session.
- **OAuth discovery probe.** The probe that `codex mcp list` and the auth status run bypasses the proxy. With `HTTPS_PROXY` set, the probe for `https://localhost:9543/mcp` reached the server directly with no CONNECT at the proxy (x12). The probe also rejected the server certificate ("tlsv1 alert unknown ca") although `SSL_CERT_FILE` held the issuing CA, so it ignores the custom CA as well. The result is `Auth: Unsupported`.
  - The control run without a proxy (x14) shows the same three direct handshakes.
  - This matches the source cited in the docs research (`codex-rs/rmcp-client/src/auth_status.rs` @ `rust-v0.136.0`, discovery client built with `.no_proxy()`).
  - In a pod this probe is a direct egress attempt. It fails closed and reports "Unsupported" without opening OAuth.
- **Other egress.** Codex contacted `chatgpt.com`, `github.com` (git), and `api.github.com` at app-server start. After `thread/start` it opened a websocket CONNECT to `api.openai.com` (`wss://api.openai.com/v1/responses`, a pre-connect with no credentials). The proxy refused all of them, and MCP was unaffected (e1).
  - Without a proxy, Codex attempted these hosts directly (x6). Those TLS handshakes failed because `SSL_CERT_FILE` held only the test CAs.
  - The three `rustls_platform_verifier ... UnknownIssuer` stderr lines in the app-server runs come from these non-MCP connections and the discovery probe.

## Q3. URL-mode elicitation in a headless harness

**Claude Code: unmeasured.** A tool call in Claude Code requires a model turn, so this spike did not run one. Measured: Claude Code declares `elicitation: {"form": {}, "url": {}}` in `initialize` on 2025-11-25 (c15, request body in `upstream-elicit.jsonl`).

Documented behaviour (retrieved 2026-11-15):

- **Interactive.** "URL mode: Claude Code asks whether to open a link in your browser" (https://code.claude.com/docs/en/mcp, "Respond to MCP elicitation requests"). CHANGELOG 2.1.281: "Added MCP URL-mode elicitation on 2026-07-28 protocol connections" (https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md).
- **`-p` with no hook or callback.**
  - https://code.claude.com/docs/en/errors ("MCP tool call timed out with the server's question unanswered") says the request stays unanswered until the tool-call timeout, then fails with "The server asked the user a question, and no answer came before the time limit. Do not call this tool again in this turn."
  - With `--permission-prompts none`, "Any MCP elicitation request that no `Elicitation` hook answers is cancelled" (https://code.claude.com/docs/en/headless).
  - The TS Agent SDK says an unhandled request is "declined automatically" when `onElicitation` is absent (https://code.claude.com/docs/en/agent-sdk/typescript).
  - The three pages disagree.
- **-32042 / `URLElicitationRequiredError`.** No documentation found.
- **2026-07-28 `input_required`.** Documented only for MCP tasks. In non-interactive sessions Claude Code asks the server to cancel a task that requests input, "without running your `Elicitation` hooks or calling the Agent SDK's `onElicitation` callback" (mcp page, tasks section). MRTR on a plain `tools/call` is not documented.
- **Forwarding hook.** The `Elicitation` hook event (https://code.claude.com/docs/en/hooks#elicitation) receives `mcp_server_name`, `message`, `mode`, `url`, `elicitation_id`, and `requested_schema`. It can answer `accept` with `content`, `decline`, or `cancel`. Because the hook is an arbitrary command, it can forward the request to an outside system and block until that system answers. The docs describe no asynchronous or deferred answer. `ElicitationResult` observes or modifies the user's response. The TS Agent SDK has `onElicitation`; the Python SDK docs show no equivalent.

**Codex: measured without a model call through app-server.** `thread/start` followed by `mcpServer/tool/call` calls an MCP tool directly with no model turn. The tool call is real, but the run is not `codex exec`. `approvalPolicy: "never"` reproduces exec's default policy (`codex-rs/exec/src/lib.rs` @ `rust-v0.136.0` defaults headless runs to `AskForApproval::Never`). Logs are in `logs/03-codex-elicitation`.

| Run | Policy | Server sends | Outcome |
|---|---|---|---|
| e1 | on-request | form `elicitation/create` | Forwarded to the app-server client as `mcpServer/elicitation/request` (mode form). Unanswered, so the server timed out at 30 s. |
| e6 | on-request | form | The client answered `decline`, and the server received `{"action":"decline"}`. |
| e2 | never (exec default) | form | Auto-declined at once (server outcome `decline`), with no client request. |
| e3, e4 | either | url | Codex declares bare `elicitation: {}`, so the SDK server refuses to send: "Client does not support url elicitation". |
| e8 | on-request, `features.auth_elicitation=true` | url | Codex declares `{"form":{},"url":{}}` and forwards `mcpServer/elicitation/request` with mode url, the URL, and the `elicitationId`. Unanswered, so it timed out. |
| e9 | never, `auth_elicitation` | url | Auto-declined. |
| e5, e7 | either | -32042 error | Returned as JSON-RPC error -32603: "tool call failed for `elicit/url_required`: Mcp error: -32042: ... URL elicitation required({...url...})". It has no special handling, and the URL appears only inside the message text. |

Codex with `codex exec` is unmeasured. The source says exec declines through the `Never` policy, and an elicitation that still reaches exec's server-request handler is cancelled ("Exec auto-cancels elicitation instead of surfacing it interactively", `codex-rs/exec/src/lib.rs` @ `rust-v0.136.0`). Codex 0.136.0 has no 2026-07-28 support. Its `mcp_2026_07_28` feature exists only in later releases and is off by default (docs research against `rust-v0.169.0`). Codex has no elicitation hook event (https://developers.openai.com/codex/hooks). The forwarding mechanism is app-server's `mcpServer/elicitation/request` server request (https://developers.openai.com/codex/app-server), measured in e1, e6, and e8. A Lenny harness adapter that drives Codex through app-server can forward elicitations, including URL mode with `auth_elicitation`. `codex exec` cannot.

## Q4. Official SDK clients through the stand-in

Measured with the tool-server spike drivers (`functional` scenario, 2026-07-28 pinned) against `https://mcp-modern.lenny.test:9443/mcp`. Logs are in `logs/04-sdk`, and the script is `src/sdk-matrix.sh`.

| Run | Client and environment | Result |
|---|---|---|
| t1 | TS, `NODE_USE_ENV_PROXY=1 HTTPS_PROXY NODE_EXTRA_CA_CERTS` | ok; 9 upstream requests, all with the injected token |
| t2 | TS, `HTTPS_PROXY NODE_EXTRA_CA_CERTS` without `NODE_USE_ENV_PROXY` | fails: `getaddrinfo ENOTFOUND` (direct, proxy ignored) |
| t8 | TS, `NODE_OPTIONS=--use-env-proxy` instead | ok |
| t7 | TS, lowercase `https_proxy` | ok |
| t3 | TS, `SSL_CERT_FILE` instead of `NODE_EXTRA_CA_CERTS` | fails: self-signed certificate in chain |
| t4 | TS, no CA | fails, same error |
| t5 / t6 | TS, `localhost` URL with / without `NO_PROXY` | direct ok / CONNECT denied 403 |
| p1 | Python, `HTTPS_PROXY SSL_CERT_FILE` | ok; 8 upstream requests, all with the injected token |
| p6 | Python, lowercase `https_proxy` | ok |
| p2 | Python, `REQUESTS_CA_BUNDLE` instead of `SSL_CERT_FILE` | fails: CA "is not trusted" |
| p3 | Python, no CA | fails, same error |
| p7 | Python, no proxy | fails: DNS |
| p4 / p5 | Python, `localhost` URL with / without `NO_PROXY` | direct ok / `ProxyError: 403` |

The TypeScript SDK on Node 22 honours `HTTPS_PROXY` and `NO_PROXY` only when the process sets `NODE_USE_ENV_PROXY=1` or `--use-env-proxy`, because Node's `fetch` ignores proxy variables by default. It trusts the proxy CA through `NODE_EXTRA_CA_CERTS`. It ignores `SSL_CERT_FILE`.

The Python SDK (`httpx2`) honours `HTTPS_PROXY`, `https_proxy`, and `NO_PROXY` by default. It trusts `SSL_CERT_FILE`. It ignores `REQUESTS_CA_BUNDLE`.

## Implications for the client proxy profile

| Variable | Claude Code | Codex | TS SDK (Node 22) | Python SDK |
|---|---|---|---|---|
| `HTTPS_PROXY` / `https_proxy` | yes | yes | only with `NODE_USE_ENV_PROXY=1` | yes |
| `HTTP_PROXY` used for https | yes | no | not tested | not tested |
| `NO_PROXY` | yes | yes | yes (with env proxy on) | yes |
| `NODE_EXTRA_CA_CERTS` | yes | no | yes | n/a |
| `SSL_CERT_FILE` | no | yes | no | yes |
| `CODEX_CA_CERTIFICATE` | n/a | yes | n/a | n/a |
| `REQUESTS_CA_BUNDLE` | n/a | n/a | n/a | no |

- The profile needs `HTTPS_PROXY` (and lowercase), `NO_PROXY` (and lowercase), `NODE_USE_ENV_PROXY=1`, `NODE_EXTRA_CA_CERTS`, and `SSL_CERT_FILE`. `CODEX_CA_CERTIFICATE` is optional.
- A Node runtime that predates `NODE_USE_ENV_PROXY` ignores the proxy variables. In a pod whose only route is netagent, that runtime fails closed.
- `SSL_CERT_FILE` replaces the trust store for Codex and Python. To keep public hosts working, it has to point at a bundle holding the system roots plus the per-pod CA.
- Placeholder headers:
  - Claude Code: a placeholder `Authorization` header in the MCP entry stops its OAuth fallback and dynamic client registration when an upstream rejects the injected credential.
  - Codex: Codex does not start OAuth on a 401 in a session, so a placeholder header there only changes the `mcp list` auth column.

## Measured and documented

**Measured.** Every table row above with a run id. The Codex elicitation rows are measured through app-server.

**Documented only, and not run:**

- Claude Code elicitation behaviour (interactive, `-p`, -32042, and `input_required`).
- The `Elicitation` hook, including whether it fires in `-p`.
- The `codex exec` elicitation path.
- Codex 2026-07-28 support in later releases.
- `CLAUDE_CODE_CERT_STORE`.

**Measurement limits.**

- The tool-server spike's results on SDK elicitation, holds, and redaction were not re-run.
- The Codex OAuth bypass is measured only for the discovery probe of `mcp list` and the app-server auth status. `codex mcp login` was not run because it starts a login.

## Cleanup

`src/down.sh` stopped every process this spike started, including the proxy, `server-modern.mjs`, `auth-front.mjs`, `elicit-server.mjs`, Codex app-server, and the probes. `ps` showed none remaining. Docker was not used, so no `lenny-hespike-` containers or images exist. The kind cluster `lenny-e2e` was not touched. The isolated config directories `cfg/` and `home/` stay in the spike directory and hold no credentials.
