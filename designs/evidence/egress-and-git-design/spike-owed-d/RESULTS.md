# Validation owed, spike D (2026-11-03)

This run covers the laptop-runnable items of "Validation still owed" in revision 42 of `designs/egress-and-git-design.md`.

Environment: Docker Desktop 29.4.3, 12 CPUs, 8 GB, arm64, kernel 6.12.76-linuxkit. Envoy 1.40.2, Squid 7.7, Postgres 17.11, PgBouncer 1.23.1, MinIO, Firefox ESR 153.6.0, .NET 8.0.33, and .NET 10.0.14. Every server, client, and analysis step ran in an `od-` container. Host commands were the Docker CLI, shell drivers, `git status`, and `perl` for timestamps. The repository was not edited.

Layout: programs are under `go/cmd/`, drivers are `i*.sh`, and raw output is under `raw/`. `raw/invalid/` holds runs a harness defect spoiled.

`go/cmd/netagent` and the evidence programs are prototypes written to the design's text. Results describe those prototypes and do not describe a shipped component. Where the prototype needed a rule the text does not state, this file says so.

## Design statements the evidence contradicts or qualifies

Each entry has its evidence in the section named.

| Statement | Evidence | Section |
|:--|:--|:--|
| "Memory is about 120 KiB per tunnel, so 64 MiB fits about 400 tunnels by estimate" | The count holds for opaque and for intercepted HTTP/1.1 on both sides, with GOMEMLIMIT unset. Other kinds range from 250 to 900 held. A soft memory limit raises the count at a cost of 0.6 to 2.4 CPUs. | 1 |
| "A `netagent` that gets a 407 refreshes its credential once and retries." | A retired generation in Squid's credential cache gets a 403, so no refresh happens. 3 of 3 CONNECTs failed until a refresh on 403 was added. | 2 |
| "For each CONNECT, `netagent` asks the decision service on I7" | True for opaque tunnels (200 of 200). On a terminated host, 3 new client CONNECTs made 0 decisions and succeeded through a pooled tunnel after the decision had been revoked. | 2 |
| The client proxy profile's trust variables reach the tools in the pod | Firefox ignores `SSL_CERT_FILE` and returned `SEC_ERROR_UNKNOWN_ISSUER`. It needs its policy file or an NSS database. | 3 |
| "a refused decision records no gap if it landed and a gap record naming the refusal if it did not" | With a frozen replica, 130 refused sequence numbers had neither a stored record nor a gap record. Ingest never received them, and the producer assumed ingest would settle them. | 4.1 |
| "writes a gap record through another replica for the range between the last durable and the last sent sequence" | Correct after a kill. After a freeze, the replica flushes later, so 102,708 sequence numbers were both stored and announced as lost. A gap record means "may be lost". | 4.1 |
| "A replica incarnation with no closing row marks a loss window" | A live incarnation has no closing row either. The lost access log entries spanned about 30 s before the kill, and only 3 of 1,273 fell after the incarnation's last flush. | 4.2 |
| "Commit bound, one transaction per tenant: 0 of 44,933 at 500 per second, and 122 of 179,677 (0.07%) at 2,000 per second" | Not reproduced: 331 of about 90,000 at 500/s and 1,309 of about 360,000 at 2,000/s. Run-to-run variation exceeds any difference between the modes. | 4.3 |
| "A flood of distinct hosts defeats aggregation, and those records are lost at the flooding tenant's bound with a gap record." | At 3,000/s nothing was lost. At 100,000/s records were lost and all were announced. | 4.5 |
| "the compacted row keeps their ordered digests", beside "A compacted row holds the digest of its ordered part list" | The two sentences conflict. The second one is what reduced the row. | 4.8 |
| "each compaction writes an anchor event naming what it merged", given the 5 GB per day objection to lists in rows | An anchor that names its batches measured 5,047 bytes, about 3.6 GB per day at the bound. The list moves into the chain unless the anchor holds a digest. | 4.8 |
| "so the compactor cannot drop or rewrite a batch unseen", with "a digest of the tenant's manifests goes into the §11.7 chain" | If interval anchors hold only a digest, a compactor that drops a batch passes the verifier. The sequence audit still shows the hole. | 4.8 |
| "Erasure destroys the key and invalidates ingest's cached copy" | With chunks, 300 payloads buffered in plaintext at destruction were stored under the new key and stayed readable. | 4.7 |
| "Each consumer holds a cursor over stable positions (producer, incarnation, stream, tenant, and sequence)" | A single sequence per position lost 6,009 and 6,111 records when a gap record landed before the records it named. | 5.1 |
| "Each manifest row has a held marker that retention, the compactor, and `EvidenceStore.DeleteByTenant` honour" | All three honoured it. Key destruction is not in that list, and destroying a held session's key made the held payload unreadable. | 5.2 |
| "`producer` ... The verified producing instance" | The SAN names the Service, so two decision replicas share one `producer`. Only `incarnation` separates them. | 5.3 |
| "after each fetch the parent re-chowns the store's contents" | On NFS, the non-root parent's `CAP_CHOWN` has no effect on the server: 0 of 3 entries changed owner, each with `EPERM`. | 7 |
| "removing setuid and setgid bits and ACL entries", extended to hard links | Removing every name with more than one link removed the store's `HEAD` in 4 of 4 runs. | 6 |

The text is silent on these, and a test showed each one matters:

- A retention run with no chain event makes the verifier report 191 absent batches and 30 anchors that do not recompute (section 5.2).
- A 503 that Envoy itself returns has no reason header, so `netagent` does not retry it (section 5.3).
- A gap record's range can span stored records, because drops between them are not contiguous (section 4.5).

## 1. Tunnel count at a hard 64 MiB limit

Setup: `--memory 64m --memory-swap 64m`, no CPU limit, idle tick every 10 s. Pod networking and the path through Envoy with server TLS follow spike C. The client and the poller run outside the limited cgroup, and each reading is appended to a file as it is taken. Raw: `raw/i1-hardlimit.txt`, `raw/i1-table.txt`, `raw/i1b-sustain.txt`, and per-reading logs in `raw/i1/` and `raw/i1b/`.

### Streams up when the out-of-memory kill came

Ramp of 100 streams/s, three runs per cell, all ended in an out-of-memory kill, with 0 client failures before the kill. Kind `cX-uY` is client HTTP/X and upstream HTTP/Y. `gz` decodes a gzip body.

| Kind | GOMEMLIMIT unset, median (runs) | GOMEMLIMIT=50MiB, median (runs) |
|:--|:--|:--|
| Opaque | 478 (489, 435, 478) | 1,279 (1,279, 1,235, 1,296) |
| Intercepted c1-u1 | 440 (448, 440, 433) | 586 (577, 586, 609) |
| Intercepted c2-u1 | 565 (565, 567, 539) | 734 (737, 729, 734) |
| Intercepted c1-u2 | 869 (828, 869, 880) | 1,175 (1,193, 1,175, 1,158) |
| Intercepted c2-u2 | 1,227 (1,227, 1,250, 1,184) | 1,624 (1,617, 1,624, 1,725) |
| gz c1-u1 | 349 (317, 349, 356) | 464 (464, 447, 465) |
| gz c2-u1 | 389 (357, 393, 389) | 533 (538, 524, 533) |
| gz c1-u2 | 591 (602, 591, 569) | 750 (750, 750, 717) |
| gz c2-u2 | 928 (915, 997, 928) | 1,060 (965, 1,060, 1,072) |

A 20 streams/s ramp, two runs: opaque 502 and 496 unset, 1,288 and 1,290 at 50 MiB; c1-u1 469 and 461 unset, 610 and 619 at 50 MiB; c2-u2 1,235 and 1,162 unset, 1,480 and 1,500 at 50 MiB.

### What holds

The ramp peak overstates what survives, because memory keeps growing after the ramp. Each row below ramps at 50 streams/s, then holds for 120 s. CPU is the mean over the run, with no CPU limit.

| Kind | Soft limit | Streams | Result | CPU |
|:--|:--|:--|:--|:--|
| Opaque | unset | 300, 400 | held | 12m, 14m |
| Opaque | 50 MiB | 600 | killed about 55 s after the ramp | |
| Opaque | 50 MiB | 800 | killed | |
| Opaque | 50 MiB | 1,000 | held | 2,364m |
| Opaque | 50 MiB | 1,150 | killed in one run; a spoiled earlier run was alive at 2,224m | |
| Opaque | 40 MiB | 500, 700 | held | 872m, 1,510m |
| Opaque | 40 MiB | 900 | killed | |
| Opaque | 30 MiB | 500 | held, peak 64.0 MiB | 856m |
| c1-u1 | unset | 300, 400 | held, peak 58.7 and 64.0 MiB | 16m, 21m |
| c1-u1 | 50 MiB | 400, 520 | held | 20m, 1,275m |
| c1-u1 | 40 MiB | 400, 500 | held | 894m, 1,206m |
| c1-u1 | 30 MiB | 400 | held, peak 64.0 MiB | 888m |
| c2-u2 | unset | 900 | held | 39m |
| c2-u2 | 50 MiB | 1,400 | killed | |
| c2-u2 | 40 MiB | 1,200 | held | 1,755m |
| gz c1-u1 | unset | 250 | held | 14m |
| gz c1-u1 | 50 MiB | 350 | held | 790m |
| gz c1-u1 | 40 MiB | 300 | held | 627m |

With a tick every 1 s and GOMEMLIMIT unset, 300 opaque streams held at 48m, and 300 c1-u1 streams held at 57m.

A new connection beside a surviving hold took p50 9 to 20 ms and p99 20 to 88 ms at 5/s.

Findings:

- About 400 held tunnels is measured for opaque and c1-u1 with no soft limit, with no margin: the peak reached 64.0 MiB at 400.
- GOMEMLIMIT=50MiB is too high for a 64 MiB limit. Kernel memory (4 to 11 MiB) and file pages sit outside it, and the outcome is non-monotonic: 600 and 800 died while 1,000 held. The 50 MiB row killed at 600 reached anon 56.7 MiB, above the soft limit.
- A soft limit near the working set costs 0.6 to 2.4 CPUs on idle tunnels. That cost does not fit the 50m request, and the design already refuses a CPU limit below 250m. The cause is continuous GC. I did not profile it.
- These figures are for idle streams with small ticks. Stalled consumers are spike C's separate case.

## 2. Level 1 with the Go `netagent` against Squid

`netagent -level1` asks an I7 stand-in per CONNECT, fetches a generation credential from an I8 stand-in, requires verified TLS to the proxy, reads the ClientHello, enforces the tunnel lifetime, closes tunnels on a binding change, and reports opens and closes. Squid 7.7 uses the reference rendering (`dstdomain -n` with the address rule) and a sync job. The I7 timeout of 2 s is a prototype choice. Raw: `raw/i2-level1.txt`, which holds four passes.

### Per-CONNECT decision

- 200 sequential CONNECTs gave 200 decisions, 200 Squid lines naming `pod-a.g1`, and 200 target connections.
- Decision deny on a host the identity lists: client 403, 0 Squid lines, and 0 target connections.
- Decision allow on a host the identity does not list: client 503 with reason `level1_proxy_refused_allowed_destination status=403`, and Squid logged `TCP_DENIED/403`.
- Decision service frozen and stopped: client 503 after 2.0 s, 0 Squid lines, and recovery on the first attempt after restart.
- I8 refusing with no held credential: 503, recovering when I8 returned. Proxy gone: 503, recovering at attempt 1 once Squid was recreated.
- Cost at 50/s, with and without the decision: p50 3.55 and 3.17 ms, p99 7.51 and 6.89 ms. At 200/s: p50 2.01 and 2.16 ms, p99 5.25 and 8.79 ms. These are inside run-to-run variation. Squid used 63 to 64m at 50/s, 140 to 155m at 200/s, and 38 to 43 MiB. 2 of 4,000 requests failed at 200/s in both modes, cause not examined.

Terminated hosts: the upstream `http.Transport` pool reuses a tunnel. 20 requests after the first added 0 decisions. After the decision service stopped allowing `api.example.test`, 3 new client CONNECTs succeeded with 0 decisions, until a binding change closed the tunnels, after which the request got 403. The default proxy decides per upstream CONNECT as well, so this applies beyond level 1. The design states one tunnel per call for the resolver and states nothing for `inject`.

A private destination: the decision's address was dialled directly, with 0 Squid lines.

### Identity lists

- pod-a reaches its list and gets 503 for pod-b's host. pod-b reaches its list and gets 503 for pod-a's host.
- A list edit reached a new CONNECT on attempt 6 of a 0.25 s loop with a 2 s sync.
- A tunnel opened before the edit lasted until the client's 12 s timeout, and a new CONNECT after the edit got 503.
- The credential still worked from another container after the binding ended (connect 200), as the design's table states.

### TLS required

| Case | Result | Squid lines added |
|:--|:--|:--|
| No `-proxy-ca` | `netagent` refuses to start | |
| Plaintext listener | 503. The listener saw only a TLS ClientHello, and 0 credentials. A curl control leaked the credential to the same listener. | |
| Squid on `http_port` | 503, "first record does not look like a TLS handshake" | 1 |
| Certificate from another CA | 503, certificate verification failed | 1 |
| Wrong server name | 503, certificate verification failed | 1 |

The added Squid line in the last three is the failed connection.

### Credentials

- A wrong password from I8: 407, one refresh, 407 again, 503. The next CONNECT, with a good password from I8, refreshed and passed.
- Rotation in the design's order: both generations passed during overlap.
- `netagent` holding g1 after g1 retired: Squid answered 403 from its access rule, as its credential was cached, so the 407-only refresh never ran. 3 of 3 CONNECTs got 503. The same run with a refresh on 403 passed on the first CONNECT.

The design's probe paragraph already records this 403, and the refresh rule does not account for it. A long-lived pod that keeps a generation past retirement stays refused until a binding change. The text has no periodic refresh.

### Server-name check, lifetime, and reports

- Fronting through `netagent`: tunnel closed, target saw no `sni=other.oa.test`. The same credential straight to Squid: connect 200, http 200, and the target saw the name.
- An upper-case authority with a trailing dot passed. A tunnel with no TLS was closed.
- `-tunnel-max 3s` ended a tunnel at 3.01 s. A binding change ended one within about 0.1 s.
- Lost subscription with `-grace 5s`: the tunnel ended about 20.5 s after the stop. The long poll had no HTTP/2 ping, so detection took 15 s. This is a prototype defect.
- 30 tunnels gave 30 opens and 30 closes in `netagent`, and 30 Squid lines. Reports reached I7 in 2 calls.

### The probe

Correct configuration with 50 new connections/s through `netagent` beside it: preflight PASS, full PASS on each of 5 runs (15 to 19 checks), and traffic ok=1,999, fail=1, p50 4.91 ms.

| Squid variant | Probe | `netagent` to a listed host | `netagent` to a private name |
|:--|:--|:--|:--|
| noauth | FAIL | 200 | 503 |
| fakeauth | FAIL | 200 | 503 |
| refuseall | FAIL | 503 | 503 |
| noaddr | FAIL | 200 | **200, reached** |
| addrafter | FAIL | 200 | **200, reached** |
| partialaddr-n | **PASS, false pass already known from spike A** | 200 | 503 |
| notls, wrongca | preflight FAIL | | |

`netagent` does not compensate for a proxy lacking the address rule. The design states this.

git through level 1, opaque tunnel: clone of 2 commits and a push landed with the same SHA, with 2 decisions and 2 Squid tunnels.

## 3. Name-constrained per-pod CA on Firefox and .NET

CAs came from Go's `x509` with the template the Go `netagent` uses. Each case is on its own port. Raw: `raw/i3-ca.txt`, `raw/i3-firefox-codes.txt`.

| Leaf | Firefox, policy or NSS | .NET 8 and 10, default handler |
|:--|:--|:--|
| Permitted host | served | served |
| Outside the constraints | `SEC_ERROR_CERT_NOT_IN_NAME_SPACE` | refused, `HasNotPermittedNameConstraint` |
| Subdomain of a permitted host | served | served |
| IP leaf, CA excluding IP ranges | `SEC_ERROR_CERT_NOT_IN_NAME_SPACE` | refused, `HasExcludedNameConstraint` |
| Outside, through an unconstrained intermediate | `SEC_ERROR_CERT_NOT_IN_NAME_SPACE` | refused, `HasNotPermittedNameConstraint` |
| Control: same outside name, unconstrained CA in the same trust set | served | served |
| CA not installed | `SEC_ERROR_UNKNOWN_ISSUER` | refused, `PartialChain` |
| IP leaf, DNS-only constraints | served | served |
| Outside, DNS-only constraints | `SEC_ERROR_CERT_NOT_IN_NAME_SPACE` | refused, `HasNotPermittedNameConstraint` |
| `*.nc.test` under a CA permitting `perm.nc.test` | `SEC_ERROR_CERT_NOT_IN_NAME_SPACE` | refused, `HasNotPermittedNameConstraint` |

Policy and NSS installs gave identical results. .NET gave the same results with a chain-observing callback and with `CustomRootTrust`. The server saw no request on any refused case.

These results extend the design's list of enforcing clients to Firefox and .NET. The IP-range exclusion is needed for both, as the design states.

End to end through the real `netagent`:

- Firefox with the CA through policy and the proxy in preferences received the echoed request from the bound host, and a leaf for `evil.example.test` signed by the real per-pod key got `SEC_ERROR_CERT_NOT_IN_NAME_SPACE`. My script's "NOT SERVED" label on the `api.example.test` line is wrong: it searches for the matrix server's text, and the body shows the echo.
- Firefox with only `HTTPS_PROXY`, `https_proxy`, and `SSL_CERT_FILE`: `SEC_ERROR_UNKNOWN_ISSUER`. The pod's network has no route except the proxy, so it used the environment's proxy and ignored the trust variable.
- .NET 8 and 10 with `HTTPS_PROXY` and `SSL_CERT_FILE` at a bundle: HTTP 200 with the injected header masked in the echo, and the `evil.example.test` leaf refused.

`SSL_CERT_DIR` alone: .NET accepted a CA in that directory and refused a CA outside it. This does not show public roots dropping out, because no public host was used.

Firefox's enterprise-roots setting and `p11-kit` were not tested.

## 4. Evidence changes revision 42 added

Setup: two evidence replicas, one decision stand-in, Envoy with `egress.accesslog`, MinIO, Postgres, and PgBouncer in transaction mode. 2,000 CONNECTs/s, 2,500 tenants, 25 regulated, unless stated. Truth for audits is the producer's own high-water marks, which a consumer does not have. Raw: `raw/i4a-replica-loss.txt`, `raw/i4b-rest.txt`, and `raw/i8-reruns.txt`.

### 4.1 Durable-sequence report and the producer's gap record

| Case | Lost tails | Holes between records | Stored and in a gap record |
|:--|:--|:--|:--|
| SIGKILL, load ends 5 s later, earlier behaviour | 2,438 with **0 announced**, plus 7 positions with nothing stored | 25,946 with 0 announced | 0 |
| Same, revision 42, run 1 | 1,878, all announced | 25,838, all announced | 0 |
| Same, revision 42, run 2 | 1,632, all announced | 25,505, all announced | 0 |
| SIGKILL, restart 20 s later, load continues | 0 | 99,462, all announced | 0 |
| SIGTERM with intake closed | 0 | 40, all announced (refused during the drain) | 0 |
| Frozen 30 s, then released | 0 | 130 with **0 announced** | 102,708 |
| Frozen 30 s, producer names unsettled refusals | 0 | 21, all announced | 103,490 |

Each lost tail appeared after a kill. A kill costs one gap record per position with a tail: 2,276 to 2,500 records.

The frozen case needs two consequences stated. A gap record is written when the replica stops answering, and the replica flushes on release, so the record claims a loss that did not happen. 311 records were also stored twice, because a retried request reached both replicas. A consumer has to treat a gap record as a statement of possible loss.

The 130 unannounced numbers are refused commits the frozen replica never received. The design leaves settling to ingest in that case. Having the producer name every refusal it cannot settle closed it, at the cost of 301 false gaps.

### 4.2 Replica incarnation rows and `egress.accesslog`

A graceful stop wrote the closing row, and the two streams matched: 456,216 and 456,216 in the first pass, 455,266 and 455,266 in the rerun.

After SIGKILL, run 1 had 266,237 tunnel entries, 245,611 access log entries, and 1,273 tunnel entries with no access log entry. Those are visible only where the tunnel record survived, so 1,273 undercounts. S2 had 1,767 by the same measure.

The incarnation row has three limits:

- All three rows lacked a closing row, and two of the three were live. A consumer cannot tell a live incarnation from a dead one until another row appears, and a Deployment's pods have no replica index to link them. My tool uses the prototype's `REPLICA` for that link.
- The window from last flush to next start held 3 of 1,273 entries (run 1) and 85 of 1,767 (S2). The lost entries spanned about 30 to 40 s before the kill, because a batch waits up to a flush interval. A window needs the flush interval subtracted from its start. No lost entry fell earlier than that.
- Run 2 lost 0 access log entries by this measure, with the same open row. Its access log count exceeded the tunnel count, which is consistent with Envoy's streams being on the surviving replica, though I did not read Envoy's connection state. The row marks possible loss across every tenant, and it cannot say which.

### 4.3 One transaction per tenant

All tenants regulated. A commit group holds about one record on this disk, so commits are about one transaction each. `pg_test_fsync` reported 83 microseconds per `fdatasync`.

| Mode | Rate | Commits | Refused | Commit p50 |
|:--|:--|:--|:--|:--|
| Per tenant | 500/s, 180 s | about 90,000 | 331 (0.37%) | 0.5 ms |
| Per tenant | 2,000/s, 180 s | about 360,000 | 1,309 (0.36%) | 0.5 ms |
| Sentinel, single transaction | 2,000/s, 120 s | about 240,000 | 347 (0.14%) | 0.4 ms |
| Per tenant, manifest too | 2,000/s, 120 s | about 240,000 | 499 (0.21%) | 0.4 ms |
| Per tenant, the first row of 4.4 | 2,000/s, 120 s | about 240,000 | 1,780 (0.74%) | 0.5 ms |

Refusals come in windows of hundreds to about 1,000, which matches spike C's `WalSync` stalls. Repeats of one mode differ by more than the modes do, so this run shows no cost and no benefit from the mode. Group commit size stayed at 1. Postgres used 315m per tenant against 262m under the sentinel.

Manifest writes per tenant, no regulated tenants, 150 s: Postgres 7m to 58m, PgBouncer 1m to 13m, and CONNECT p99 2.4 to 3.2 ms. One row is about one transaction, about 100/s here. The text requires per-tenant transactions for the write-ahead commit only, and the prototype's drain and anchors still use the sentinel.

### 4.4 The retry on the distinct 503 reason

Each row is a single 120 s run at 2,000/s, all regulated. The stall is `docker pause` on Postgres every 15 s. The retry is implemented at the load client, which stands where `netagent` does. Reported latency for a retried CONNECT starts at the retry, so it leaves out the first attempt and the delay.

| Stall | Retry | First refusals | Visible to the client | Decisions made |
|:--|:--|:--|:--|:--|
| none | none | 1,780 | 1,780 | 239,941 |
| none | 100 ms | 485 | 0 | 240,425 |
| none | 500 ms | 505 | 0 | 240,448 |
| none | 200 ms + up to 300 ms | 370 | 0 | 240,317 |
| 0.4 s | none | 5,388 | 5,388 | 239,942 |
| 0.4 s | 100 ms | 5,475 | 1,481 | 245,412 |
| 0.4 s | 500 ms | 5,392 | 0 | 245,334 |
| 0.4 s | 200 ms + up to 300 ms | 4,905 | 12 | 244,835 |
| 2 s | none | 30,095 | 30,095 | 239,941 |
| 2 s | 100 ms | 30,667 | 26,641 | 270,612 |
| 2 s | 500 ms | 32,226 | 22,138 | 272,170 |
| 2 s | 200 ms + up to 300 ms | 31,755 | 24,004 | 271,694 |

The retry reduces refusals when its delay outlasts the stall, and it reduces them by 11% to 26% otherwise. It adds load equal to the refusals: 2% at a 0.4 s stall and 13% at a 2 s stall, all of it during the stall. CPU in Postgres and ingest moved by less than run-to-run variation. Commit latency stayed pinned at the 150 ms bound during stalls with and without the retry.

Late-landing commits rise with the retry: 30,062 at a 2 s stall with no retry, and 57,235 with a 100 ms retry. Each one is a stored allow with no tunnel, which the design already says proves no connection.

### 4.5 Aggregates over contiguous ranges

Beside 500/s, 90 s. Lookups resolve a sampled decision identifier from a 403.

| Flood | Sequence numbers | Stored records | Stored size | Gaps | Lookups |
|:--|:--|:--|:--|:--|:--|
| 3,000/s, one host | 269,888 | 4 | 1.1 KB | 0 | 398 aggregate, 2 whole |
| 3,000/s, two hosts alternating | 269,895 | 247,908 | 6.3 MB | 0 | 129 aggregate, 271 whole |
| 3,000/s, new host each | 269,885 | 269,885 | 10 MB | 0 | 400 whole |
| 1,000/s of one host, tenant with about 50 other records/s | 94,474 | 13,278 | 438 KB | 0 | 365 aggregate, 35 whole |

Every sampled identifier resolved, before and after compaction, in p50 3 to 20 ms and p99 22 to 45 ms on local MinIO. Quiet tenants had 0 gaps in every case.

Requiring contiguity costs compression. Two alternating hosts reduce the record count by 8%, where one host reduces it by a factor of 67,000. A flood with other traffic reduces it by a factor of 7.

Round 11's 282,281 records dropped at 3,000/s of distinct hosts did not recur: 0 were dropped. In spike C's code the aggregate path returned before the size-bound check, so its batch never detached early. This is my reading of the code, and I did not rerun the old path.

At 100,000/s for 45 s through the producer's flood path, quiet tenants had 0 gaps:

- New hosts: 1,046,728 present, 827,177 missing and all announced, plus 1,099 in tails and all announced. 595,112 were dropped by the producer and 233,166 by ingest.
- One host: 1,260,326 present in 1,000 records and 30 KB, and 592,480 missing and all announced.

Gap records overlapped stored records by 967,997 and 1,055,665 sequence numbers in those two runs. A gap record's `from` and `to` span drops that were not contiguous.

### 4.6 Per-session sequences

One chunk of session s3 was lost in place, with s3 and s4 interleaved.

- One sequence per tenant: the chunk covered 1,000 records, and the audit shows 1 missing sequence number (4001). 5,994 sequence numbers overlap. The other 999 are hidden behind s4's range.
- A sequence per session: the audit shows s3 missing 2001 to 3000, 1,000 of 1,000.

Each session adds a position to a consumer's cursor. In the first case there is one position, and in the second there are two.

### 4.7 Cached key invalidation

- Per-record encryption with a push: 200 records before destruction became unreadable, 60 after it were readable under a new key, and the other replica created no key of its own after the push.
- No push, cached key lifetime 10 s: the replica that was not told wrote 40 of 140 later records under the destroyed key, so those are unreadable. Its copy expired at 10 s. The text names invalidation and gives no mechanism across replicas. A push plus a lifetime is what this run supports.
- Chunks encrypted at flush: 300 payloads buffered at destruction were stored under the new key, and 300 were readable after erasure. Purging the buffer on destruction, which the text does not state, turned them into a gap record with reason `erased_before_flush`, and 0 were readable.

### 4.8 The compacted row

50 tenants, 60 batches per tenant-stream-hour, 2,160,015 records. Row size is `pg_column_size`. The per-day figures multiply by 720,000 rows and are extrapolations.

| Layout | Row | Per day | Compaction anchor | Per day | Table after `VACUUM FULL`, 100 rows |
|:--|:--|:--|:--|:--|:--|
| List in the row, anchor names batches | 7,608 B | 5.5 GB | 5,047 B | 3.6 GB | manifest 912 KiB, chain 600 KiB |
| Digest in the row, list in the index, anchor names batches | 392 B | 282 MB | 5,048 B | 3.6 GB | manifest 104 KiB, chain 600 KiB |
| Digest in the row, list in the index, anchor holds a digest | 392 B | 282 MB | 440 B | 317 MB | manifest 104 KiB, chain 72 KiB |

Cost of the index: 4.4 KiB per object. Export took 17.1 s with the list in the row, and 18.9 and 18.3 s with the index. A pass with nothing new took under 0.05 s with the list in the row and 0.1 to 0.2 s with the index, one GET per row. At 720,000 rows that is about 12 to 24 minutes for a no-op pass by one worker on local MinIO (extrapolation). A flipped byte failed its digest in all three layouts.

A compromised compactor dropped one batch per group and wrote a consistent object, index, row, and anchor:

| Interval anchor | Compaction anchor | Verifier | Sequence audit |
|:--|:--|:--|:--|
| Names batches | Names batches | detected: 120 absent, 40 not recomputing | holes, 14,709 |
| Names batches | Digest | detected: 120 absent, 40 not recomputing | holes, 11,794 |
| Digest only | Digest | **passed: 0 absent, 0 not recomputing** | holes, 14,747 |

The interval anchor measured 216 B as a digest and 1,178 to 1,497 B naming batches, at 40 tenants with 5 s of batches. The cheap option is a digest in the compaction anchor with batch names in the interval anchor. I did not write a verifier that rebuilds interval membership from the indexes, which may also close the third row.

## 5. Evidence items never run before

Raw: `raw/i5-evidence.txt`, `raw/i8-reruns.txt`, `raw/i9-highwater.txt`.

### 5.1 Reads through `lenny-evidence`

- Allowlist: the consumer's SAN read; the decision service's SAN got 403; a foreign CA failed at TLS.
- From a named position at sequence 3,711: 3,711 records, first 3,712, last 7,422, and 0 at or below the named sequence.
- A consumer that died at 20,818 records, resumed through alternating replicas while compaction and load ran, and finished after load ended: 84,938 delivered of 84,938, 0 duplicates, and 0 not delivered. Server-side retries were 0, so reading across a removed object was not forced.
- Rates were 12,000 to 48,000 records/s.

The cursor shape matters. A replica frozen for 10 s under load, two runs:

| Cursor | Not delivered | Duplicates |
|:--|:--|:--|
| Highest sequence per position | 6,009 of 216,289, and 6,111 of 215,957 | 0 |
| Delivered ranges per position, gap records on their own ranges | 0, and 0 | 15, and 13 |

The gap record arrives first and moves a high-water mark past records that land later. This is inferred from the counts: the 6,009 and 6,111 undelivered records sit beside 6,024 and 6,124 sequence numbers both stored and in a gap record, and runs with no gap record had 0 undelivered. I did not match the two sets record by record. With rolling graceful restarts there were no gap records and no batch overlap, and both cursors delivered everything (263,865 and 54,969 of the decision stream). My first version of the ranges cursor also merged gap records into delivered ranges and skipped 198 lines, which is why they have their own ranges. With that change the reader rereads parts it could otherwise skip, because the index alone no longer shows the part is done. I did not measure that cost at scale.

### 5.2 Retention, holds, and DeleteByTenant

Retention at 5 s with a 1 h consumer bound:

- Of 150 decision rows, 141 stayed for a consumer that had read part of them, 5 stayed under a hold, and 4 went.
- After the consumer finished, 141 went, and 5 held rows stayed. The consumer had 0 records not delivered.
- A registered consumer that never read: 145 rows stayed under a 1 h bound. Under a 6 s bound all 145 went, with 145 loss rows per consumer.

Chain events: retention that writes no event left the verifier reporting 191 absent batches and 30 anchors not recomputing. With an event naming what was removed, it reported 0 and 0. The text does not state this event.

Holds:

| Operation | Result |
|:--|:--|
| Compactor | t7's 33 rows and digests unchanged, other tenants compacted |
| Retention | t7's decision rows stayed |
| DeleteByTenant on a held tenant | 0 removed, 33 kept, session key kept, payloads readable. Keeping the key is a rule the prototype added. |
| DeleteByTenant on an unheld tenant | removed, key destroyed, chain rows removed |
| Hold released, then DeleteByTenant | 0 rows, 0 objects |
| Hold set during compaction | 31 groups aborted with "source rows changed", every digest verified |
| Hold set during retention | 7 tenants aborted, 163 held rows left, 0 unheld, every digest verified |
| Destroying a held session's key | succeeded, and the held payload became unreadable |

Records arriving after DeleteByTenant recreated the tenant's rows (t8 had 1 row). The text is silent on both of the last two. Escrow and key rewrap were not run.

### 5.3 More than one decision service replica

Two replicas, 2,000/s, 150 s: 1,439,489 records, 0 gaps, compaction and verification clean, 0.37 CPUs in total against about 0.27 for one in section 5.4's baseline. Regulated refusals were 119. One `producer` value with two incarnations appeared in the decision stream.

Envoy sent the access log stream to one replica, giving 134,308 and 703,964 records emitted, so the load split unevenly.

SIGKILL of one replica at t=40 s: 288 503s, of which 104 carried the commit reason. The other 184 came from Envoy with no reason header, so a retry on the reason skips them. Restart added an incarnation and took positions from 10,000 to 17,460, which a consumer's cursor carries until the incarnation closes. A killed incarnation's own high-water marks die with it, so its tail was not measured.

SIGTERM: 2,491 closing records, one per position, and 0 gaps. A killed incarnation wrote none, which gives the signal the design states.

### 5.4 `egress.local`, `git.operation`, and `credential.mint` under load

Beside 2,000 CONNECTs/s for 180 s: `egress.local` at about 1,957/s through the I7 stand-in, `git.operation` at 195/s from the mirror's identity, and `credential.mint` at 39/s from the Token Service's identity.

| Measure | Baseline | With the three streams |
|:--|:--|:--|
| CONNECT p50 small tenants | 0.50 ms | 0.52 ms |
| CONNECT p99 small tenants | 2.04 ms | 3.07 ms |
| CONNECT p99.9 small tenants | 5.1 ms | 14.6 ms |
| Evidence CPU, two replicas | 247m | 327m |
| MinIO CPU | 131m | 217m |
| Decision service CPU | 267m | 305m |

2,121,336 records, 0 gaps. `git.operation` 35,130 of 35,130 and `credential.mint` 7,028 of 7,028 stored.

| Stream | Stored, % of raw | Mean record |
|:--|:--|:--|
| `egress.tunnel` | 4.9 | 724 B |
| `egress.accesslog` | 6.4 | 516 B |
| `egress.decision` | 7.2 | 720 B |
| `egress.local` | 8.0 | 551 B |
| `git.operation` | 27.3 | 520 B |
| `credential.mint` | 32.9 | 525 B |

Generated hashes and few records per batch explain the last two. Production values will differ.

Forged fields on `egress.local` reports were overwritten: 0 rows under the forged tenant, and stored records carried `self_reported` and the binding's tenant. A session the binding does not name, and a stream the report path does not carry, were each refused. No token gave 401. The allowlist checks matched spike C.

### 5.5 The undelivered record of `b2`

The cause is still not established. I tested numbering outside the send order, where a sequence is taken and the record queued afterwards. Five runs of that old code, two with 200 tenants and three with 6 tenants at about 330 decisions/s each and 1 s batches, gave 0 batches overlapping an earlier one and 0 undelivered. `b2` had no replica loss, so the high-water fault in 5.1 does not explain it either. A late access log flush arriving between export and check is possible, and I did not test it.

## 6. The mirror's mode normalization helper

No earlier spike had a helper beyond a `find ... chmod ug-s` line, so `go/cmd/mhelper` implements the text. It runs under the fetch UID after the sweep, walks through directory descriptors, sets the store's modes, sets the store's ACL, removes other extended attributes, and removes symlinks and special files. The scenario runs on ext4 under the capability posture of the design, with `protected_hardlinks=1`, `protected_fifos=1`, and `protected_regular=2`. Raw: `raw/i6-mirror.txt`.

| Plant by a compromised fetch | No helper | With helper |
|:--|:--|:--|
| Setuid, setgid | walk gets `EPERM`, fetch UID keeps ownership | normalized, walk has 0 errors |
| Sticky directory | a later fetch cannot remove a file (`EPERM`) | removal allowed |
| 0777 directory and 0666 file, tenant store | reader refused, since the named ACL entry applies | refused |
| 0777 directory and 0666 file, public store | a groupless reader can write both, and another store's fetch can write the file | refused |
| Planted ACL on a file | reader can write it | refused |
| Default ACL on a directory | reader refused | refused |
| 0000 directory with contents | walk gets `EACCES` | walked, 0 errors |
| Symlink to the Secret, another store, itself | walk does not follow; reader refused | removed |
| FIFO at `packed-refs` | later `ls-remote`, clone, and fetch hang (timeout at 6 to 8 s) | removed, all work |
| Character or block device | `mknod` refused, `CAP_MKNOD` absent | |
| Hard link to the Secret or a public store's file | `ln` refused by `protected_hardlinks` | |
| Hard link to the store's own file | allowed | see below |
| `trusted.*` xattr, file capability, `chattr +i` | refused | |
| `user.*` xattr | allowed | removed |
| File names with spaces, a newline, or `-rf` | no failure | no failure |

Results that change the text:

- **Hard links.** Removing names with a link count above 1 removed `HEAD` in 4 of 4 runs and broke the store. Keeping them worked in 4 of 4 runs. A link within one store crosses no boundary, and `protected_hardlinks` stopped the cross-store cases I tried. A node with that sysctl at 0 was not tested. The helper should not remove them.
- **ACL.** The helper sets the store's ACL and does not leave files without one, because the reader group reads through it. Removal alone was not run.
- **Order.** A helper that ran before the sweep left a replanted 4755 file. Sweep then helper left mode 660. The text states the sweep precedes completion and leaves the helper's order implicit.
- **What a setuid-only helper leaves.** The earlier `find` line left the planted ACL writable, the FIFO hanging every later git, and failed with `Permission denied` on the 0000 directory.
- **Files of other UIDs.** The helper cannot change them and counts them (46 to 47 here), and a compromised fetch cannot chmod them either.
- **Honest work afterwards.** A fetch, `fsck`, and a clone through the reader group passed.
- **Content.** Modes do not cover a planted `config`. `core.fsmonitor`, `core.sshCommand`, `core.hooksPath`, `uploadpack.packObjectsHook`, and a `post-update` hook stayed in the store. None ran in the paths tried: reader `upload-pack`, reader clone, and a later `file://` fetch with `core.hooksPath=/dev/null`. An SSH transport and worktree commands were not tried.
- **Cost.** 100,000 files and 1,000 directories took 319 ms with the ACL set, and 67 to 70 ms when the fetch UID owned none of them. The re-chown took 106 ms.

## 7. POSIX default ACLs on NFS

A kernel-level mount works in this Docker setup. The kernel has `nfs`, `nfs4`, and `nfsd` built in, and lacks `CONFIG_NFS_V3_ACL` and `CONFIG_NFSD_V3_ACL`. A privileged container ran kernel `nfsd`, exporting ext4 with `no_root_squash`, and a privileged client mounted it. Raw: `raw/i7-nfs.txt`.

| Operation | NFSv4.2 and 4.1 | NFSv3 on this kernel |
|:--|:--|:--|
| `setfacl`, or `setxattr` of `system.posix_acl_*`, from the client | `EOPNOTSUPP` | `EOPNOTSUPP` |
| `nfs4_setfacl` with inherit flags from the client | works | fails |
| Server's view of that | a POSIX ACL with default entries | |
| New file and directory by a fetch UID | inherit, reader read and search allowed, write refused | no ACL, reader refused |
| Default ACL set on the server, then used through the mount | inherited, reader read allowed, create refused | inherited, reader read allowed, create refused |
| `getfacl` from the client on an ACL the server holds | shows mode bits only | shows mode bits only |

A second run tested the parent:

- The non-root parent at UID 65532 with ambient `CAP_CHOWN`: `lchown` gave `EPERM` on 3 of 3 entries on v4.2 and v3, while the same walk on local ext4 passed. The server authorizes by the AUTH_SYS UID and does not see the capability. The fetch UID keeps ownership on NFS, so "no fetch UID keeps ownership of files once it exits" fails there, along with the per-fetch UID reuse that depends on it.
- As UID 0 on a `no_root_squash` export, `chown` of a setuid file succeeded and cleared the bit, unlike ext4's `EPERM` for a non-root parent. The same parent setup on ext4 was not retested as root.
- The helper under the fetch UID changed modes and removed the FIFO and symlink on NFS. With the reader ACL it reported errors on every entry (`operation not supported`). On v4.2 it also tried to remove `system.nfs4_acl`, which gave `EINVAL`, so listing and removing attributes needs an exception for that name on NFS.

The design's alert for a volume without ACL support would fire on NFSv4, although that volume can carry the ACL through the NFSv4 interface. Options are `nfs4_setfacl` through the NFSv4 attribute, or setting ACLs on the server. `root_squash` exports and a kernel with `CONFIG_NFS_V3_ACL` were not tried.

## Harness defects

- `raw/invalid/i1-first-pass-opaque-only.txt`: `docker exec -i` consumed the loop's stdin, so only the first kind ran. The live counter patch also missed. Both are fixed.
- The first 8 of 66 Item 1 runs (all opaque at 100/s and two c1-u1) used a `netagent` binary built before level 1 code was added. The code path is unused there. The later binary replaced it mid-series.
- `raw/invalid/i1b-disturbed-by-i3-block.txt` and two later rows: my Item 3 end-to-end block and my Level 1 cleanup (`--filter name=od-na`) removed the hold test's containers. Those runs were discarded and rerun alone. The Item 1 sustained runs otherwise overlapped with Items 2, 3, 6, and 7, which affects CPU more than memory readings.
- `i2.sh`: a restarted Squid container exits at once, which spoiled T15 and T16. `raw/i2-level1.txt` holds the rerun passes. The first two git fixtures used an image with no `http-backend`, and the fourth pass used a working one.
- The first Item 2 probe traffic result was empty because it was read before the run ended. The third pass reads it afterwards.
- Item 3's "NOT SERVED" label on `api.example.test`, described in section 3.
- First-pass S3 and the first rolling-restart read: the prototype's 30 s drain bound fired after the replica had returned, which wrote 44,977 and 1,612 false gap sequence numbers. It was fixed, and `raw/i8-reruns.txt` holds the reruns. A commit refused during a drain had no gap record at first (28 unannounced), also fixed.
- First-pass S5 audits ran before buffers emptied, so their unannounced figures are invalid. Their drop counters are valid. S5 was rerun.
- `dec_start` overwrote the flood test's tenant variable. The flooding tenant was literally named `1` in three cases, which changes nothing, and the "interleaved" case had no interleaving, so it was rerun.
- The first no-tombstone retention check had no interval anchors, so it passed for the wrong reason. It was rerun.
- The first 5d run's generator lacked connection reuse: 32,643 of 194,500 `egress.local` reports failed, and CONNECT p99 reached 54 ms. That latency is discarded. The rerun has 0 failures.
- A stray command waited on stdin and left an empty `/tmp/x` on the host, which I removed.

## What a laptop limits

- Commit refusals, the 150 ms bound, and retry outcomes depend on this virtual disk, where `fdatasync` takes 83 microseconds with stalls. A production disk will differ in a direction this run cannot state.
- Latency, throughput, CPU, and records per second are relative. All containers share 12 CPUs and 8 GB.
- Object store timings are local MinIO, with no network latency. Index-read cost and lookup times will rise on a cloud store.
- Per-day row and anchor sizes are extrapolations from 50 tenants.
- Memory counts apply to this prototype, Go 1.26, and this kernel. They include kernel socket memory at idle only.
- Detection times (kill about 0.1 s through a broken stream, freeze 1.3 to 1.8 s through health checks) depend on my check intervals of 500 ms and 800 ms, and on local networking.
- Docker's bridge network stands in for pod networking. Kubernetes was not involved.

## Not run

- Out of scope: GitHub mint rate, hosted providers' `ls-refs`, corporate gateways, commercial proxies, and EDL consumers.
- T4 KMS and rewrap, escrow, and cloud block volumes.
- The gateway's group mismatch path, which has no code.
- The production `netagent`.
- Hard links with `protected_hardlinks=0`, `root_squash` exports, and NFS with `CONFIG_NFS_V3_ACL`.
- A consumer read forced across a removed object.

## Cleanup

After cleanup, 0 `od-` containers, networks, volumes, and images remain. The two .NET SDK images this run pulled were also removed, and `raw/images-before.txt` and `raw/images-after.txt` list the same images. The containers that existed before this run were left alone. `docker image prune` was not used, because it would have removed a dangling image that predates this run.

The repository's `git status` shows only the `scratchpad/` entry present at the start. `work/` (35 MiB of test exports and cursors) remains in this directory, because a safety check refused its removal. It holds no results that `raw/` lacks and can be deleted.
