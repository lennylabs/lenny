# Envoy as `lenny-egress`: hands-on spike, round 4

Envoy image: `envoyproxy/envoy:v1.40.2` (build b2d89e0a, BoringSSL). Host: Docker on macOS arm64 (12 vCPU VM, 7.75 GiB), shared with other spikes. All containers are named `r4-*`, the network is `sr4-pub` (198.19.0.0/24). Everything ran on one VM, so absolute latencies are indicative only.

## Harness

| Path | Purpose |
|:--|:--|
| `up.sh` / `down.sh` | Network and helper containers. The two decision services run under `sh -c 'while :; do /b/authz3; sleep 2; done'` so a process kill or SIGSTOP targets `authz3` while the container and its IP stay. |
| `envoy.sh NAME IP ADMINPORT CFG [args]` | Starts `r4-envoy-NAME`; `main` is 198.19.0.10 (admin 127.0.0.1:19912), `soak` is 198.19.0.40 (admin 127.0.0.1:19921). |
| `gen4.py OUT k=v...` | Renders the bootstrap as JSON: gRPC ext_authz to a 2-endpoint STATIC cluster, HTTP/2 keepalive (`ka=`), outlier detection (`od=`), gRPC health check (`hc=`), `GrpcService.retry_policy` (`retry=`, `rbase=`, `rmax=`), timeouts (`timeout=`, `cto=`, `sit=`, `idle=`, `msd=`, `dto=`), access-log flush (`flush=`), circuit breakers (`cbauthz=`, `cbdfp=`). Outlier and health-check events go to stdout. |
| `go/authz3` | Decision service: gRPC `:9000` with `grpc.health.v1`, control `:9002` (`/delay?ms=N`, `/count`, `/reset`, `/hist` = histogram of in-flight checks sampled every 10 ms). `NAME` is returned in dynamic metadata so the access log shows which endpoint answered. |
| `go/loadgen` | Rate-controlled CONNECT load generator (from r3) with a `-bucket` flag for 500 ms reporting. |
| `go/soak` | Opens N CONNECT tunnels, reads the server's 1 chunk/s stream, records per-tunnel end time and reason (EOF, RST, still open), and optionally probes a fresh CONNECT every second. |
| `bin/target` | r3 target: `:7000` writes `tick <t>` once per second (the SSE-like stream), `:7001` accepts and stays silent (idle control). |
| `t1.sh`, `t3.sh`, `t4.sh`, `soak.sh`, `ld.sh`, `st.sh` | Test drivers. Raw output of every run is under `logs/`. |
| `ref/` | Envoy v1.40.2 source files fetched from GitHub to explain observed behaviour (`utility.cc`, `conn_manager_impl.cc`, `drain_manager_impl.cc`, `drain_close_util.cc`, `async_client_impl.cc`, `grpc_async_client_impl.cc`). |

Base Envoy settings in every run: `codec_type: HTTP1`, CONNECT terminated through the dynamic-forward-proxy cluster `dfp` (`connect_config: {}`), `failure_mode_allow: false`, `status_on_error: 503`, `clear_route_cache: true`, `delayed_close_timeout: 0.1s`, `suppress_envoy_headers: true`, `--concurrency 2` with `docker --cpus 2` for `main`, `--concurrency 1` with `--cpus 1` for `soak`.

## Test 1: ext_authz over HTTP/2 with keepalive, two decision-service endpoints

Load for every run: 500 new CONNECT/s for 60 s, each tunnel held 2 s (about 1000 concurrent), through `r4-envoy-main` (`./t1.sh NAME MODE 15 35`). Endpoint `authz1` (198.19.0.11) is disrupted at t=15 s and restored at t=35 s; `authz2` (198.19.0.12) stays up. Both endpoints are in one STATIC cluster with round-robin, so about 250 checks/s go to each. Reporting buckets are 500 ms. Every failed CONNECT is a `503 UAEX ext_authz_error`.

Authz cluster configuration (`envoy/t1-ka.json`):

```yaml
clusters:
- name: authz
  type: STATIC
  connect_timeout: 0.25s
  typed_extension_protocol_options:
    envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
      "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
      explicit_http_config:
        http2_protocol_options:
          connection_keepalive: {interval: 1s, timeout: 1s}     # absent in the "noka" runs
  outlier_detection:
    interval: 1s
    base_ejection_time: 10s
    max_ejection_percent: 100
    consecutive_5xx: 3
    consecutive_gateway_failure: 3
    split_external_local_origin_errors: true
    consecutive_local_origin_failure: 3
    enforcing_consecutive_local_origin_failure: 100
    enforcing_success_rate: 0
    enforcing_local_origin_success_rate: 0
  load_assignment:
    cluster_name: authz
    endpoints: [{lb_endpoints: [{endpoint: {address: {socket_address: {address: 198.19.0.11, port_value: 9000}}}},
                                {endpoint: {address: {socket_address: {address: 198.19.0.12, port_value: 9000}}}}]}]
# ext_authz: grpc_service: {envoy_grpc: {cluster_name: authz}, timeout: 0.5s}
```

Disruption modes: `kill` = `docker kill r4-authz1` (process and network namespace gone, the IP stops answering) then `docker start`; `stop` = `pkill -STOP authz3` inside the container (frozen process, the kernel still completes TCP handshakes and ACKs) then `pkill -CONT`.

### Results

| Run | Disruption | Keepalive | ext_authz timeout | Failed CONNECTs | What happened (times are loadgen seconds; disruption at 15 s, restore at 35 s) |
|:--|:--|:--|:--|:--|:--|
| `t1a-kill-ka` | kill | 1s/1s | 0.5 s | 317 / 30 000 (1.06%) | 3 failures at t=15.0 (the checks in flight on the connection that was reset); `authz1` ejected immediately (`ejections_enforced_consecutive_local_origin_failure`). At t=25.5 (`base_ejection_time` 10 s) the host was unejected and probed with live traffic: 314 CONNECTs failed at p99 243–252 ms (= `connect_timeout` 0.25 s) over about 1.25 s before the host was ejected again (three connect attempts of 250 ms each). The second ejection lasted 20 s (`base_ejection_time` × ejection count), so `authz1` rejoined at t≈51 s, 16 s after it was restarted at 35 s. `upstream_cx_connect_timeout: 10`, `upstream_cx_connect_fail: 12`. |
| `t1c-stop-ka` | SIGSTOP | 1s/1s | 0.5 s | 630 (2.1%) | 169 failures at t=15.0–16.5, each after the full 0.5 s ext_authz timeout (`upstream_rq_timeout: 614`); ejection about 1 s after the freeze. `cluster.authz.http2.keepalive_timeout: 3` shows the keepalive also closed the frozen connections. Uneject probe at t=26.5–28.5: 461 failures at 0.5 s. Rejoined at t≈47. |
| `t1d-stop-noka` | SIGSTOP | none | 0.5 s | 665 (2.2%) | Same picture as with keepalive: 387 failures at t=15.5–17.0 and 278 at t=28.0–29.5, all after 0.5 s. With a 0.5 s request timeout the timeouts drive the ejection and the keepalive adds nothing measurable. |
| `t1c2-stop-ka-to5` | SIGSTOP | 1s/1s | 5 s | 840 (2.8%) | Checks to the frozen host hung until the keepalive closed the connections (`keepalive_timeout: 4`): 330 failures at t=16.5 after waiting p99 1.32 s, ejection at that moment. Uneject probe: 510 failures at p99 2.05 s. The keepalive bounded the hang at 1.3–2 s instead of the 5 s timeout. |
| `t1d2-stop-noka-to5` | SIGSTOP | none | 5 s | 1461 (4.9%) | Nothing detected the frozen host until the first checks timed out: 1461 failures at p99 5.00 s between t=20.5 and t=26.0 (every check queued during the first 5 s failed), ejection at t≈21. At the uneject probe (t≈32) half the CONNECTs hung again; when `authz1` was resumed at 35 s, 1140 queued checks completed with p50 1.4 s and p99 3.6 s. Envoy memory peaked at 124 MiB with 1251 pending checks (`upstream_rq_active`). |
| `t1e-kill-hc-retry` | kill | 1s/1s | 0.5 s | 0 | Adds the gRPC health check with `successful_active_health_check_uneject_host` and a retry policy (see below). The 3 in-flight checks were retried on `authz2` (`upstream_rq_retry_success: 3`). Outlier event log: `EJECT` at 08:55:03.001 (the kill), health check `eject_unhealthy_event NETWORK_TIMEOUT` at 08:55:04.263, outlier `UNEJECT` at 08:55:13.754, but the host stayed unhealthy for the health checker until it passed a check about 2 s after `docker start`; no live traffic was sent to the dead host. |
| `t1e-stop-hc-retry` | SIGSTOP | 1s/1s | 0.5 s | 0 | 112 checks were reset and retried on `authz2`, all successful; max CONNECT latency in the run 62.7 ms. |
| `t1f-kill-final` | kill | 1s/1s | 1 s | 0 | Final configuration (below). 3 retries, all successful; p99 3.6 ms, max 33 ms. `EJECT` 09:03:25.966, health check unhealthy +0.9 s, `UNEJECT` +10.4 s (host still unhealthy), healthy again 2 s after the restart. |
| `t1f-stop-final` | SIGSTOP | 1s/1s | 1 s | 0 | 234 retries, all successful; p99 13.4 ms, max 423 ms. `EJECT` 1.1 s after the freeze (09:04:34.113), health check unhealthy +0.4 s, healthy 1 s after `pkill -CONT`. |

Keepalive in isolation: `kill` closes the TCP connection, so keepalive is irrelevant for it and the `kill` runs were not repeated without keepalive. For a frozen peer the keepalive matters only when the request timeout is longer than `interval + timeout`; with the 0.5 s timeout the request timeouts detected the freeze first. Either way the first checks to reach a frozen host fail; a retry to the other endpoint is what turns those into successes.

The uneject probe is the dominant failure source with plain outlier detection: after `base_ejection_time` Envoy sends live checks to the ejected host and needs `consecutive_*_failure` more failures (each costing a full `connect_timeout` or request timeout) to eject it again. `successful_active_health_check_uneject_host: true` with an active gRPC health check removes that probe, because the host only returns once a health check passes.

### Final authz configuration (`envoy/final.json`, `gen4.py ... ka=1s,1s od=1 hc=1 retry=1 rbase=0.02s rmax=0.4s timeout=1s`)

```yaml
# ext_authz
grpc_service:
  envoy_grpc: {cluster_name: authz}
  timeout: 1s
  retry_policy:                       # GrpcService.retry_policy; EnvoyGrpc.retry_policy is xDS-only (proto comment)
    num_retries: 1
    retry_on: connect-failure,reset,refused-stream,unavailable,cancelled,deadline-exceeded
    retry_back_off: {base_interval: 0.02s, max_interval: 0.4s}   # max_interval doubles as the per-try timeout
    retry_host_predicate:
    - name: envoy.retry_host_predicates.previous_hosts
      typed_config: {"@type": type.googleapis.com/envoy.extensions.retry.host.previous_hosts.v3.PreviousHostsPredicate}
    host_selection_retry_max_attempts: 3
# cluster authz (in addition to the block above)
  health_checks:
  - timeout: 0.5s
    interval: 1s
    no_traffic_interval: 1s
    unhealthy_threshold: 1
    healthy_threshold: 1
    grpc_health_check: {}
  outlier_detection:
    successful_active_health_check_uneject_host: true
    # ... as above
  circuit_breakers: {thresholds: [{max_connections: 20000, max_pending_requests: 20000, max_requests: 20000, max_retries: 20000}]}
```

`cluster_manager.outlier_detection.event_log_path` and `health_checks[].event_log_path` were set to `/dev/stdout` to time the events; `HealthCheck.event_log_path` is logged as deprecated in 1.40.2 (`event_logger` replaces it).

Retry-policy pitfall found on the way (`t1e-*` used `retry_back_off: {base_interval: 0.01s, max_interval: 0.05s}`): `Http::Utility::convertCoreToRouteRetryPolicy` (`ref/utility.cc`) sets the per-try timeout of a `GrpcService.retry_policy` to `retry_back_off.max_interval`. With `max_interval: 0.05s` every check that takes 50 ms times out per try, and at 1000 CONNECT/s with a 50 ms decision latency 10 111 of 19 999 CONNECTs failed (`upstream_rq_per_try_timeout: 40 038`, 41 outlier ejections, `upstream_cx_protocol_error: 13`); at 200/s 1689 of 4000 failed. The same policy with `timeout: 5s` failed identically, so the overall timeout plays no role in it. Without a backoff block the defaults are 1 s base and 10 s max, which is slower than any sensible ext_authz timeout. Set `max_interval` to the longest acceptable single try.

## Test 2: long-stream soak (200 tunnels, 20 minutes)

Configuration (`./soak.sh t2-soak 20 msd=7200s sit=300s idle=3600s flush=60s`, file `envoy/t2-soak.json`):

```yaml
http_connection_manager:
  stream_idle_timeout: 300s            # reset by any byte in either direction; a 1 chunk/s stream never idles
  common_http_protocol_options: {idle_timeout: 3600s}   # connection idle timeout; irrelevant while a tunnel is active
  delayed_close_timeout: 0.1s
  access_log_options:
    flush_log_on_tunnel_successfully_established: true
    access_log_flush_interval: 60s
route:
  max_stream_duration: {max_stream_duration: 7200s}
```

Clients: `soak -n 200 -rate 50 -max 20m` to `target.spike:7000` (one `tick` line per second per tunnel), plus `soak -n 2` to `target.spike:7001` (silent) as the idle control. Envoy memory sampled every minute with `docker stats` and `/memory`.

| Check | Result | Evidence (`logs/t2-soak-*`) |
|:--|:--|:--|
| No tunnel cut | PASS | `SUMMARY n=200 codes=map[200:200] end_reasons=map[max reached (still open):200] ticks_total=239340 ticks_per_tunnel=[1195..1199] lifetime min=19m56.02s max=20m0s`. 12 000 ticks per minute throughout (200 tunnels × 60). Access log at the end: 200 × `downstream_remote_disconnect` with `dur_ms` 1 199 922–1 200 003 (the client closed them at 20 min). |
| Envoy memory flat | PASS | Container RSS: 20.0 MiB at start, 26.5–26.7 MiB from minute 1 to 8, 32.9 MiB from minute 9 to 19 (one step), 31.4 MiB after the clients closed. `/memory`: `allocated` 15.5–15.7 MB flat for all 20 minutes, `heap_size` 20.97 MiB until minute 8 and 23.07 MiB afterwards. CPU 0.8–2.8%. |
| Periodic access-log flush | PASS | `DownstreamPeriodic` entries: 0 at minute 1, 202 at minute 2, then +200 per minute, 3808 in total; the first periodic entry of a tunnel carries `dur_ms: 59999`, the last 1 140 056. Every tunnel also produced a `DownstreamTunnelSuccessfullyEstablished` entry at setup and a `DownstreamEnd` at the end (202 each). |
| Idle tunnel is cut (control) | PASS | The two tunnels to the silent port ended at 300.0 s: access log `"details":"stream_idle_timeout","dur_ms":300002,"flags":"SI"`, stats `downstream_rq_idle_timeout: 2`, client `EOF (clean close)` at lifetime 5m0.1s. |
| `max_stream_duration: 2 min` ends all streams cleanly | PASS | `./soak.sh t2-msd2m 3 msd=120s sit=300s idle=3600s flush=60s`: `end_reasons=map[EOF (clean close):200] ticks_per_tunnel=[120..120] lifetime min=2m0.1s p50=2m0.11s max=2m0.13s`; access log 202 × `max_duration_timeout` with `dur_ms` 119 999–120 010; `downstream_rq_max_duration_reached: 202`. The client saw a FIN (EOF), never a reset. |

A 1-minute dry run (`logs/dry-*`, `sit=20s flush=15s`) showed the same mechanics at small scale: periodic entries at 15 s, the silent tunnels cut at 20.0 s.

## Test 3: decision latency, Envoy-side caching, in-flight checks

Envoy has no ext_authz response cache. The `ExtAuthz` filter proto at v1.40.2 (go-control-plane v1.40.0, field list extracted from `ext_authz.pb.go`) contains no cache-related field; `clear_route_cache` refers to the route cache. The `envoy.filters.http.cache` filter caches HTTP responses of cacheable requests under RFC 9111 rules and is unrelated to ext_authz decisions (and a CONNECT has no cacheable response). A decision cache therefore lives in the decision service (or in a cache in front of it); nothing was measured "with caching" because there is nothing to turn on.

Runs (`./t3.sh NAME DELAY_MS 1000`): 1000 new CONNECT/s for 20 s, each held 1 s, decision delay injected in both `authz3` instances, in-flight checks sampled every 10 ms in each instance and every 0.5 s from `cluster.authz.upstream_rq_active`.

| Run | Decision delay | Config | CONNECT setup latency | In-flight checks per endpoint (p50 / p99 / max of 10 ms samples) | Envoy `upstream_rq_active` | Outcome |
|:--|:--|:--|:--|:--|:--|:--|
| `t3a` | 0 | final minus retry fix (`t1-hc-retry`) | p50 0.7 ms, p99 3.2 ms | 0 / 0 / 4–5 | 0–2 | 20 000/20 000 `200` |
| `t3c` | 50 ms | ka+od+hc, no retry | p50 51.6 ms, p90 52.7 ms, p99 66 ms, max 117 ms | 25 / 27–28 / 48 | 50–53 | 20 000/20 000 `200` |
| `t3f` | 50 ms | final (retry=1, per-try 0.4 s) | p50 51.7 ms, p99 65.7 ms, max 103 ms | 25 / 27 / 49 | 50–53 | 20 000/20 000 `200`, no retry triggered |
| `t3b` | 50 ms | retry with `max_interval: 0.05s` | p50 107 ms, p99 179 ms | 60 / 139 / 146–148 | 117–138 | 9888 `200`, 10 111 `503` (see Test 1 pitfall) |

Envoy adds about 1.6 ms to the decision latency at 1000/s on this host. The in-flight count follows Little's law: 1000/s × 50 ms = 50 checks in flight per Envoy replica, split evenly over the endpoints; the only excursion above that was the first 100 ms of each run (max 48–49 per endpoint while the connection pools warmed up). Sizing rule from these runs: per Envoy replica, concurrent checks = CONNECT rate × decision latency, with a 2× allowance for start-up bursts; the decision service must also tolerate a failed-retry doubling of load if a retry policy is configured (`t3b` ran at 2.5× the request count).

## Test 4: drain with live tunnels

Setup (`./t4.sh NAME MODE 20`): `r4-envoy-main` with the final configuration and `--drain-time-s 20 --drain-strategy gradual` (`immediate` in one run); `soak -n 200 -rate 100 -probe 1s -probe-for 60s` opens 200 ticking tunnels in the first 2 s and then attempts one fresh CONNECT per second; the drain action is applied at t=8 s (t=20 s in one run). Wall-clock timestamps come from the soak client and the script; the two clocks are the same VM clock.

| Run | Action | Live tunnels | New CONNECTs during drain | Envoy process |
|:--|:--|:--|:--|:--|
| `t4-graceful` | `POST /drain_listeners?graceful` at 09:09:06.0 | All 200 closed with a clean EOF between 09:09:19.4 and 09:09:21.1, that is 13.4–15.1 s after the call; access log `"details":"on_drain_deadline"`, every tunnel had received its ticks until then (`ticks_per_tunnel=[20..23]`). | `200` for every probe until 09:09:26 (one probe got `200` then EOF at 09:09:18.9, caught by the reset); from 09:09:26.96 (+20.9 s) `connect: connection refused`. | Still running 3 minutes later; `/ready` returns 200 and `server.state: 0` although the listener is closed. |
| `t4-graceful-skip` | `POST /drain_listeners?graceful&skip_exit` at 09:10:08.8 | Same: all 200 closed cleanly at +13.4 to +15.0 s (`on_drain_deadline`). | `200` for all 60 probes; the listener was never closed. | Running. |
| `t4-immediate` | `POST /drain_listeners` (no `graceful`) at 09:11:11.6 | Same: all 200 closed cleanly at +13.4 to +15.0 s. | `connection refused` from the first probe after the call (+1 s). | Running. |
| `t4-sigterm` | `docker kill -s TERM` at 09:12:14.37 | All 200 closed at 09:12:14.319–331 (within 12 ms of `caught ENVOY_SIGTERM`), clean EOF on the client; access log `downstream_local_disconnect(purging_socket_that_have_not_progressed_to_connections)`, tunnels were 6–8 s old. | A probe in flight got `200` then EOF; afterwards `dial tcp: i/o timeout` / `no route to host` (the container is gone). | Exited 0 in 14 ms (`shutting down server instance`, `exiting`); `--drain-time-s` is not applied on SIGTERM. |
| `t4-graceful-at20` | `?graceful` applied at t=20 s instead of 8 s | Closed at +13.5 to +15.1 s after the call (09:18:02.0–09:18:03.7 for a call at about 09:17:48.5), so the window is relative to the drain call, not to the tunnel age. | Refused from +21 s. | Running. |
| `t4-graceful-dto60` | `?graceful` with HCM `drain_timeout: 60s` | No tunnel was closed in 100 s (`end_reasons=map[max reached (still open):200]`, 98–100 ticks each). | Refused from +21 s. | Running. |
| `t4-strategy-immediate` | `?graceful` with `--drain-strategy immediate`, call at 09:20:31.9 | Same window: all 200 closed cleanly at 09:20:45.3–09:20:46.9 (+13.4 to +15.0 s). The strategy does not change the tunnel reset window. | `200` until +20 s, refused from +21 s. | Running. |

Mechanism (Envoy 1.40.2 source, `ref/conn_manager_impl.cc` and `ref/drain_close_util.cc`): when a drain starts, every HTTP connection manager arms a "proactive drain" timer at a random point of the window `[drain_time × 2/3, drain_time − drain_timeout]`; when it fires on a connection whose only streams are CONNECT or upgrade tunnels it resets them (`resetTunnelingStreams`, response code details `on_drain_deadline`), because "a GOAWAY does not end them, and they would otherwise all be terminated at the same moment at the end of the drain sequence". With `--drain-time-s 20` and the default HCM `drain_timeout` of 5 s the window is 13.3–15 s, which is what every run showed. When `drain_timeout ≥ drain_time / 3` the window is empty and no tunnel is reset (the `dto60` run). The window is computed from the drain start, so a tunnel accepted during the drain after the window opened is left alone and only ends when the process exits. Listeners are closed at `drain_time` for `?graceful`, immediately for plain `/drain_listeners`, and never for `skip_exit`; in 1.40.2 the process did not exit after the drain in any variant, so the exit has to come from SIGTERM (or the orchestrator) after the drain period.

Rolling-update reading: a preStop hook that calls `POST /drain_listeners?graceful` and then sleeps for `drain_time` gives (a) new CONNECTs still accepted until `drain_time` (the Service endpoint must be withdrawn separately, since `/ready` keeps answering 200), (b) every established tunnel reset with a clean FIN somewhere in the last third of the drain period, spread over `drain_time/3 − drain_timeout`, and (c) a SIGTERM afterwards that kills the remainder at once. Without the preStop, SIGTERM alone resets all tunnels simultaneously within milliseconds.

## Recommended settings (from these runs)

- Decision-service cluster: HTTP/2 with `connection_keepalive {interval: 1s, timeout: 1s}` (bounds a frozen peer to about 1.3 s instead of the request timeout), `connect_timeout: 0.25s`, two or more endpoints, outlier detection with `split_external_local_origin_errors: true`, `consecutive_local_origin_failure: 3`, `consecutive_5xx: 3`, `interval: 1s`, `base_ejection_time: 10s`, plus an active `grpc_health_check` (`interval: 1s`, `unhealthy_threshold: 1`) with `successful_active_health_check_uneject_host: true` so an ejected endpoint is never probed with live CONNECTs. `max_ejection_percent: 100` was used so a single-endpoint failure can always be ejected; with two endpoints the default 10% would eject nothing.
- ext_authz: `timeout: 1s`, `GrpcService.retry_policy` with `num_retries: 1`, `retry_on: connect-failure,reset,refused-stream,unavailable,cancelled,deadline-exceeded`, `previous_hosts` predicate, and `retry_back_off.max_interval` equal to the per-try budget (0.4 s here); never leave `max_interval` at a small backoff value.
- Decision-service sizing: concurrent checks per Envoy replica = CONNECT rate × decision latency (50 at 1000/s and 50 ms), doubled for start-up bursts and for retry load.
- Tunnel timeouts: `stream_idle_timeout: 300s` (cuts a tunnel that carries nothing for 5 minutes, leaves an SSE stream alone), `common_http_protocol_options.idle_timeout: 3600s`, route `max_stream_duration: 7200s`, `delayed_close_timeout: 0.1s`, `access_log_flush_interval: 60s` with `flush_log_on_tunnel_successfully_established: true`.
- Drain: `--drain-time-s D` with the HCM `drain_timeout` left at 5 s (so `D ≥ 16 s` keeps a reset window), preStop `POST /drain_listeners?graceful` followed by a sleep of `D`, `terminationGracePeriodSeconds > D + 5`. Tunnels are reset with a clean FIN between `2D/3` and `D − 5 s`; new CONNECTs are refused from `D`. Use `skip_exit` only if the listener must keep accepting until SIGTERM.

## Caveats

- One Docker VM hosted Envoy, both decision services, the target, and the load generator; latencies include that contention.
- The decision service is a Go gRPC server with no real work; a 50 ms sleep stood in for the decision latency.
- `kill` here removed the container's network namespace; in Kubernetes a deleted pod is also withdrawn from EDS, which this STATIC cluster cannot model. The `pkill -9` variant (process restart on the same IP) was prepared but not run.
- Hot restart (`--restart-epoch`) was not exercised; SIGTERM and the admin drain endpoints were.
- The probe client only attempted one CONNECT per second during drains, so the exact second at which the listener closed is known to ±1 s.

## Cleanup

`./down.sh` removes every `r4-*` container and the `sr4-pub` network; images are kept. Go binaries were built on the host with the r3 module and build caches (`env.sh`).
