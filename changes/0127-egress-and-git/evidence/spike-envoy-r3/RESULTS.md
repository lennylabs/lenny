# Envoy as `lenny-egress`: hands-on spike, round 3

Envoy images: `envoyproxy/envoy:v1.40.2` (build b2d89e0a, BoringSSL) and `envoyproxy/envoy:v1.39.6`.
Host: Docker 29.4.3 on macOS arm64 (12 vCPU VM, 7.75 GiB). All containers are named `r3-*`, networks `sr3-*`.

## Harness

| Path | Purpose |
|:--|:--|
| `up.sh` / `down.sh` | Networks `sr3-pub` (198.18.0.0/24 + 2001:db8:1::/64), `sr3-priv` (10.99.0.0/24 + fd00:99::/64), `sr3-v6only` (2001:db8:2::/64, `--ipv4=false`), helper containers. |
| `envoy.sh TAG CFG [args]` | Starts `r3-envoy` at 198.18.0.10 / 2001:db8:1::10, also on `sr3-priv` (10.99.0.10, fd00:99::10) and `sr3-v6only` (2001:db8:2::10). Admin on 127.0.0.1:19911. |
| `gen.py OUT k=v...` | Renders the r3 bootstrap as JSON (ext_authz grpc/http, DNS resolver, SSRF filter, hygiene, circuit breakers, SNI gate). |
| `b0.sh TAG` | Baseline suite on the previous spike's `envoy/final.yaml`. |
| `go/authz2` | Decision service: gRPC :9000, HTTP/1.1 :9001, HTTP/2 cleartext :9003, control :9002 (`/delay?ms=N`, `/count`). |
| `go/dnsd` | Controllable DNS server (UDP/TCP 53, control :8053 `/set`, `/log`). |
| `go/upproxy2` | Upstream CONNECT proxy: `REQUIRE_AUTH` (407 with two `Proxy-Authenticate` headers and a body), `DENY403` (403 with a body, `X-Corp-Policy`, and a `Proxy-Authenticate`). |
| `go/loadgen` | Rate-controlled CONNECT load generator with per-second and summary latency. |
| `logs/` | Raw output of every run quoted below. |

Fixed addresses: authz 198.18.0.11, proxy A 198.18.0.12 (no auth), proxy B 198.18.0.13 (auth), proxy D 198.18.0.15 (403), target 198.18.0.20 / 2001:db8:1::20, v6-only target 2001:db8:2::20, internal target 10.99.0.5 / fd00:99::5, dnsd 198.18.0.53, curl client 198.18.0.30.

## Test 0: baseline re-run of the previous suite

Commands: `./up.sh` (old `bin/authz` decision table), `./b0.sh v1.40.2`, `./ssrf.sh v1.40.2 on`, then the `route.max_stream_duration=5s` lifetime variant (`envoy/lt.yaml`, rendered by `envoy/render.py`), and the same on v1.39.6. Raw output: `logs/b0-*.txt`.

The networks are now dual-stack, so `target.spike` resolves to both 198.18.0.20 and 2001:db8:1::20 through Docker DNS and Envoy (`dns_lookup_family: ALL`) connected over IPv6 in most runs.

| Case | v1.40.2 | v1.39.6 | Evidence |
|:--|:--|:--|:--|
| CONNECT + gRPC ext_authz allow, HTTPS through tunnel | PASS | PASS | `connect=200 http=200`, body `hello from target, you are [2001:db8:1::10]:36956`; access log `200 via_upstream up=[2001:db8:1::20]:443 dfp tenant=acme` |
| ext_authz deny (`bad`) | PASS | PASS | `connect=403`, access log `403 UAEX ext_authz_denied reason=lease-invalid` |
| No credential | PASS | PASS | `407`, `proxy-authenticate: Basic realm="lenny-egress"`, `x-lenny-deny-reason: no-credential` (curl without `--proxy-user`) |
| Host deny (`blocked.example.com`) | PASS | PASS | `403 UAEX ext_authz_denied reason=host-not-allowed` |
| Fail-closed, authz container stopped | PASS | PASS | `connect=503 time=1.0097s` (authz cluster `connect_timeout: 1s`), access log `503 UAEX ext_authz_error`; first CONNECT after restart `200` |
| SSRF filter, 23 targets | PASS | PASS | Only `target.spike:443` and `example.com:443` reach `200`; all others `503 DF dns_resolution_failure{address_denied}`, including `[::ffff:127.0.0.1]:9901`, `[::ffff:10.99.0.5]:80`, `[::ffff:a63:5]:80`, `[fe80::1]:80`, `[64:ff9b::a63:5]:80`, `[::1]`, `[::]`, `0.0.0.0`, `localhost`, and `localtest.me`. Numeric forms `2130706433`, `0x7f000001`, and `127.1` fail as `cares_norecords` (c-ares does not parse them as IPs). `0177.0.0.1` is parsed as decimal 177.0.0.1 (public, `UF connection_timeout`), so it does not reach loopback. |
| `route.max_stream_duration: 5s` ends an active tunnel | PASS | PASS | Ticking tunnel: `tunnel ended after 5.99s: EOF (last="tick 4.0")`; access log `200 max_duration_timeout 5002 ms`. The extra ~1 s is the default `delayed_close_timeout` (1 s) of `envoy/lt.yaml`; `final.yaml` sets 0.1 s. A client-active tunnel to a silent upstream ended at 5.99 s with `connection reset by peer`. |
| Chaining, forwarded CONNECT to a STATIC proxy cluster (`chain-b`) | PASS | PASS | Proxy B log `CONNECT target.spike:443 headers=map[Proxy-Authorization:[Basic dGVuYW50LWdsb2JleDp1cHN0cmVhbS1zZWNyZXQ=]]`, `connect=200 http=200` |
| Chaining, forwarded CONNECT via ORIGINAL_DST from ext_authz dynamic metadata (`chain-m`, the design path) | PASS | PASS | access log `200 via_upstream up=198.18.0.13:3128 upstream_dynamic_md tenant=globex`; HTTPS body returned |
| Chaining, ORIGINAL_DST from request header `x-lenny-upstream` (`chain-o`) | FAIL | FAIL | `503 UH no_healthy_upstream`, Envoy warning `original_dst_load_balancer: No downstream connection or no original_dst.` The header added by ext_authz is not used by the ORIGINAL_DST LB (`use_http_header`). Not the design path. |
| Chaining, DFP cluster + `http_11_proxy` transport socket with `default_proxy_address` (`chain-a`) | FAIL | FAIL | `connect=200`, then `curl: (35) TLS connect error ... unexpected eof`; proxy A log `malformed HTTP request "\x16\x03\x01..."`: the transport socket sent the raw TLS ClientHello to the proxy without a CONNECT preamble. Same with `V4_ONLY`. Not the design path. |

Additional observation: every config that uses `resolved_address_filter` logs, once per CIDR entry at startup:

```
[warning][misc] message 'xds.core.v3.CidrRange' is contained in proto file 'xds/core/v3/cidr.proto' marked as work-in-progress. API features marked as work-in-progress are not considered stable, are not covered by the threat model, are not supported by the security team, and are subject to breaking changes.
```

The SSRF control therefore depends on an API that Envoy itself labels as outside its threat model.

## Test 1: ext_authz in HTTP mode (`http_service`) on CONNECT

Config (`gen.py authz=http [httph2=1]`, files `envoy/g-http.json`, `envoy/g-http2.json`):

```yaml
http_service:
  server_uri: {uri: http://198.18.0.11:9001, cluster: authz_http, timeout: 2s}
  path_prefix: /check
  authorization_request:
    headers_to_add: [{key: x-lenny-peer, value: "%DOWNSTREAM_REMOTE_ADDRESS%"}]
  authorization_response:
    allowed_upstream_headers: {patterns: [{exact: x-lenny-route}, {exact: proxy-authorization}]}
    allowed_client_headers:   {patterns: [{exact: x-lenny-deny-reason}, {exact: proxy-authenticate}]}
    dynamic_metadata_from_headers: {patterns: [{exact: x-lenny-upstream}, {exact: x-lenny-tenant}]}
allowed_headers: {patterns: [{exact: proxy-authorization}]}
failure_mode_allow: false
clear_route_cache: true
status_on_error: {code: ServiceUnavailable}
# ORIGINAL_DST cluster for chaining reads the header-derived metadata key:
original_dst_lb_config: {metadata_key: {key: envoy.filters.http.ext_authz, path: [{key: x-lenny-upstream}]}}
```

### Result over HTTP/1.1 to the decision service: FAIL (every request 503)

The check request is sent with the original method. The decision service logged:

```
HTTP CHECK {"headers":{"Content-Length":["0"],"Proxy-Authorization":["Basic YWdlbnQ6Z29vZA=="],"X-Envoy-Expected-Rq-Timeout-Ms":["2000"],"X-Envoy-Internal":["true"],"X-Lenny-Peer":["198.18.0.30:41602"]},"host":"target.spike:443","method":"CONNECT","n":3,"peer":"198.18.0.10:53610","proto":"HTTP/1.1","uri":"target.spike:443"}
```

`path_prefix` is dropped (a CONNECT has no path). Envoy then closes the HTTP/1.1 connection to the decision service immediately after writing the request (debug log: `closing data_to_write=220 type=0`, `close during connected callback`, `Request reset. Reason 7`), so allow, deny, and missing-credential requests all end as `503 UAEX ext_authz_error` (`cluster.authz_http.upstream_rq_503: 1`, `http.egress.ext_authz.error: 1`). Overriding the method with `headers_to_add: [{key: ":method", value: POST}]` is rejected at config load: `:-prefixed or host headers may not be modified`. Same on v1.39.6.

### Result over HTTP/2 cleartext to the decision service: PASS

With `explicit_http_config: {http2_protocol_options: {}}` on `authz_http` (decision service listening h2c on :9003), the same check arrives as `"method":"CONNECT","proto":"HTTP/2.0","uri":"target.spike:443"` and is answered normally.

| Question | Observed (v1.40.2; v1.39.6 identical) |
|:--|:--|
| Authority | Present as the `Host`/`:authority` of the check request: `"host":"target.spike:443"`. |
| Proxy-Authorization | Present only because `allowed_headers` lists it (default allowlist excludes it). |
| Peer address | Absent by default (no XFF, `use_remote_address` is false); the decision service sees only Envoy's own address (`peer: 198.18.0.10:58024`). Adding `headers_to_add: x-lenny-peer: "%DOWNSTREAM_REMOTE_ADDRESS%"` works: `"X-Lenny-Peer":["198.18.0.30:41602"]`. The formatter is substituted. |
| Route header | `x-lenny-route` returned by the decision service is applied via `allowed_upstream_headers`; `clear_route_cache: true` reselects the route (`route=direct` in the access log). |
| Dynamic metadata for chaining | `dynamic_metadata_from_headers` stores `x-lenny-upstream` under `envoy.filters.http.ext_authz` with the header name as key; ORIGINAL_DST `metadata_key` path `[{key: x-lenny-upstream}]` routes the forwarded CONNECT to it: access log `200 via_upstream up=198.18.0.13:3128 upstream_dynamic_md md_up=198.18.0.13:3128 md_tenant=globex`. |
| Replacement upstream credential | `proxy-authorization` returned on 200 overwrites the client header; proxy B received `Basic dGVuYW50LWdsb2JleDp1cHN0cmVhbS1zZWNyZXQ=` and returned 200. |
| Client credential leak | Without extra config, a chained route with no replacement credential forwards the client's lease credential to the tenant proxy: proxy B log `headers=map[Proxy-Authorization:[Basic YWdlbnQ6Y2hhaW4tNDA3]]`. Fixed by the decision service returning `x-envoy-auth-headers-to-remove: proxy-authorization`; proxy B then logs `headers=map[]`. (gRPC mode uses `OkHttpResponse.headers_to_remove`.) |
| Deny | 403 with the decision service's body; 407 from the decision service with `proxy-authenticate: Basic realm="lenny-egress"` and `x-lenny-deny-reason` passed to the client via `allowed_client_headers`. |
| Fail-closed | Container stopped: `503` after 1.008 s (connect timeout). Container paused (TCP accepted, no answer): `503` after 2.004 s (the `server_uri.timeout`). |

Default `allowed_headers` check (`envoy/g-http2-noallow.json`): without `allowed_headers: proxy-authorization`, the check request carries no `Proxy-Authorization` and every client gets `407 no-credential`.

Verdict: HTTP mode PARTIAL. It works on v1.40.2 and v1.39.6 only when the `http_service` cluster speaks HTTP/2 (h2c). Over HTTP/1.1 it fails closed for every CONNECT. The decision service must also handle a check request whose method is CONNECT and whose request-target is `host:port` (Go `net/http` accepts it on both h1 and h2c).

## Test 2: IPv6

Setup: `sr3-v6only` is created with `--ipv6 --ipv4=false --subnet 2001:db8:2::/64`; `r3-target6` (2001:db8:2::20) is attached only to it, so it is reachable only over IPv6. Envoy resolves through c-ares pointed at `dnsd` (`gen.py dns=dnsd`):

```yaml
typed_dns_resolver_config:
  name: envoy.network.dns_resolver.cares
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.network.dns_resolver.cares.v3.CaresDnsResolverConfig
    resolvers: [{socket_address: {address: 198.18.0.53, port_value: 53}}]
    use_resolvers_as_fallback: false
```

Listener: `address: "::"`, `ipv4_compat: true`. Command: `./t2.sh v1.40.2` and `./t2.sh v1.39.6` (logs `logs/t2-v6-*.txt`). The target echoes the source address Envoy used.

| `dns_lookup_family` | `[2001:db8:2::20]:80` (literal) | `v6only.spike` (AAAA only) | `dual.spike` (A + AAAA) |
|:--|:--|:--|:--|
| ALL | 200, from `[2001:db8:2::10]` | 200 | 200 over IPv4 (`198.18.0.10`) |
| AUTO | 200 | 200 | 200 over IPv6 (`[2001:db8:1::10]`) |
| V4_PREFERRED | 200 | 200 | 200 over IPv4 |
| V4_ONLY | 503 `DF dns_resolution_failure{cares_failure:Misformatted_domain_name}` | 503 | 200 over IPv4 |
| V6_ONLY | 200 | 200 | 200 over IPv6 |

`V4_ONLY` refuses IPv6 literals as a misformatted domain name. Results are identical on v1.39.6.

SSRF filter on IPv6 (family ALL, filter on), every case `503 DF dns_resolution_failure{address_denied}`:
`[fd00:99::5]:80` (ULA literal), `ula.spike` (AAAA fd00:99::5), `ll.spike` (AAAA fe80::1), `[fe80::7c5e:4aff:fed3:2b]:80` (the internal target's real link-local address), `loop6.spike:9901` (AAAA ::1), `mapped.spike` (AAAA ::ffff:10.99.0.5), `nat64.spike` (AAAA 64:ff9b::a63:5), and `[::ffff:198.18.0.20]:80`. `dns_cache.egress_dns.dns_address_filter_out: 8`. The public v6 literal `[2001:db8:1::20]:80` passes.

Control with the filter off (`envoy/g-v6-off.json`): `[fd00:99::5]` and `ula.spike` return `200`, `hello from target, you are [fd00:99::10]`, so the ULA block above is the filter and not unreachability. With the filter off, v4-mapped targets fail on their own (`UF immediate_connect_error: Network is unreachable | remote_address:[::ffff:10.99.0.5]:80`), because Envoy does not connect to v4-mapped addresses over a v6 socket here. The filter's `::ffff:0:0/96` entry also blocks mapped public addresses.

Verdict: PASS on both versions.

## Test 3: live DNS rebinding

Commands: `REFRESH=2s ./t3.sh TAG` and `./t3b.sh TAG` (logs `logs/t3-*.txt`, `logs/t3b-*.txt`). DNS cache: `dns_min_refresh_rate: 1s`, `dns_lookup_family: ALL`, `host_ttl: 300s` (t3) or `5s` (t3b), `dns_refresh_rate: 60s` (first run) or `2s`. `dnsd` serves `rebind.spike` and logs every query. The private target 10.99.0.5 / fd00:99::5 is reachable from Envoy, so a missed filter would produce a 200 from the internal target.

Sequence and observations (v1.40.2, `dns_refresh_rate: 2s`; v1.39.6 identical):

| Phase | DNS answer | Next CONNECT | Explanation |
|:--|:--|:--|:--|
| 1 | A 198.18.0.20, TTL 1 | 200 via `198.18.0.20:80` | First resolution. |
| 2 | AAAA 2001:db8:1::20, TTL 1 | 200 via `[2001:db8:1::20]:80` | The cache refreshes at the 1 s TTL (dnsd log shows queries at 0.7, 1.7, 2.7, ... s) and applies the new address (`host_address_changed: 4`). |
| 3 | A 10.99.0.5 (private) | 200 via `[2001:db8:1::20]:80` | Every refresh is filtered (`dns_address_filter_out` rises 2 per refresh). The fully filtered answer is treated as a failure and the host keeps its previous address. The internal target is never contacted. |
| 4 | AAAA fd00:99::5 (ULA), TTL 0 | 200 via `[2001:db8:1::20]:80` | Same. |
| 5 | A 10.99.0.5 and 198.18.0.20 | 200 via `198.18.0.20:80` (3 of 3) | The filter removes only the private record; the public one is used. |
| 6 | A 10.99.0.5 | 200 via `198.18.0.20:80`; a never-resolved name `fresh.spike` with the same answer gets `503 DF dns_resolution_failure{address_denied}` | |
| t3b | public, then private; idle for 12 s with `host_ttl: 5s` | 503 `address_denied` | After `host_removed: 1`, the next CONNECT resolves from scratch and is refused. |

Refresh cadence after a filtered answer: with `dns_refresh_rate: 60s` (first run, `logs/t3-rebind-v1.40.2.txt`) the dnsd log shows no further query for `rebind.spike` after the first filtered answer at 5.9 s for the remaining 12 s of the test (`dns_query_attempt` frozen at 12). A filtered answer counts as a resolution failure, so the next refresh waits for `dns_failure_refresh_rate`, which defaults to `dns_refresh_rate`. With `dns_refresh_rate: 2s`, the queries resume every 2 s (7.7, 9.7, 11.7 s, ...).

Already-open tunnel: a tunnel to `rebind.spike:7000` opened in phase 1 stayed connected to `198.18.0.20:7000` through every rebinding and ran for its full 25 s (`tunnel ended after 25.00s: i/o timeout (last="tick 24.1")`, access log `200 ... 198.18.0.20:7000 25024 ms`). Rebinding does not affect established tunnels, which is expected because the TCP connection is to a fixed IP.

Verdict: PASS for SSRF safety. The filter is applied on every resolution and every cache refresh, and no request reached the private address. The behaviour differs from "the second CONNECT is refused": a name that already has a good address keeps serving that address after rebinding until the host leaves the cache (`host_ttl`). A name whose first answer is private is refused. This matches the proto comment on `resolved_address_filter` (`If all addresses are removed, the resolution is treated as a failure and the host will retain any previously resolved address.`).

## Test 4: load

Setup: Envoy v1.40.2 with `--concurrency 2` and `docker --cpus 2` (`ENVOY_DOCKER="--cpus 2" ./envoy.sh v1.40.2 envoy/g-load*.json --concurrency 2`), gRPC ext_authz (`timeout: 2s`), JSON access log to stdout enabled, decision service with logging off (`LOG=0`). `loadgen` runs in a separate container on the same bridge and opens CONNECT tunnels to `target.spike:7000`, which writes one line per second into every tunnel (`ticks` counts lines received, so it proves the tunnels carry data). Every CONNECT triggers one ext_authz check (`http.egress.ext_authz.ok` equals the attempt count). Latency is measured from TCP connect to the parsed `200` response, so it includes the ext_authz round-trip and the upstream TCP connect. CPU is the `docker stats` percentage of one core (200% is the cap); memory is container RSS. Idle Envoy: 18.8 MiB. Everything runs on one 12-vCPU Docker VM, so absolute numbers are indicative only. Command: `./ld.sh NAME [loadgen flags]`; logs `logs/t4-*.txt`, `logs/t4-*.stats`.

| Run | Load | Result | CONNECT setup latency | Envoy CPU (peak) | Envoy memory (peak) |
|:--|:--|:--|:--|:--|:--|
| A | 1000 tunnels opened at 100/s, each held 25 s | 1000/1000 `200`, `broken=0`, 25 000 ticks | p50 2.4 ms, p90 3.3 ms, p99 10.8 ms, max 20.2 ms | 21.7% | 72.8 MiB with 1000 open tunnels (about 54 KiB per tunnel above idle) |
| B | 500 new CONNECT/s for 30 s, held 2 s (about 1000 concurrent) | 15 000/15 000 `200` | p50 0.8 ms, p90 1.7 ms, p99 3.7 ms | 43.6% | 79.9 MiB |
| C | 1000 new CONNECT/s for 20 s, held 1 s | 20 000/20 000 `200` | p50 0.7 ms, p90 0.9 ms, p99 2.0 ms | 49.4% | 80.4 MiB |
| C2 | 2000 new CONNECT/s for 15 s, held 1 s, default circuit breakers | 16 384 `200`, 13 612 `503 UO upstream_reset_before_response_started{overflow}` | p50 821 ms, p99 1.73 s | 56.6% | 118.7 MiB |
| C3 | Same as C2 with `circuit_breakers` raised on `dfp` (`cbdfp=100000`) | 30 000/30 000 `200` | p50 0.7 ms, p90 1.1 ms, p99 8.7 ms | 59.2% | 131.9 MiB |
| D | 3000 tunnels at 300/s, held 15 s, raised breakers | 3000/3000 `200`, 45 000 ticks | p50 0.8 ms, p99 7.8 ms | 24.3% | 178.7 MiB with 3000 open tunnels |
| H | Run C with HTTP-mode ext_authz over h2c | 15 000/15 000 `200` | p50 0.8 ms, p99 4.2 ms | 50.0% | 74.1 MiB |
| I | Run C on v1.39.6 | 15 000/15 000 `200` | p50 0.7 ms, p99 7.8 ms | 47.8% | 72.8 MiB |
| I2 | Run A on v1.39.6 (1000 concurrent) | 1000/1000 `200` | p50 2.5 ms, p99 8.4 ms | 11.8% | 71.8 MiB |

Concurrent-tunnel cap: run C2 shows `open=1024` in every second, and stats show `cluster.dfp.upstream_cx_overflow: 45677` and `cluster.dfp.upstream_rq_pending_overflow: 13612`. Each terminated CONNECT holds one upstream connection, so the default cluster circuit breaker (`max_connections: 1024`, `max_pending_requests: 1024`, `max_requests: 1024`) caps one Envoy instance at 1024 concurrent tunnels per cluster. Excess CONNECTs queue (latency up to 1.7 s) and then fail with `503 UO`. Run A (exactly 1000 concurrent) stayed below the cap by chance. Every tunnel cluster (`dfp`, `upstream_dynamic_md`, and the SNI gate cluster) needs explicit `circuit_breakers.thresholds` sized for the per-replica tunnel budget, and a global downstream connection limit (Envoy warns `There is no configured limit to the number of allowed active downstream connections`).

### Decision-service latency injection (run E, `./t4lat.sh E-latency 10:100 20:1000 30:3000 40:0`)

200 new CONNECT/s for 50 s, held 3 s, ext_authz `timeout: 2s`, default authz-cluster breakers.

| Window | Injected delay | Outcome per second |
|:--|:--|:--|
| 0–10 s | 0 | all `200`, p50 0.9–1.5 ms |
| 10–20 s | 100 ms | all `200`, p50 102.5 ms, p99 105–107 ms |
| 20–30 s | 1000 ms | all `200`, p50 1.002 s, p99 1.005 s |
| 30–40 s | 3000 ms (above the 2 s timeout) | all `503` at p50 2.001 s, p99 2.004 s (`UAEX ext_authz_error`); `cluster.authz.upstream_rq_timeout: 1515`; established tunnels unaffected |
| 40–43 s | back to 0 | checks issued before the change still time out (`503` until about t=42 s), then all `200` at p50 0.9 ms from t=43 s |

Summary: `200: 7990, 503: 2010`. Envoy does not queue beyond the in-flight checks; each CONNECT waits for its check and fails closed at the timeout. Recovery takes exactly one timeout period after the decision service recovers. Peak Envoy CPU 36.9%.

### Decision service killed mid-load (run F, `./t4kill.sh F-kill 10 25`)

`docker kill r3-authz` at t=10 s, `docker start` at t=25 s, 200 CONNECT/s. From t=11 s every CONNECT gets `503` at p50 509–529 ms, p99 about 1.0 s: requests are queued on the reconnect attempts (`connect_timeout: 1s`) and ejected (`cluster.authz.upstream_rq_pending_failure_eject: 3247`, `upstream_cx_connect_timeout: 32`, `upstream_cx_connect_fail: 40`). Tunnels opened before the kill finished normally (`broken=0`). The first `200` returned at t=27 s (177 of 381), and from t=28 s all CONNECTs succeeded at p50 0.8 ms. The kill closed the gRPC connection cleanly; a decision service that disappears without a RST would instead cost a full ext_authz timeout per check until the HTTP/2 connection is detected dead.

### Authz-cluster circuit breaker (run G, `cb=100`, delay 1000 ms from t=5 s to t=15 s)

With `max_requests: 100` on the authz cluster and 200 CONNECT/s at 1 s decision latency, about 100 CONNECTs per second succeed after 1.0 s and about 101 per second fail immediately with `503 UAEX ext_authz_error` (`cluster.authz.upstream_rq_active_overflow: 1010`). The breaker converts excess load into an immediate fail-closed answer instead of holding the CONNECT for the timeout.

### Recommended settings (from these runs)

- `ext_authz` `timeout`: 250–500 ms. The decision call took under 2 ms at p99 on an idle path, and a CONNECT waits for the full timeout when the service hangs.
- `failure_mode_allow: false` and `status_on_error: 503` (as tested).
- Authz cluster: HTTP/2, `connect_timeout` 250 ms, `circuit_breakers.max_requests` and `max_pending_requests` near the expected CONNECT rate × timeout (for example 1000/s × 0.5 s = 500), HTTP/2 `connection_keepalive` (interval and timeout) so a silently dead decision service is detected, and more than one decision-service endpoint with outlier detection. The keepalive and outlier settings were not exercised in this spike.
- Tunnel clusters: raise `circuit_breakers` on `dfp`, `upstream_dynamic_md`, and any SNI-gate cluster above the per-replica tunnel budget; add `envoy.resource_monitors.global_downstream_max_connections` and an overload manager.

## Test 5: chained routes, upstream proxy answers 407 or 403

Setup: route `upstream-md` (forwarded CONNECT, ORIGINAL_DST from ext_authz metadata). Token `chain-407` sends to proxy B without a credential; proxy B answers `407` with `Proxy-Authenticate: Basic realm="corp"`, `Proxy-Authenticate: Negotiate`, `X-Corp-Hint: kerberos`, and body `corp auth required`. Token `chain-403` sends to proxy D, which answers `403` with `X-Corp-Policy: rule-17`, `Proxy-Authenticate: Basic realm="corp-deny"`, and body `blocked by corp policy: target.spike:443`. Command: `EXTRA=hygiene=3 ./t5.sh TAG` (raw CONNECT via `nc`); logs `logs/t5-chain-*.txt`.

| Variant | Client sees for upstream 407 | Client sees for upstream 403 |
|:--|:--|:--|
| `hygiene=0` (no handling) | `407 Proxy Authentication Required`, both `proxy-authenticate` headers, `x-corp-hint: kerberos`, body `corp auth required` | `403`, `x-corp-policy: rule-17`, `proxy-authenticate: Basic realm="corp-deny"`, body `blocked by corp policy: target.spike:443` |
| `hygiene=1`: route `response_headers_to_remove: [proxy-authenticate, proxy-connection, x-corp-policy, x-corp-hint, via]` | `407`, headers removed, body still passed | `403`, headers removed, body still passed |
| `hygiene=2`: HCM `local_reply_config` mapper 407 → 502 | unchanged `407` (the mapper applies only to Envoy-generated replies) | unchanged |
| `hygiene=3`: `envoy.filters.http.custom_response` with a per-route matcher | `502 Bad Gateway`, `x-lenny-upstream-status: 407`, body `upstream proxy rejected the egress credential`; no upstream headers | `403`, `x-lenny-upstream-status: 403`, body `upstream proxy denied the destination`; no upstream headers |

Without handling, the tenant proxy's `Proxy-Authenticate` reaches the pod; a client that honours it would answer the tenant's challenge with the pod's own credential on the next attempt. With `hygiene=3`, the ext_authz-generated `407` for a missing credential is unaffected (`407`, `proxy-authenticate: Basic realm="lenny-egress"`), because the filter has no matcher globally and only the chained route carries one, and a successful chained tunnel (`chain-m`) still returns `200`. Same results on v1.39.6 and with HTTP-mode ext_authz (h2c).

Config for `hygiene=3` (full file `envoy/g-chain-hygiene3.json`):

```yaml
http_filters:
- name: envoy.filters.http.custom_response        # first in the chain, no matcher at listener level
  typed_config: {"@type": type.googleapis.com/envoy.extensions.filters.http.custom_response.v3.CustomResponse}
- ...ext_authz, dynamic_forward_proxy, router...
routes:
- match: {connect_matcher: {}, headers: [{name: x-lenny-route, string_match: {exact: upstream-md}}]}
  route: {cluster: upstream_dynamic_md, upgrade_configs: [{upgrade_type: CONNECT}]}
  typed_per_filter_config:
    envoy.filters.http.custom_response:
      "@type": type.googleapis.com/envoy.extensions.filters.http.custom_response.v3.CustomResponse
      custom_response_matcher:
        matcher_list:
          matchers:
          - predicate:
              single_predicate:
                input: {name: status_code, typed_config: {"@type": type.googleapis.com/envoy.type.matcher.v3.HttpResponseStatusCodeMatchInput}}
                value_match: {exact: "407"}
            on_match:
              action:
                name: lenny_upstream_407
                typed_config:
                  "@type": type.googleapis.com/envoy.extensions.http.custom_response.local_response_policy.v3.LocalResponsePolicy
                  status_code: 502
                  body: {inline_string: "upstream proxy rejected the egress credential\n"}
                  response_headers_to_add: [{header: {key: x-lenny-upstream-status, value: "407"}, append_action: OVERWRITE_IF_EXISTS_OR_ADD}]
          # ...same for 403 -> 403 with a fixed body
```

Verdict: PASS. The leak is real without configuration; `custom_response` with a per-route matcher normalizes status, headers, and body.

## Test 6: SNI check through an internal listener

Mechanism (`sni_gate.py`, enabled by `gen.py sni=1`; config additions extracted to `envoy/sni-gate-additions.yaml`, 175 lines of YAML without the inner access log, of which about 45 lines are a repeated copy of the DNS cache config):

1. The decision service allows the CONNECT with `x-lenny-route: sni-gate` and returns the expected host and port as `x-lenny-host` / `x-lenny-port` (split from the authority).
2. An HTTP `set_filter_state` filter after ext_authz writes `lenny.sni_expect` (`envoy.string`) from `%REQ(x-lenny-host)%`, `lenny.authority` from `%REQ(:AUTHORITY)%`, and `envoy.upstream.dynamic_host` / `envoy.upstream.dynamic_port`, all with `shared_with_upstream: ONCE`.
3. Route `sni-gate` terminates the CONNECT (`connect_config: {}`) to cluster `sni_gate`, whose endpoint is `envoy_internal_address: {server_listener_name: sni_inspect}` with the `envoy.transport_sockets.internal_upstream` transport socket. Bootstrap needs `envoy.bootstrap.internal_listener`.
4. Internal listener `sni_inspect`: `tls_inspector`; filter chain `transport_protocol: tls` runs network RBAC with a CEL condition, then `sni_dynamic_forward_proxy` (same `egress_dns` cache, so the SSRF filter applies) and `tcp_proxy` to the `dfp` cluster. The shared filter state is visible in the internal listener (inner access log `%FILTER_STATE(lenny.sni_expect:PLAIN)%` prints `target.spike`), and `sni_dynamic_forward_proxy` takes host and port from it (the 8443/8444 tests connected to port 8443/8444 although `port_value` is 443; SNI `evil.spike`, which resolves to 127.0.0.1, still connected to `[2001:db8:1::20]:443`).

RBAC condition (parsed CEL AST; no string form is accepted by `Policy.condition`):

```yaml
condition:   # connection.requested_server_name == string(filter_state['lenny.sni_expect'])
  call_expr:
    function: _==_
    args:
    - select_expr: {operand: {ident_expr: {name: connection}}, field: requested_server_name}
    - call_expr:
        function: string
        args:
        - call_expr: {function: "_[_]", args: [{ident_expr: {name: filter_state}}, {const_expr: {string_value: lenny.sni_expect}}]}
cel_config: {enable_string_conversion: true}
```

Matrix (`./t6.sh TAG sniplain=deny|allow [snitimeout=2s]`, client `bin/snicl`; logs `logs/t6-*.txt`). Results identical on v1.40.2 and v1.39.6.

| Case | Result | Evidence |
|:--|:--|:--|
| authority `target.spike:443`, SNI `target.spike` | allowed | `TLS ok: 200 OK hello from target`; `sni_gate.rbac.allowed` |
| SNI `evil.spike` | denied | client gets CONNECT `200`, then `tls: EOF`; inner log `sni=evil.spike expect=target.spike` |
| no SNI | denied | `tls_inspector.sni_not_found: 1`, `tls: EOF` |
| SNI `TARGET.SPIKE`, authority lower case | allowed | tls_inspector lower-cases the SNI (inner log `sni=target.spike`) |
| authority `TARGET.SPIKE:443`, SNI lower case | denied | expected value is not normalized; the decision service must lower-case (and strip a trailing dot from) the host it returns |
| authority `198.18.0.20:443`, SNI `target.spike` | denied | an IP-literal CONNECT cannot match an SNI; policy must decide separately |
| `internal.spike:443`, SNI `internal.spike` | denied by the SSRF filter (`UH`, no host) | but the client first receives CONNECT `200`, because DNS resolution moved from the outer route into the internal listener; the client sees `200` then EOF instead of `503` |
| plaintext, client-first (`sniplain=deny`) | closed | `listener.envoy_internal_sni_inspect.no_filter_chain_match: 2` |
| plaintext, client-first (`sniplain=allow`) | passed | `plain: HTTP/1.1 200 OK` in 21 ms |
| plaintext, server-first (target :7000) | delayed by `listener_filters_timeout` | 15 s by default (`server-first: err=EOF (15116ms)`, `downstream_pre_cx_timeout: 1`); with `snitimeout=2s` and `sniplain=allow` the first tick arrives after 2026 ms. Every server-first protocol on a gated route waits for the timeout. |
| ECH, `public_name` `public.ech.spike`, inner SNI `target.spike` | denied | tls_inspector sees the outer SNI `public.ech.spike` |
| ECH, `public_name` `target.spike`, inner SNI `evil.spike` | allowed (bypass) | `TLS ok ech_accepted=true: ech-server: inner SNI="evil.spike"`; the gate only sees the outer ClientHello |
| Control: ECH on the direct route | works | `ech_accepted=true` |

ECH was tested with a Go 1.26 `crypto/tls` server (`EncryptedClientHelloKeys`, X25519/HKDF-SHA256/AES-128-GCM config built by `snicl -serve`) and client (`EncryptedClientHelloConfigList`).

Cost (`--concurrency 2 --cpus 2`, 20 000 sequential-per-worker CONNECT + TLS 1.3 handshake + GET, 16 workers; Envoy CPU from `/proc/1/stat`, `logs/t6-cost-cpu-v1.40.2.txt`):

| Route | Throughput | p50 / p90 / p99 | Envoy CPU per 1000 tunnels |
|:--|:--|:--|:--|
| direct (2 runs) | 3380/s, 3456/s | 4.2–4.3 / 7.0–7.2 / 10.8–11.1 ms | 304 ms, 295 ms |
| SNI gate (2 runs) | 3315/s, 3073/s | 4.4–4.5 / 7.3–8.1 / 11.2–15.7 ms | 342 ms, 371 ms |

The gate adds about 0.1–0.3 ms at p50 and 15–25% Envoy CPU per tunnel (one extra user-space connection, the TLS inspector, and an extra access-log line). The client was the throughput bottleneck. Memory per gated tunnel was not measured.

Pitfall found while building it: `set_filter_state` with `%REQ(x-lenny-host)%` produces `-` when the header is absent, and `skip_if_empty` does not skip `-`. Because the filter runs on every route, every direct-route CONNECT then failed with `503 UH dns_resolution_failure{cares_norecords:Domain_name_not_found}` (DFP resolved the host `-`). Setting `omit_empty_values: true` on the format string fixes it.

Other caveats: the denied connection is closed by RBAC on the first data, after `tcp_proxy` has already started the upstream connection on connection creation (inner log shows an upstream host for the denied `evil.spike` case); whether any payload bytes reach the destination was not verified. Denied clients see a delay of about 100 ms (the outer `delayed_close_timeout: 0.1s`).

Verdict: PASS (works on both versions), with the ECH bypass, the server-first delay, and the 200-before-SSRF behaviour as design caveats.

## Test 7: CONNECT-specific issues and alpha status

Alpha status, verbatim from `api/envoy/config/route/v3/route_components.proto` at tag v1.40.2 (fetched from GitHub into `ref/v1.40.2/`; the same text is in go-control-plane v1.40.0 `route_components.pb.go`):

- On `RouteMatch.connect_matcher` (field 12): `Note that CONNECT support is currently considered alpha in Envoy.` followed by `[#comment: TODO(htuch): Replace the above comment with an alpha tag.]`
- On `RouteAction.UpgradeConfig.connect_config` (field 3): the same two lines.

There is no formal `[#alpha:]` tag on these fields, and `docs/root/intro/arch_overview/http/upgrades.rst` at v1.40.2 marks only CONNECT-UDP and raw UDP tunneling as alpha (`CONNECT-UDP is in an alpha status and may not be stable enough for use in production.`). It contains no alpha statement for TCP CONNECT.

Issues observed in this spike:

1. HTTP-mode ext_authz on CONNECT fails closed for every request when the authz cluster speaks HTTP/1.1 (Test 1). The check request inherits the CONNECT method, and Envoy closes the HTTP/1.1 connection after sending it. Use gRPC, or HTTP/2 for `http_service`.
2. HTTP-mode ext_authz forwards the client's `Proxy-Authorization` to a chained upstream proxy unless the decision service returns `x-envoy-auth-headers-to-remove: proxy-authorization` (Test 1).
3. The default cluster circuit breaker caps concurrent tunnels at 1024 per cluster; excess CONNECTs queue and then fail with `503 UO` (Test 4).
4. `ORIGINAL_DST` with `use_http_header` does not see a header added by ext_authz (`503 UH`, `No downstream connection or no original_dst`), and DFP + `http_11_proxy` with `default_proxy_address` sends the TLS payload to the proxy without a CONNECT preamble (Test 0). Neither is the design path; the metadata-driven `ORIGINAL_DST` works.
5. An upstream proxy's `407`/`403`, its `Proxy-Authenticate`, and its body pass through to the client by default (Test 5).
6. With a fully filtered DNS answer, the cache retries only at `dns_failure_refresh_rate` (default `dns_refresh_rate`, 60 s) and keeps the last good address (Test 3).
7. The SSRF filter's `xds.core.v3.CidrRange` is flagged at startup as work-in-progress and "not covered by the threat model" (Test 0).
8. The default `delayed_close_timeout` (1 s) delays the close of a tunnel ended by `max_stream_duration` by about 1 s (Test 0).

No Envoy crash or failed health check was observed in any run; every `envoy.sh` start reached `/ready` except the two invalid configs noted above (`:method` override, RBAC as last filter).

## Cleanup

`./down.sh` removed every `r3-*` container (including `r3-ech`, `r3-ech2`) and the networks `sr3-pub`, `sr3-priv`, and `sr3-v6only`. Pulled images were kept. `podium-postgres` and `podium-minio` were not touched. Go binaries were built on the host with `GOMODCACHE`/`GOCACHE` under this directory (`env.sh`); one early test build used the default module cache `~/go/pkg/mod`.
