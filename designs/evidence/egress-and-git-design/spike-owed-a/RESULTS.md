# Validation owed, spike A (2026-11-02)

Laptop, Docker, arm64, a kind cluster from another spike running beside it. Stand-ins are named as such. No Go `netagent` exists, so `go/sniagent` stands in for its check. Raw output sits under each item's `raw/` directory, and `go/` holds the programs.

## 1. Name-constrained per-pod CA

CA `nc` permits DNS `allowed.oa.test` only, created with Go's `x509` as the Go `netagent` would. CA `ncx` permits `xallowed.oa.test` and excludes all IP ranges. CA `ctl` has no constraints and shares the bundle, so a refusal cannot be a trust-configuration error. CA `unk` is outside the bundle. Each CA acts as a trust anchor. Raw output: `item1/raw/`, table in `item1/raw/matrix.txt`.

Targets: `allowed` (permitted), `denied` (unpermitted DNS name), `sub` (`sub.allowed.oa.test`), `viaint` (unpermitted name through an unconstrained intermediate), `ip` (IP SAN under the DNS-only CA), `ctl` (control), `unk` (unknown CA), `xallowed`, `xdenied`, and `xip` (IP SAN under the CA that excludes IPs).

| client | allowed | denied | sub | viaint | ip | ctl | unk | xallowed | xdenied | xip |
|:--|:--|:--|:--|:--|:--|:--|:--|:--|:--|:--|
| OpenSSL 3.5: curl, git (Alpine), s_client, Python requests/httpx h1 and h2/aiohttp/urllib, pip (default, `PIP_CERT`, legacy-certs), Node fetch, npm (`NODE_EXTRA_CA_CERTS`, `cafile`), reqwest native-tls | ok | REF | ok | REF | ok | ok | REF | ok | REF | REF |
| Go net/http | ok | REF | ok | REF | ok | ok | REF | ok | REF | REF |
| rustls: reqwest (root added, and `SSL_CERT_FILE` through rustls-native-certs), Deno | ok | REF | ok | REF | ok | ok | REF | ok | REF | REF |
| BoringSSL: Bun | ok | REF | ok | REF | ok | ok | REF | ok | REF | REF |
| NSS 3.110: tstclnt, vfychain | ok | REF | ok | REF | ok | ok | REF | ok | REF | REF |
| Chromium (Alpine package, user NSS database) | ok | REF | ok | REF | ok | ok | REF | ok | REF | REF |
| GnuTLS 3.8: git (Debian), wget, gnutls-cli, certtool | ok | REF | ok | REF | REF | ok | REF | ok | REF | REF |
| Java 21 HttpClient, PKCS12 truststore | ok | ok | ok | ok | ok | ok | REF | ok | ok | ok |

- The design's claim about enforcement holds: OpenSSL, Go, rustls, NSS, and Chromium refuse the unpermitted name, and Java's PKIX ignores the constraints. Refusal also holds through an unconstrained intermediate.
- "so for those clients a leaked key cannot sign for any other name" is wrong as written. Under a DNS-only constraint, a leaf for an IP address is accepted by every enforcing client here except GnuTLS, and a leaf for a subdomain of the permitted host is accepted by all of them. RFC 5280 defines both results: a constraint applies only to its own name form, and a DNS subtree admits added labels.
- Adding excluded IP ranges closes the IP case in every enforcing client and breaks no permitted host (columns `xallowed` and `xip`). The subdomain case has no exact-host form.
- GnuTLS, BoringSSL, and Deno also enforce. Debian and Ubuntu git uses GnuTLS (libcurl-gnutls, seen with `ldd`).
- "`NODE_EXTRA_CA_CERTS` is set to the deployer CA alone" conflicts with the injected-configuration text ("points at the added CAs only"). With only the deployer CA in it, Node refused the per-pod CA's leaf (`UNABLE_TO_VERIFY_LEAF_SIGNATURE`; `item1/raw/node-deployer-only.txt`).

Not run: Firefox (mozilla::pkix, which differs from the libnss verifier tstclnt uses) and .NET.

## 2. git push through Envoy's raw tunnel

Stock git 2.49 to stock `git http-backend` behind Go TLS (`go/gitsrv`), at 172.30.50.21 with no route or name from the client, through Envoy 1.40.2 with an ORIGINAL_DST route to the address the stand-in decision pins. Raw output: `item2/raw/t2.out`, `t2b.out`.

| Test | Result |
|:--|:--|
| Control: no route and no name from the client | curl rc 6 by name, rc 28 by address |
| Small push, clone, second push, ref create and delete | all rc 0, server SHA equal |
| 64 MiB, HTTP/1.1 | `Transfer-Encoding: chunked` in the client trace and at the server, 67,129,715 bytes, fsck clean |
| 64 MiB, `http.postBuffer` raised | content-length POST, same SHA |
| 1 GiB chunked, tunnel | 23.8 s, 1,073,906,039 bytes, fsck clean |
| 1 GiB, tunnel versus direct, HTTP/2 | 24.1 s versus 24.2 s |
| Envoy memory.peak during 1 GiB | 20.2 to 21.3 MB (HTTP/2 run), 26.5 to 30.6 MB (HTTP/1.1 run) |
| Proxy hop over TLS, 64 MiB and 1 GiB chunked | rc 0, SHA equal, 23.9 s for 1 GiB |
| pre-receive rejection | message reaches the client, ref unmoved |
| CONNECT refused | 403 for a bad credential, 407 with none, 503 with the decision service down; git reports each |
| Envoy killed during a 1 GiB push | client error, ref absent, no quarantine left, retry landed in 24.5 s |
| `max_stream_duration` 5 s | log shows `max_duration_timeout` at 5,002 ms, client rc 1 at 7.4 s, ref absent |
| Silent 25 s hook, `stream_idle_timeout` 10 s, git's default `receive.keepAlive` | rc 0 at 25.2 s: the keepalive holds the tunnel |
| Same with `receive.keepAlive -1` | client rc 1 at 20.4 s, and the ref landed anyway when the hook ended at 25 s |

git prints "Everything up-to-date" on the two cut cases while exiting 1. With Go's ALPN on the origin, git uses HTTP/2, where no chunked framing exists, so the first run did not test chunking; `http.version=HTTP/1.1` did. No statement in the design is wrong here. The owed item can close. A host that holds a silent hook past the idle timeout with keepalive off gives a failed push whose ref moves, which the design does not state.

## 3. ls-refs with credentials

Gitea 1.24.7 over TLS with a private and a public repository and real tokens. Client: the earlier `go/lsrefs` with `-auth`, `-ca`, and a code and reason in its result. The paths are direct, Envoy raw tunnel (pinned address), and Envoy DFP. All three paths classify alike, and v2 and forced v0 agree. Raw output: `item3/raw/t3a.out`.

| Case | Host status | Requests in v2-first mode | Code and reason |
|:--|:--|:--|:--|
| Valid token, private (branch, annotated tag peeled, `x-access-token` user, no `.git`) | 200 | 1 | ok, SHA equal to `git ls-remote` |
| Valid token, absent ref | 200 | 1 | UNRESOLVABLE `ref_not_found` |
| Invalid token, private | 401 | 2 | UNRESOLVABLE `auth_failed` |
| No credential, private | 401 | 2 | UNRESOLVABLE `auth_failed` |
| Token without repository scope | 403 | 2 | UNRESOLVABLE `auth_failed` |
| Other user's token, private | 404 | 2 | UNRESOLVABLE, no reason in the spec |
| Valid token, absent repository | 404 | 2 | UNRESOLVABLE, no reason in the spec |
| No credential, absent repository | 401 | 2 | UNRESOLVABLE `auth_failed` |
| No credential, public | 200 | 1 | ok |
| Invalid token, public | 401 | 2 | UNRESOLVABLE `auth_failed` |
| CONNECT 403, 407 | none | 0 to the host | UNRESOLVABLE `egress_denied` |
| Decision service down, host stopped | none | 0 | TRANSIENT |

- The design's **Errors** bullet has no entry for a host 401, 403, or 404, and the spec's two reasons (`auth_failed`, `ref_not_found`) do not name a host 404. The mapping above is mine.
- A 401 costs two authenticated requests (v2, then v0), because the fallback runs on any 401, 403, or 404. A front that answers v2 with 400, 403, 404, or 415 needs that fallback, and it worked in each case.
- "with a protocol v0 `info/refs` fallback for a host that refuses v2" omits the form a stock server uses. Stock git without v2 (Gitea with `Git-Protocol` stripped, and `git http-backend` directly) answers the v2 POST with HTTP 200, an empty body, and the result content type; the backend logs `protocol error, expected to get object ID, not 'command=ls-refs'`. The round 6 client read that as a short read and returned TRANSIENT, so it never tried v0. This is the mechanism and was reproduced on two servers. With an empty 200 treated as a refusal, v0 resolves correctly (`item3/raw/raw-v0.out`).

Mirror stand-in (`go/mirls`, the text under "Ref resolution"): gateway client to the mirror, then real Envoy, a fault front, and real Gitea, with a real bare store and a real `git fetch`. 72 of 72 checks pass (`item3/raw/t3b.out`). Earlier runs are kept: their 5 and 4 failures were my own expectations, including a script edit that reset the repository before S14.

- Listed causes answered from the store with `resolvedFromStore` and the confirmation time: host 502, 503, 504, a reset, a hang, a stopped origin (CONNECT 503), a stopped Envoy, a proxy that never replies, a TLS name failure, and an unavailable mint.
- Passed on with confirmations kept: host 429 and 500, and CONNECT 403, 407, 429, and 502.
- Cleared until the next success: injected 401, 403, and 404, a refused mint, and real cases (a repository made private, a bad token, another user's token). A later 503 returned TRANSIENT.
- A deleted ref, a force-push the store lacks, the age limit (scaled to 2 s), and a mirror restart all behave as the text says. 20 sequential calls made 1 upstream call and 20 I3 calls. 8 concurrent calls during a hang made 1 upstream call.
- Time to a store answer: 8.0 s for a hang, a stopped Envoy, or a silent proxy, because the stand-in has only the 8 s limit; 5.0 s for a stopped origin (Envoy's `connect_timeout`). The text's 1.5 s CONNECT budget applies to the gateway's own attempts and not to the mirror's.
- "A successful answer that omits a ref clears its pair" is wrong read literally. `ref-prefix` narrows the answer, so resolving `main` cleared `refs/tags/v1`, and v1 then failed during an outage. Limiting the clearing to refs the request's prefixes cover fixed it.
- With the mirror's I3 healthy and Envoy unable to reach the decision service, Envoy answers CONNECT 503, which is a listed cause, so the store answered. "a decision service outage returns the transient error" holds only when the mirror's own I3 call fails.
- Coalescing kept a success for 5 s after the credential broke, and the 401 appeared at about t+6 s. A repository made private can therefore resolve for up to 5 s.

Not run: Azure DevOps and AWS CodeCommit, which need accounts.

## 4. ClientHello check

`go/sniagent` dials upstream, replies 200, reads and parses the hello before forwarding any byte, and closes on a difference. Each case also ran against the same agent with the check off. The target's certificate covers both names, so the mismatch succeeds without the check. Raw output: `item4/raw/t4.out`, `t4b.out`, `item5/raw/t5d.out`.

| Case | With the check | Without it |
|:--|:--|:--|
| Match, TLS 1.3 and 1.2 (Go, curl, openssl) | pass | pass |
| Mismatch, TLS 1.3 and 1.2 (Go, openssl, curl `--connect-to`) | closed | reaches the target |
| No SNI; IP-literal authority with or without SNI | closed | reaches the target |
| Non-TLS first byte; SSLv2-format hello | closed at once | origin answers |
| Hello in 1-byte TCP segments | pass, 2.98 s | pass |
| Mismatch in 40-byte segments | closed | reaches the target |
| Hello fragmented across TLS records (64 and 1 byte) | pass; a mismatch is closed | pass |
| Same, under a first-record-only reading | closed, whatever the name | |
| Silent client; 3 bytes then a stall | closed at the 5 s deadline | stays open |
| 5-byte segments, 20 ms apart | closed at the 5 s deadline, a slow sender is refused | pass |
| Duplicate `server_name`, two names, record length 65535, 1 MiB hello | closed | |
| Python requests, httpx, Node fetch, Java HttpClient, git | pass | |
| Through Squid over TLS | match passes, mismatch closed, unlisted host 403 | stock Squid passes the mismatch |

- Hello sizes from real clients ranged from 214 to 1,606 bytes against a 1,500-byte MTU, so the check has to read by record length. The check has to reassemble across records, because a fragmented hello is valid TLS and the target accepted it.
- curl sends the authority as typed (`TARGET.OA.TEST:443`, `target.oa.test.:443`) and a lower-case, dot-free SNI, and requests does the same for the trailing dot. A raw comparison closed these. The check needs the shared canonicalizer.
- Limits the check leaves, as measured: with ECH the agent saw the outer name and passed, and the server received inner SNI `secret.oa.test`. A matching SNI with `Host: other.oa.test` passes.
- Added latency, CONNECT plus handshake, mean of p50 over 5 alternating runs of 1000: TLS 1.3 967 µs versus 950 µs (+17 µs), TLS 1.2 842 µs versus 838 µs (+4 µs). Through Squid, 5 runs of 500: 1.427 ms versus 1.406 ms (+21 µs). Each delta is inside the variation between runs (TLS 1.3 p50 ranged 874 to 1,028 µs). First byte to decision for a hello delivered whole: p50 2 µs, p99 10 µs. Parser alone: p50 0.29 µs, p99 0.88 µs.

## 5. Level 1 probe

`go/l1probe` is the probe as a program, with preflight and full modes, against Squid 7.7 over TLS. A stand-in serves the document and probe credentials and rotates a canary, and a real sync job renders and reconfigures. Times are scaled: rotation 30 s, apply window 15 s, sync 3 s. The truth column comes from curl and the target's connection counters, independently of the probe. Raw output: `item5/raw/t5.out`, `t5b.out`, `t5c.out`, `t5d.out`.

| Configuration | Preflight | Full | Full + address control | Defect the truth check shows |
|:--|:--|:--|:--|:--|
| correct (`-n` and without) | pass | pass | pass | none |
| no authentication | FAIL | FAIL | FAIL | no credential reaches a host |
| authentication with no password check (`basic_fake_auth`) | FAIL | FAIL | FAIL | a wrong password reaches a host |
| refuses after authentication | pass | FAIL | FAIL | nothing reachable |
| refuses everything | FAIL | FAIL | FAIL | nothing reachable |
| no TLS; certificate for another name and CA | FAIL | FAIL | FAIL | |
| no address rule; rule after the allow rules | pass | FAIL | FAIL | private name reached (200), literals attempted (503) |
| literals dropped from the list, no rule, `-n` | pass | FAIL (name only) | FAIL | same |
| literals dropped, rule covers RFC 1918 only, `-n` | pass | **false pass** | FAIL | a metadata name and a loopback name attempted (503) |
| rule covers only the probed ranges | pass | **false pass** | **false pass** | 10.77.0.20 reached |
| status 200 then a close (fake proxy) | not run | **false pass** with a status-only control | FAIL with a handshake | carries no traffic |

Stopped sync job: first FAIL at 28.4 s in the rerun and 19.7 s in the first run, each being the time to the next rotation plus the window. Until then the probe passes on a stale proxy. A frozen canary source passes until a separate freshness assertion fires at about 17.6 s.

- "Each must be refused before any connection is attempted" cannot be read from a 403 alone: a list refusal and a rule refusal look alike, so a renderer that drops the literals yields a false pass. Wildcard siblings that resolve publicly and privately, one of which must return 200, separated them.
- "The proxy then enforces the identity's host list" fails on the reference rendering. Squid's `dstdomain` tries a reverse lookup on a literal, so `CONNECT 198.19.50.66:443`, on no list, returned 200 because its reverse name fell in `*.wild.oa.test`. With `dstdomain -n` it returned 403. The test's reverse name came from `/etc/hosts`; a real attacker sets a PTR record. It also explains why dropped literals still got 503 before `-n`.
- "with the list `lenny-egress` uses" breaks Squid: `::ffff:0:0/96` in a `dst` ACL refuses every IPv4 destination (`raw/t5-first-run-mapped-rule.out`). Without that range, Squid already refuses `[::ffff:127.0.0.1]`, `[::ffff:172.30.50.20]`, `[64:ff9b::7f00:1]`, `127.1`, `2130706433`, and `0x7f.1`.
- "stock Squid answers it with a 403 from its access rule" holds only for a generation still in the credential cache. A never-used retired generation gets 407, so the probe accepts both.
- "A canary generation retired, and a canary host added, within the last apply window must already be in effect" contradicts the window. Read literally, it failed a healthy proxy in 1 of 50 runs, right after a rotation. The working rule is the newest rotation older than the window.
- The retired check must use a host the generation could reach. Aimed at the new canary host, a stale proxy refused it because of the list, which is another false pass; the rerun shows 200.
- Outside the probe by design: expiry, log content, `Proxy-Authorization` stripping.

Harness faults found, none a mechanism result: my stand-in did not end a tunnel when the client closed over a TLS hop, so Squid held 10,015 file descriptors and hit my 256 MiB limit (OOM) in `t5b.out`. After the fix Squid stayed at 173 MiB with 15 descriptors over 5,000 tunnels (`t5d.out`). While that fault held, Squid used about 0.2 MiB per open tunnel. The failing expectations in the first `t3b` runs and the first TLS hop test (no IP SAN on the proxy certificate) were also mine.

Not run: a commercial proxy and an EDL consumer, which need products, and the Go `netagent`, which does not exist.

## Cleanup

All `oa-` containers, networks, volumes, and images are removed. The images pulled for this spike remain: `gitea/gitea:1.24`, `rust:1-slim-bookworm`, `denoland/deno:2.5.0`, and `oven/bun:1.2`.
