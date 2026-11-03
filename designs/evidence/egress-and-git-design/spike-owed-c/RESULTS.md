# Validation owed, spike C (2026-11-02)

Part A and Part B both ran. Part B ran against revision 37 of the design, including `egress.accesslog`, `sequenceEnd`, and `environment`.

Environment: Docker 29.4.3, 12 CPUs, 8 GB, arm64, Envoy 1.40.2. Raw output is under `raw/`, programs under `go/cmd/`, and scripts at the top level.

## Part A: Go `netagent` prototype

`go/cmd/netagent` implements the design's text: a loopback CONNECT endpoint, a per-pod CA with name constraints (permitted DNS hosts and excluded IP ranges), termination for bound hosts, header overwrite with the authority check, the plain HTTP upgrade, sliding-window scrubbing, WebSocket frame scrubbing, opaque tunnels, and server TLS to Envoy. Clients share `netagent`'s network namespace, as in a pod, and have no other route.

### A1. Client matrix (`raw/a1-matrix.txt`, `raw/a1-git.txt`, `raw/a1-behaviour.txt`)

All of these reached the upstream with the real secret, using the placeholder `lenny-injected` or no credential:

curl (HTTP/1.1, HTTP/2, plain upgrade), Python requests, httpx HTTP/1.1, httpx HTTP/2, aiohttp, pip, Node 24 fetch, npm, Go, Java 21 (HTTP/2), `gh`, `glab` (https and `api_protocol: http`), rustls through reqwest (root added, and `rustls-native-certs`), Deno, and Bun.

git, on OpenSSL and on GnuTLS: clone through an opaque tunnel passed, clone and a 64 MiB chunked push through termination passed, and a VCS-covered host was refused with 403 on both the HTTPS and the plain HTTP path.

Behaviour checks that passed:

- A differing `Host` or `:authority` carried no secret to the upstream.
- A differing port carried no secret.
- An absolute-form request with a disagreeing `Host` got 400, as did two `Host` headers.
- A pinned host refused a differing authority and an upgrade.
- An upstream certificate signed by the per-pod CA was refused, and that upstream received 0 requests.
- A leaf from the per-pod CA for a name outside the constraints was refused by curl with "permitted subtree violation".
- A wrong CA or a wrong server name on I2 gave 502.
- The CA kept the same fingerprint across a restart.
- Reflections in plain, gzip, deflate, split, and WebSocket frame bodies were masked, including a reflection split across fragments.
- A truncated upstream body stayed truncated at the client (curl rc 18).

Findings for the design:

1. net/http removes the `Host` header from an absolute-form request, so the disagreement rule needs the raw request head. The prototype parses it itself and carries its own HTTP/1.1 response writer.
2. The text states that it decodes gzip and deflate. Clients advertise more: curl sends `deflate, gzip, br, zstd`. With no `Accept-Encoding` rewrite and no refusal of other encodings, a zstd reflection reached the client (`-lax-encoding`, 1 line with the secret). With the rewrite and a 502 on a forced zstd body, none did. The design states neither rule.
3. "scrubs the exact value" misses a Basic attach template. A reflected rendered header reached the client as `Basic cHlwaXVz...`, which decodes to `pypiuser:PYPI-REAL-33cc03`. Scrubbing the rendered value as well closed it.
4. A request naming another bound host inside a terminated connection must be sent to that host. The prototype does so. The design does not state it.

### A2. Memory and CPU (`raw/a2-memory.txt`, `raw/a2-cpu.txt`)

Idle: 13.5 MiB `memory.current`, of which 5 MiB is anonymous, and 0m CPU over 60 s.

Memory per stream, `memory.current` increase divided by streams:

| Kind | n=100 | n=1,000 | n=5,000 |
| :-- | :-- | :-- | :-- |
| Opaque tunnel | 143 KiB | 127 KiB | 119 KiB |
| Intercepted, client HTTP/1.1, upstream HTTP/1.1 | 176 KiB | 129 KiB | 117 KiB |
| Intercepted, client HTTP/1.1, upstream HTTP/2 | 90 KiB | 74 KiB | 63 KiB |
| Intercepted, client HTTP/2, upstream HTTP/2 | 86 KiB | 52 KiB | 45 KiB |
| Intercepted, client HTTP/2, upstream HTTP/1.1 | | 104 KiB | |
| Intercepted, gzip decoded, client HTTP/1.1, upstream HTTP/1.1 | | 183 KiB | |
| Intercepted, gzip decoded, client HTTP/2, upstream HTTP/2 | | 97 KiB | |

Throughput for one download, in MB/s. Each cell is the range of three runs, except the HTTP/1.1 upstream row, which has two.

| Kind | 50m | 250m | 1 CPU | No limit | CPU-s/GiB, no limit |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Opaque | 25 to 30 | 270 to 323 | 2,048 to 2,213 | 2,172 to 2,320 | 0.46 to 0.56 |
| Intercepted, client HTTP/1.1, upstream HTTP/2 | 6.6 to 8.7 | 62 to 68 | 652 to 678 | 777 to 785 | 1.78 |
| Intercepted, client HTTP/2, upstream HTTP/2 | 5.0 to 6.2 | 46 to 52 | 534 to 568 | 598 to 604 | 2.72 |
| Intercepted, client HTTP/1.1, upstream HTTP/1.1 | 11 to 12 | 112 to 126 | 854 | 912 | 1.19 |

Scrubbing added about 0.15 CPU-s/GiB (1.62 off, 1.78 on), and the choice of window and secret length made little difference. A 4 KiB opaque copy buffer gave 170 MB/s at 250m against about 300 MB/s with `io.Copy`'s 32 KiB buffer, and 0.85 CPU-s/GiB against about 0.5. Its memory per tunnel was not measured.

New connections per second under a CPU limit (`raw/a2-cpu.txt`):

| Limit | Intercepted, 5/s | Intercepted, 20/s | Intercepted, 100/s | Opaque, 20/s | Opaque, 100/s |
| :-- | :-- | :-- | :-- | :-- | :-- |
| 250m | p50 11.5 ms | p50 10.4 ms | p50 3.5 ms | p50 8.8 ms | p50 3.3 ms |
| 50m | p50 12 ms | p50 68 s, 40 of 300 failed | p50 164 s, 1,102 of 1,500 failed | p50 0.98 s | p50 20.5 s, 818 of 1,500 failed |

The CPU column in the 50m overload rows of the raw file divides by 15 s although those runs lasted minutes, so those rows' millicore figures are wrong. Latency and failure counts are measured from the client and are unaffected.

CPU with no limit:

- Five LLM streams at 20 events/s used 21m in total.
- 50 new connections/s used 135m, with a 23.4 MiB peak.
- 100 new connections/s used 191m intercepted and 124m opaque.

Slow consumers, 20 stalled responses with no limit:

| Case | Total | Kernel socket memory |
| :-- | :-- | :-- |
| Upstream HTTP/2, defaults | 177 MiB | 78.5 MiB |
| Upstream HTTP/2, 256 KiB stream buffer | 74 MiB | 52 MiB |
| Upstream HTTP/1.1 | 162 MiB | 140 MiB |
| Opaque | 142 MiB | 124 MiB |

Under a hard 64 MiB limit, the default HTTP/2 and HTTP/1.1 upstream cases were OOM-killed with 8 and 10 consumers reported up, and bounded socket buffers survived 25 s:

- Upstream HTTP/2, 256 KiB stream buffer, no socket bound: survived, 15 up, 63.7 MiB.
- Upstream HTTP/2, 256 KiB stream buffer and 256 KiB socket buffers: survived, 9 up, 23.8 MiB.
- Upstream HTTP/1.1, 256 KiB socket buffers: survived, 20 up, 35.6 MiB.

Hard limit tunnel count: an intercepted HTTP/1.1 run was killed by the OOM killer with its last good reading at 455 active streams. The other rows of that run lost their last reading to a harness defect that overwrote it when the final poll failed. The rerun meant to fix this was stopped by the handback with no row written, so `raw/a2-hardlimit.txt` is empty. The per-stream table implies about 400 tunnels above idle in 64 MiB, which is an estimate rather than a measurement.

Assessment of the example request of 50m and 64Mi: it is realistic as a request for an ordinary session. Idle, a few streams, and light request rates fit: 21m for five LLM streams and a 14 to 23 MiB footprint. It does not cover a package install at 50 new connections/s, which used 135m, and a stalled consumer or about 400 tunnels exceeds 64 MiB. A hard limit at those values fails: at 50m, 20 new intercepted connections/s collapsed to a 68 s p50 with failures. This supports the 250m floor the design sets.

### A3. Streams (`raw/a3-streams.txt`)

This statement is wrong: "scrubbed with a sliding window that holds back one byte less than the secret's length, so a secret split across chunks is still caught and events still arrive as they are sent."

Holding a fixed `len-1` bytes withholds each event's terminating blank line until the next bytes arrive.

| Case | Fixed window lag | Prefix window lag |
| :-- | :-- | :-- |
| Events 500 ms apart | 503 ms | 1.1 ms |
| 10 s pause before the last event | 10.2 s on the preceding event | 2.7 ms |
| 99-byte secret, token-sized events 50 ms apart | 106 ms | 1.2 ms |
| Opaque baseline, events 500 ms apart | | 0.7 ms |

The prefix window holds only a suffix that is a proper prefix of the secret. It masked every reflection, including reflections split into 5-byte and 7-byte chunks, and showed 0 leaks. gzip with a flush per event streamed at 1.4 ms. Round 3's mitmproxy figures show the same delay: its first event arrived at +0.42 s with events 400 ms apart.

Same-length masking keeps a known `Content-Length` correct. This was an implementation choice in the prototype.

CPU limits, stream lag at p99 with four other streams or one bulk download beside it:

| Limit | Four other streams | One bulk download |
| :-- | :-- | :-- |
| 50m | 6.4 ms | 198 ms |
| 250m | 6 ms | 77 ms |
| 1 CPU | 7.3 ms | 10 ms |

A five-minute stream stayed flat at 16 MiB and 9 goroutines, with lag of 1.1 ms at event 5,800. Its final summary line was not captured, because the script removed the container before the stream ended.

## Part B: `lenny-evidence` as a separate service

Programs: `go/cmd/evidence`, `decsvc`, `evload`, `evtool`, and `fsprobe`. Scripts: `b-lib.sh`, `b0.sh` to `b11.sh`. Raw output: `raw/b*.txt`.

Setup: two evidence replicas, one decision stand-in, Envoy 1.40.2 at 2 CPUs, MinIO at 4 CPUs, Postgres 17 at 4 CPUs, and PgBouncer 1.23 in transaction mode with a pool of 25 (§17.8 tier 1). There are 2,500 tenants, of which 5 busy tenants share half the rate. 5% of CONNECTs are denied, tunnels hold for 300 ms, and authorities are varied and long-tailed. Mean records are 716 to 720 bytes (512 for `egress.accesslog`). Stored size was 6.8% to 9.5% of raw, against 17% to 31% in 5-second batches.

Port exhaustion was checked: each container set `ip_local_port_range` and `tcp_tw_reuse`, the client held about 31,000 sockets in TIME-WAIT with evidence off and on alike, and no run had a dial error.

### Harness defects found and corrected

- `raw/invalid/`: the first b1 and b2 pass reset the store while the previous replicas were still flushing, which left 838 rows pointing at deleted objects. The reset now stops replicas first.
- The first prefilter scans in `raw/b7-scan.txt` searched for a trailing comma the field lacks and found 0 hits. `raw/b9-scan-fixed.txt` replaces them. The unfiltered runs in b7 are valid.
- b5's SIGTERM case kept accepting records, so it never emptied and was killed at 30 s. b8 reruns it with intake stopped first. The writer's closing row is still unset in that rerun, because the handler's wait also counts rows the write-ahead drain adds. Records did not depend on it.
- b8's 1,462 undelivered export records were late batches that landed after export read the manifest. b11 waits for buffers to empty and gives 0.
- b2 reported 1 record undelivered of 3,167,692. Its cause was not established, and b11's clean run did not reproduce it.
- `announced` in the gap audit undercounts, because it requires one gap record to span a whole gap. The dropped-record counters are the reliable figures.

### mTLS and the allowlist (`raw/b0-mtls.txt`)

Allowed: the decision service, the token service, and the mirror for a routed tenant, each on its own streams. A forged `producer` was overwritten with the SAN. Refused: the token service on another stream, the mirror for an unrouted tenant, and a same-CA leaf outside the allowlist (each 403), and a foreign CA and no certificate (at TLS).

### CONNECT latency and ingest cost (`raw/b1-latency.txt`), 2,000 CONNECTs/s

| Run | p50 | p99 | p99.9 |
| :-- | :-- | :-- | :-- |
| Evidence off, no access log | 0.54 ms | 1.4 ms | 27 to 40 ms |
| On, decision service's access log only | 0.58 ms | 2.0 ms | 5 to 6 ms |
| On, with `egress.accesslog` direct from Envoy | 0.59 ms | 3.2 ms | 40 ms |

About 9,700 records/s cost 120m to 128m per replica, 276m in the decision service, 206m in MinIO, and 13m in Postgres. Replica memory reached 617 to 641 MiB with about 100 MiB buffered. Time to durable for small tenants was 60 s at p50 and 64 s at p99.

Gaps: 0 across 3.17 million records, against the producer's own high-water marks.

Envoy's stream cannot hash per tenant. 343,252 of 626,462 records on one replica were for tenants it does not own, so `egress.accesslog` wrote 23,707 rows against about 12,500 for each other stream. This contradicts "Tenants hash to ingest replicas, so one writer serves a tenant."

### "no more than 600,000 rows per day for 5,000 tenants"

After compaction the rule holds per row: exactly one row per tenant, stream, and hour (7,500 for 2,500 tenants and 3 streams, and 5,000 for the busy hour). Three parts of the statement need changing:

- The stream table now lists six streams, which gives 720,000 at 5,000 tenants. The text still says "across the five streams".
- Before compaction the manifest took 10,000 rows per minute, or 600,000 per hour, at 2,500 tenants on three streams (assuming the measured steady window continues). The daily figure describes rows retained after compaction and leaves out inserts.
- A compacted row with 60 parts is 7,401 bytes (19.6 KB of JSON before compression), so 600,000 rows would be about 4.4 GB per day, and rows accumulate over retention (extrapolation).

Objects follow rows before compaction: 300,000 for the busy hour on two streams.

### The 150 ms commit bound (`raw/b3-regulated.txt`, `raw/b8-diagnostics.txt`, `raw/b10-fsync.txt`)

| Case | Commits | p50 | Median window p99 | Refused |
| :-- | :-- | :-- | :-- | :-- |
| Pooler, 500/s | 89,864 | 0.6 ms | 4.8 ms | 19 (0.021%) |
| Pooler, 2,000/s, 390 s | 778,402 | 0.5 ms | 41 ms | 3,013 (0.387%) |
| Direct to Postgres, 2,000/s | 239,548 | 0.4 ms | 10.7 ms | 1,059 (0.442%) |
| Pooler, one transaction per tenant, 2,000/s | 179,677 | 0.4 ms | 11 ms | 122 (0.068%) |
| Pooler, one transaction per tenant, 500/s | 44,933 | 0.6 ms | 2.9 ms | 0 |
| Drain off, 2,000/s | 359,924 | 0.5 ms | p99 130 ms overall | 2,756 (0.766%) |
| Evidence with 4 CPUs, 2,000/s | 359,907 | 0.5 ms | p99 106 ms overall | 2,275 (0.632%) |
| Emulated 3 ms WAL flush, 2,000/s | 359,894 | 7.2 ms | p99 45 ms overall | 251 (0.070%) |
| Emulated 3 ms WAL flush, 500/s | 59,984 | 7.0 ms | p99 21 ms overall | 195 (0.325%) |

The bound does not hold strictly at either rate. At 2,000/s refusals come in bursts of hundreds to about 1,700, with a worst 30-second window p99 at the 150 ms cap. The refusal is fail-closed and works as stated.

Cause: the pooler is excluded, because direct connections behaved the same. The drain and ingest CPU are excluded by their own runs. The probe found no `fdatasync` above 50 ms idle (maximum 16 ms) and stalls up to 124 ms beside the load, and wait events at refusal times were `WalSync` and `WALWrite`. The bursts therefore track this machine's virtual disk under load. A production disk will differ, in a direction this run cannot state. Group sizes stayed near 1 on this fast disk (`fdatasync` p50 74 to 394 microseconds) and reached 4 to 5 with the 3 ms stand-in.

The round 9 statement "no failures at 2,000 per second" did not reproduce. Rows through the pooler with the tenant guard trigger and RLS in place: the writer used the `__all__` sentinel for a group, which §12.3 reserves for platform-admin paths. One transaction per tenant is the alternative, and on this disk it cost no more.

Allowed against stored: pooler paused 5 s, 42,504 allowed and 43,982 stored. Replica killed, 37,122 and 37,122.

### Noisy tenant, P7 (`raw/b4-noisy.txt`)

Quiet tenants had 0 gaps in every case, and their p99 CONNECT latency stayed between 1.9 and 3.7 ms.

| Case, beside 500/s | Noisy tenant's result |
| :-- | :-- |
| 3,000 denied/s, one host | 359,925 aggregated into 26 records, 0 dropped |
| 3,000 denied/s, a new host each | Aggregation defeated: 282,281 dropped at the tenant bound, 402 gap records |
| 100,000 records/s, new hosts | 1,798,349 dropped by the producer, 1,749,725 by ingest |
| 100,000 records/s, one host | 1,850,042 dropped by the producer, 3,072,088 aggregated |

Aggregation has two costs the text leaves out. It needs per-record sequence ranges, or a loss between denials becomes invisible, and all but the first decision identifier per host in a batch are gone, which breaks a 403 lookup during a flood.

### Replica killed (`raw/b5-kill.txt`, `raw/b8-diagnostics.txt`)

SIGKILL at t=120 with load continuing, 117 MiB buffered:

- 101,861 sequence numbers were missing in 2,504 gaps a consumer sees. That count includes 4,438 refused regulated decisions and 234 producer drops, because a refused decision consumes a sequence number here.
- 826 records in 43 tails were invisible to a consumer. They are visible only against the producer's marks.
- With the load ending 5 s after the kill, tails rose to 2,425 records in 294 positions, and 3 positions had nothing stored (158 records).
- `egress.accesslog` lost about 80,000 records, by subtraction. It is sequenced at ingest, so it shows no gap at all.

This statement is wrong: "A crash loses what was buffered, which the incarnation rule makes detectable". The producer's incarnation does not change when a replica dies. A writer incarnation or a producer high-water record is needed.

Graceful stop with intake closed: missing 22,766, which matches 15,749 producer drops during the 34 s outage plus 7,020 refused decisions (22,769). The 8 MiB producer bound overflowed for busy tenants.

### Compaction and cursors (`raw/b2-compaction.txt`, `raw/b7-scan.txt`, `raw/b11-cursor.txt`)

The busy hour merged 300,000 batches into 5,000 rows in 65 s at 8 workers, using 56 CPU-seconds. Every part digest, record count, chain link, and anchor verified afterwards. A flipped byte failed its digest. A compactor that dropped a batch and rewrote the object and row failed its anchors. Held rows stayed untouched, and a late batch merged back to one row.

Export across compaction: 1,919,780 delivered of 1,919,780, 0 duplicates, and 14,334 parts skipped by the index alone. The cursor holds 10,000 positions in 738 KB at 2,500 tenants, and each producer incarnation adds positions.

### Scan (`raw/b9-scan-fixed.txt`)

"which suggests minutes for a busy hour with no index" is too high on this store. The tunnel stream at 2,000 CONNECTs/s for an hour is 14.4 million records, 9,959 MiB raw and 698 MiB compressed. Each run found the planted record, and port alone returned 233 hits, which confirms that all the filters matter.

| Objects | 1 worker | 4 workers | 8 workers | 4 workers, full decode |
| :-- | :-- | :-- | :-- | :-- |
| Compacted, 2,500 | 17.1 s | 5.7 s | 5.3 s | 21.9 s |
| Uncompacted, 150,000 | 79.0 s | 25.8 s | 18.5 s | 42.7 s |

This is local MinIO. A cloud object store's GET latency was not measured, and it weighs most on the 150,000-object case.

### Erasure and `sequenceEnd` (`raw/b6-erasure.txt`)

Destroying a session key made that session's 2,500 records unreadable, left other sessions readable, and left every digest and anchor verifying before and after compaction. After destruction, ingest kept encrypting new records under its cached copy of the key (100 records). The design needs that cache invalidated.

Per-record encryption stored about 226 bytes per payload and chunks about 40 bytes, which supports `sequenceEnd`. Two sessions interleaved in one tenant's sequence gave overlapping ranges (s3 1 to 1999, s4 2 to 2000), so one lost chunk is hidden by the other's range. A single range is enough only for a non-interleaved sequence.

### Not run

- Reads through `lenny-evidence` from a named position: the tool reads the store directly.
- Retention honouring a consumer's position, `DeleteByTenant` honouring holds, and escrow.
- `egress.local`, `git.operation`, and `credential.mint` under load: probed only.
- T4 KMS, a cloud object store, a networked disk, and more than one decision service replica.

## Cleanup

All `oc-` containers, networks, volumes, and images were removed: 0 of each remain. The repository was not edited.
