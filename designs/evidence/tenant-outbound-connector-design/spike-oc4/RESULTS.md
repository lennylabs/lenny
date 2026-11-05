# Outbound connector, decision 4: TLS from Envoy to the outbound gateway (2026-11-05)

Owed by `tenant-outbound-connector-design.md` "Spikes owed", **Envoy**. Docker Desktop, arm64, `envoyproxy/envoy:v1.40.2` and `v1.39.6`. Every component ran in an `oc-` container on network `oc-net` (198.20.0.0/24).

## Harness

| Path | Purpose |
|:--|:--|
| `gen.py OUT MSD MODE` | Renders the Envoy bootstrap: a CONNECT listener, `custom_response`, ext_authz in `http` (h2c) or `grpc` mode, and three `ORIGINAL_DST` clusters reading the replica address from ext_authz dynamic metadata (`x-lenny-upstream`): `outbound_tls` with an `UpstreamTlsContext` that sets SNI and verifies the egress CA and the exact SAN `lenny-outbound-gateway.lenny-outbound.svc`, `outbound_tsm` with only `transport_socket_matches`, and `outbound_plain` with no TLS. Each route forwards the CONNECT (no `connect_config`), removes Lenny's routing headers, sets `max_stream_duration`, and maps a replica's 407 to a fixed 502 and its 403 to a fixed 403. |
| `authzgo-main.go.txt` | Decision stand-in, HTTP ext_authz over h2c. |
| `authzg-main.go.txt` | Decision stand-in, gRPC ext_authz. |
| `gw.py` | Outbound gateway replica stand-in: a TLS-only CONNECT listener on 8443 and a plain one on 8080, logging SNI, ALPN, and request headers. |
| `client.py` | Raw CONNECT client sending the pod's `Proxy-Authorization`. |
| `mkcerts.sh` | Egress CA, a foreign CA, and leaves with the right SAN, a wrong SAN, and the right SAN under the foreign CA. Keys are not kept here. |
| `run.sh TAG MSD MODE` | One run. Raw output is under `raw/`. |

## Results

Identical on 1.40.2 and 1.39.6 unless stated.

| Case | Result |
|:--|:--|
| Allow, replica with the right SAN | 200, data relayed. The replica saw TLS, SNI `lenny-outbound-gateway.lenny-outbound.svc`, ALPN `http/1.1`, and `Proxy-Authorization` equal to the grant. The pod's credential and `x-lenny-*` headers did not reach it. |
| Allow with the pod credential removed and no grant | 200, and the replica saw no `Proxy-Authorization`. |
| Replica with a wrong SAN | 503 before any request reached it. Envoy recorded `CERTIFICATE_VERIFY_FAILED` from the SAN matcher. |
| Replica under a foreign CA | 503, `unable to get local issuer certificate`. |
| ext_authz deny 403 | 403 with the decision's body and `x-lenny-deny-reason`, in both modes. |
| ext_authz deny 503, gRPC mode | 503 with the decision's body and `x-lenny-deny-reason`. |
| ext_authz deny 503, HTTP mode | 503 with an empty body and no reason header. Envoy logs `UAEX` `ext_authz_error`, because it treats a 5xx from an HTTP authorization server as a failure. |
| Replica 503 (`no_live_agent`) | 503 with the replica's fixed body. |
| Replica 407 with `Proxy-Authenticate` | Fixed 502, with no upstream headers or body. |
| Replica 403 with its own header and body | Fixed 403, with no upstream headers or body. |
| `max_stream_duration` 5 s | Tunnel closed at 5.1 s, after 5 ticks. |
| `transport_socket_matches` on the cluster with no default TLS | No TLS was used, and the TLS-only replica failed the handshake. An `ORIGINAL_DST` host created from metadata carries no endpoint metadata to match, so TLS needs its own cluster. |
| Control: plain cluster to the TLS port, and to the plain port | Failed, and worked. The TLS case above therefore used TLS. |

Not covered: a level 2 proxy, a real outbound gateway or agent, cert-manager rotation of the leaf, HTTP/2 between Envoy and the replica, and load.
