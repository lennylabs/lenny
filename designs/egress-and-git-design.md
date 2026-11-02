# Egress and git access for agent pods

Status: design for owner review, revision 18, 2026-11-02. The owner adopted the recommended decisions except where "Decisions" says otherwise, and reversed the deferral of level 1 replacement. Nothing in this document is built or staged. It replaces the "Git and egress design under owner review" handoff note in `gateway-runtime-comms-remediation.md` §10.2 as the current statement of the design, and it is the input to the change proposals listed under "Proposal split".

Six validation rounds checked the design on 2026-11-01 and 2026-11-02, and thirteen rounds of adversarial review plus a fact-check examined revisions 4 to 17 (see "Review record"). The first verified claims against vendor documentation, source code, the specification, and the Lenny tree. The second and third ran hands-on tests in throwaway containers and a throwaway kind cluster: Envoy 1.40.2 and 1.39.6, Squid 7.7, a Go proxy built on Smokescreen, git 2.49 with git-lfs 3.6, Kubernetes 1.35 on kind, and the client tools Maven, Gradle, `gh`, `glab`, npm, and pip. Each round also included research. The section "Validation record" lists what each round established, and "Validation still owed" lists what remains.

## Requirements

The owner set the following direction:

- Pods may have network access to named destinations. A pod needs some network path to clone a repository.
- Egress filtering applies to all HTTP traffic from pods that enable it, and git is one case of it.
- Runtimes can `git push`.
- Pools can have egress during warm-up, including cloning repositories.
- Lenny ships working defaults. A deployer can replace each default, or augment it with deployer-specific services.
- Different tenants can have different configurations, including different external proxies and git services. Tenant admins get as much flexibility as the platform's safety bounds allow.
- Pull requests and other host operations use the host's standard HTTP APIs through the egress route. Lenny adds no platform tool for them.
- The policy decision service runs separately from the gateway.
- Lenny does not adopt Goblet, which is archived (last commit 2021-07-14).

## Security properties

The design holds these properties. Each later section states which component enforces them.

- P1. A pod reaches only the destinations its effective policy names.
- P2. Long-lived credentials never enter a pod, with four bounded exceptions: `netagent` memory holds a `stored` provider's read token for a fetch and, under the `storedTokenPush` opt-in, for a push (see "Credentials"); `netagent` memory holds a stored API secret for an `inject` binding, which never names a git host a VCS pool covers; the agent-readable deliveries place a credential in the agent's reach under their opt-in (see "Credential delivery to runtimes"); and under level 1 replacement, `netagent` memory holds the identity's rotating proxy credential (see "Level 1 replacement"). Each opt-in sits behind its own gate (see "Credential delivery to runtimes", **Gates**).
- P3. Egress and VCS credentials are never readable by the agent UID. The exceptions are the credential binding deliveries that place a credential in the agent's reach by design: `file`, `federation`, and `metadata` (see "Credential delivery to runtimes"). Each needs its class's gate and a tenant opt-in (see "Credential delivery to runtimes", **Gates**). LLM credentials keep their existing delivery rules in §4.7.
- P4. One tenant's data and credentials never reach another tenant's pods.
- P5. Every allowed or denied egress connection is attributable to a pod, a pool, and a tenant once the pod is bound to one, and to a session where the pod model allows it.
- P6. Failure of any egress component blocks traffic.
- P7. A pod's load cannot remove another tenant's egress. Quotas per pod and per tenant bound tunnels, connection rate, and decision calls before any shared limit is reached.

These statements describe the default proxy and level 2 replacement. A deployment that selects level 1 replacement holds P1, P5, and P7 in a weaker form, which "Level 1 replacement" states.

## Architecture

```
  agent pod (one network namespace; NetworkPolicy applies to the whole pod)
 +------------------------------------------------------------------------------+
 | runtime container (agent UID)   adapter container      netagent container    |
 |  HTTPS_PROXY=127.0.0.1:3128 ------------------------> local egress endpoint -+--> lenny-egress (central proxy)
 |  git remotes rewritten      ------------------------> local git endpoint   --+--> git endpoint (lenny-git-mirror,
 |                                                         | projected SA token  |      deployer service, or upstream
 |                                                         | (audience lenny-egress)     through lenny-egress)
 +---------------------------------------------------------+--------------------+
                                                           | binding (I7)        | credentials (I8)
                                                           v                     v
                                                   lenny-egress-authz <--scope-- Token Service (minting)
 lenny-egress --(ext_authz, per CONNECT)--> lenny-egress-authz
 lenny-egress --> internet, or --> upstream proxy chosen per tenant (forwarded CONNECT)
```

The design separates three responsibilities.

- **Network enforcement** happens in `lenny-egress` and the git endpoint. The pod's NetworkPolicy allows only those services, `lenny-egress-authz` (I7), the Token Service (I8), the `proxied` resolver, and the existing base destinations, each on its port. The runtime can reach those services directly, so the operative control is that every one of them acts only on a request that carries the pod's service account token, which only `netagent` holds. The NetworkPolicy bounds what the pod can address, and the token bounds what it can obtain.
- **Decisions** happen in `lenny-egress-authz`. It identifies the pod from its service account token and looks up the pod's current binding (pool, tenant, and sessions) from the records the controller and gateway already write.
- **Identity, credential use, and routing inside the pod** happen in `netagent`. This is a new Lenny container in every pod that uses the local endpoints, running under its own UID. It holds the pod's identity token and any upstream token, and it attaches them on the hops that leave the pod. It also enforces which repositories a credential may be used for, because it is the only component that sees both the plain git request and the credential.

### The `netagent` container

The local egress endpoint and the local git endpoint run in a dedicated container rather than in the existing adapter.

|                       | In the existing adapter                                                                                                                                                                                                                    | In a separate `netagent` container                                                                                                                                                                                                                                                                        |
| :-------------------- | :----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | :-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Embedded runtimes     | The embedded runtime is the adapter, running at the agent UID. Every embedded runtime links the endpoints through `pkg/adapter`, must accept their rendered flags, and holds the egress identity and tokens at the agent UID, so P3 fails. | Embedded and sidecar pods get the same container. Embedded runtimes need no code and no new flags, and P3 holds in both models.                                                                                                                                                                           |
| Identity before claim | The adapter already mounts a projected service account token with the deployment's gateway audience (`podspec/sa_token.go`, 900 s), so an identity exists before claim in both designs. In the embedded model that token sits in the agent's container, so the agent could present the egress identity itself. | The container mounts its own projected tokens at pod creation, with audiences the agent's container never holds. The identity exists from the first second of the pod's life, independent of claim timing and pod model, and it is distinct from the adapter's. |
| New channels          | A lease handoff inside the adapter, plus a new delivery RPC timed before setup.                                                                                                                                                            | No intra-pod channel. `netagent` talks only to `lenny-egress-authz`, the Token Service, `lenny-egress`, and the git endpoint.                                                                                                                                                                             |
| Failure isolation     | A crash in proxy code takes down the adapter and the session.                                                                                                                                                                              | A crash restarts `netagent` only. Egress fails closed while it restarts.                                                                                                                                                                                                                                  |
| Cost                  | No extra container.                                                                                                                                                                                                                        | One extra container per pod, including warm pods. It is a small Go process (the forwarding prototype peaked at 11 MB RSS while streaming 100 MB clones). `applyResources` (`pkg/controller/sandbox/podspec/resources.go`) copies the pool's resource class onto every container, so the builder appends `netagent` after it runs, as it already does for the test-only egress-capture sidecar, and sets an explicit request from a flag. The egress-capture sidecar sets none and falls back to the namespace LimitRange. |
| Specification         | No new container rules.                                                                                                                                                                                                                    | A third reserved UID and a §13.1 container-identity row. The container does not mount `/run/lenny`, so the `lenny-cred-readers` boundary is unchanged.                                                                                                                                                    |

Recommendation: the separate container. The decisive point is that P3 holds for embedded runtimes: the egress identity and every upstream token stay in a container the agent UID cannot read.

**Identity.** The pod spec mounts a projected volume with two service account tokens into `netagent` only: audience `lenny-egress-<cluster-name>`, presented on I2, I3, I5, and I7, and audience `lenny-token-service-<cluster-name>`, which `netagent` presents to the Token Service directly on I8 (see "Egress decision service", **Credential requests**). The runtime and adapter containers do not mount either. The adapter keeps its existing gateway-audience token, and every validator rejects a token whose audience is not its own, so the gateway-audience token the embedded runtime holds at the agent UID buys nothing on the egress hops. The audiences carry the cluster name because §10.3 requires deployment-specific audiences, so a token from another Lenny deployment on the same cluster fails. Kubernetes binds each token to the pod (the JWT carries the pod name and UID) and rotates it; the design sets `expirationSeconds` to 600. `lenny-egress-authz` verifies the signature offline through the cluster's OIDC discovery keys, checks the pod UID against its watch of live pods, and requires the connection's peer address to be one of the pod's addresses (`status.podIPs`, which covers dual-stack) from the same watch, so a copied token is useless from any other source. The check assumes the pod-to-Service path is not source-translated; preflight verifies that kube-proxy does not masquerade pod sources (`masqueradeAll` and IPVS configurations), and the check is disabled with a warning where it would reject every pod. The mirror and the Token Service apply the same peer check. R14 (agent-pod mTLS) later adds a certificate on the same hops without changing the model.

Round 3 validated this on kind (Kubernetes 1.35):

- The runtime and adapter containers have no path to the token. §13.1 forbids `shareProcessNamespace` on every Lenny pod and the `lenny-pod-security` webhook rejects it, so the spike's shared-namespace run was a control for a misconfigured cluster: even then, `/proc/<pid>/root`, `environ`, and `fd` of `netagent` return permission denied. The protection rests on the separate mount and the distinct UID: a container at `netagent`'s UID read the token. `netagent`'s command line and mount table stay visible, so `netagent` takes no secret as an argument.
- A stdlib Go validator verified signature, issuer, audience, expiry, and the pod, node, and service account claims, and rejected a wrong audience, a token without a pod binding, a tampered payload, and an expired token. The default `system:service-account-issuer-discovery` binding was enough.
- After the pod was deleted, the validator rejected its unexpired token 55 to 72 ms after the watch delivered the deletion. The API server's TokenReview kept accepting the token for about 6 seconds after the pod was gone, so the offline check is faster than TokenReview.
- The kubelet rewrote the token at 80 to 88 percent of its 600-second lifetime with an atomic swap, and the lifetime was not extended.
- Anyone allowed to `create serviceaccounts/token` on the agent service account can mint a pod-bound token that the validator accepts, and anyone with `pods/ephemeralcontainers` or node access can read the mounted one. The chart grants none of these to a Lenny component, preflight gains a check that no role bound in the agent namespaces holds them, and the peer-address check above limits what a copied token can do.
- The protection is the mount boundary, not file modes. The pod's `fsGroup` (the `lenny-cred-readers` GID, `base_pod.go`) makes every volume group-readable, and the agent UID is in that group, so any volume mounted into both containers is readable by the agent. `netagent`'s token volume and its private emptyDir (CA key and state) are mounted into `netagent` only, and the `lenny-pod-security` and `lenny-ephemeral-container-cred-guard` webhooks extend their `/run/lenny` rules to those two volumes, so no other container or ephemeral container can mount them. Inside its private emptyDir, `netagent` creates its directories with mode 0700 and a 077 umask.

`netagent` runs as a native sidecar (an init container with `restartPolicy: Always`), so it starts before the runtime and stops after it. Round 3 confirmed that ordering, and that a `netagent` restart leaves the runtime and adapter running. Its startup probe calls a readiness endpoint rather than testing for a file, because after a restart a file check passed on the previous instance's file.

**Binding.** `lenny-egress-authz` resolves the pod UID to its Sandbox and pool, reads the tenant pin (`lenny.dev/tenant-id`) and any tenant ownership of the pool, and reads the pod's current session assignments from the session store. The feed from the gateway is explicit:

- The session store is the source of truth, read through the §12 transaction-mode pooler. Postgres `LISTEN`/`NOTIFY` is unavailable through that pooler (§12.6), and the pod registry polls for the same reason, so the design does not use it.
- The gateway pushes every binding change (bind, unbind, reclaim, and recycle) to every `lenny-egress-authz` replica, over a gRPC call on the gateway's mTLS identity to each endpoint of a headless Service, after the store write commits. Envoy spreads a pod's decisions across replicas and `netagent` subscribes to one of them, so a push to one replica would leave the others serving the old binding until their re-read. The push is a hint: a replica never updates its cache from the payload. It re-reads the session row and acts on what the store says, so a forged or replayed push changes nothing, and only the gateway identity is accepted on that call. The push never blocks the commit; the gateway retries each endpoint for a bounded time and then relies on the re-read.
- A decision for a pod whose binding is not in the cache reads through to the store before it answers, and every cached binding is re-read at a bounded age (30 seconds by default) whether or not a push arrived. A lost push therefore costs at most that bound, and the normal bound between a bind commit and the decision service seeing it is one gRPC round trip.
- Each binding carries the §7.3 coordination generation, and the decision service keeps the highest generation it has seen per session. A pod presenting a lower generation is refused, which closes replay of an old binding.
- A pod's session-scoped requests are honoured only when the session's current binding names that pod. §7.3 publishes no binding for a replacement pod until the `resuming → running` commit, yet `FinalizeWorkspace`, which now runs the clone in the pod, precedes that commit. The resume driver therefore writes a provisional binding (pod, session, and generation, with state `materializing`) before `FinalizeWorkspace`, and the commit promotes it. The provisional state grants read tokens for the session's `gitClone` sources and nothing else; write tokens, `inject`, `file`, `federation`, and `metadata` become available at the `running` commit. The binding lives outside the `pod_assignment` field, its write is fenced on the incarnation's `coordination_generation` in the step-2 transaction where the pod is already known, and every exit from `resuming` (the failure transition, the watchdog, and a mid-resume terminal) retires it in the same write that releases the pod, so a released pinned-idle pod carries no session grant. The §4.6.1 orphan-claim predicate keeps reading `pod_assignment`. That is a §7.3 change staged in proposal 2, and a tier 4 test resumes a session with a `gitClone` source.
- `netagent` receives the same changes through its I7 subscription to one replica, which is served from that replica's cache after the re-read. When the replica goes away (loss or rollout), `netagent` resubscribes to any replica, sends the last generation it saw, and receives the current binding from that replica's read-through; a resubscription whose generation matches the replica's cache is answered from cache, and resubscription has its own low rate limit (one per 10 seconds per pod), so a pod cannot drive store reads through it. Open tunnels stay open for a 30-second grace during resubscription and then close (P6). When a session ends or the binding changes, `netagent` closes that session's tunnels at once. On a concurrent-session pod it cannot attribute tunnels to a session, so tunnels there run to the pool's cap, which is a stated residual.

`lenny-egress-authz`, the Token Service, and `lenny-git-mirror` therefore read the session store through the pooler, with §13.2.4 rows. The Token Service reads only its own token and audit stores today (`cmd/lenny-token-service/stores.go`), so the session, pool, and binding reads are new for it. None of them holds a direct Postgres connection.

### Local egress endpoint (`netagent`)

`netagent` listens on `127.0.0.1:3128` as an HTTP CONNECT proxy that requires no client authentication. Every tool in the pod reaches the internet through it.

- The proxy variables are fixed values that are identical for every pod, so they are set at pod creation. In the sidecar model the supervisor sets them on the runtime process. In the embedded model they are container env.
- For each CONNECT, `netagent` opens a CONNECT to `lenny-egress` carrying `Proxy-Authorization: Basic` with the service account token as the password. 
- `netagent` does not resolve the target name and does not open TLS. It forwards the CONNECT authority unchanged, and nothing else from the client: it sends `Host` and its own `Proxy-Authorization`, and it drops every other header the agent sent, so an agent cannot inject a route header or an identity header. The decision overwrites the route header on every CONNECT regardless.
- `netagent` binds its loopback ports before the runtime starts and holds them. If a restart loses a port to a process the agent started, `netagent` fails its readiness probe, egress stays closed (P6), and the pod is reported unhealthy and retired. The adapter's own git traffic (the materialization clone) does not use the loopback port: it uses a Unix socket in a directory mounted into the adapter and `netagent` only, so in the sidecar model an agent process cannot stand in for `netagent` toward the adapter. In the embedded model the adapter is the agent's container, so the socket grants nothing the loopback port does not; the volume counts as shared for the webhook rules.
- Before any binding exists, the pod's effective policy is the pool's warm-up policy. Warm-up egress therefore works for pod-warm, SDK-warm, and service-mode pods alike.

### Central egress proxy (`lenny-egress`)

`lenny-egress` is a Kubernetes Deployment, a replicated set of identical stateless pods, in the `lenny-system` namespace where the gateway and Token Service already run. It runs Envoy 1.39.6 or later (tested on 1.40.2 and 1.39.6), configured by a static bootstrap that the Helm chart renders. Lenny writes no proxy code.

The second spike verified each of these behaviours on both versions:

- It terminates HTTP/1.1 CONNECT and reaches the target through the dynamic forward proxy.
- It sends an ext_authz gRPC check to `lenny-egress-authz` for every CONNECT. The check carries the method, the authority (host and port), all request headers including `Proxy-Authorization`, and the peer address. A deny returns 403. A 407 with `Proxy-Authenticate: Basic` is available for clients that need a challenge. If `lenny-egress-authz` is unreachable, Envoy returns 503 and opens no tunnel (P6).
- It writes the decision metadata that ext_authz returns (tenant, pool, session, and rule) into a JSON access log, for allows and for denies. Long tunnels log at establishment and at an interval, as well as at close.
- `DnsCacheConfig.resolved_address_filter` refuses targets that resolve to private, loopback, link-local, metadata, or cluster ranges. It also covers IP literals, because literals pass through the same resolution step. The range list must include `::ffff:0:0/96` (IPv4-mapped) and `64:ff9b::/96` (NAT64). Without them, `[::ffff:127.0.0.1]` passes the filter. The list also carries the Azure wireserver `168.63.129.16/32`, `0.0.0.0/8`, `100.64.0.0/10`, `198.18.0.0/15`, multicast, and `::/128`, and refuses IPv6 zone identifiers. The list has no exemptions: a private destination a tenant is allowed to reach never passes through resolution (see "Egress decision service", **Private destinations**). The ext_authz filter runs before the dynamic forward proxy filter, so a refused authority is never resolved, which closes a DNS side channel for denied names. A conformance test pins the list and the filter order.
- `max_stream_duration` ends active, idle, and client-only CONNECT tunnels at the configured time (5.0 seconds in the test, with `delayed_close_timeout` lowered). `max_connection_duration` does not end tunnels, and the design does not use it.
- **Tunnel lifetime.** `max_stream_duration` is route configuration in the static bootstrap, so the chart renders a fixed menu of routes (15 minutes, 1 hour, 2 hours, and 8 hours by default, as Helm values) and a pool names one of them. The decision selects that route, so the cap is a pool setting without xDS: an LLM pool can carry 2 hours while a coding pool carries 15 minutes. A pod whose `netagent` is gone keeps a tunnel until its pool's cap, attributed to the session that opened it. That is the residual of Envoy having no per-tunnel kill, and the per-pool cap bounds it.
- It chains a tunnel to an upstream proxy chosen per request. The decision response sets a route header and returns the upstream address in dynamic metadata. An `ORIGINAL_DST` cluster then dials that address, and Envoy forwards the CONNECT with an injected `Proxy-Authorization` or identity headers. The upstream proxy received only the injected credential, and the pod's token did not reach it. Changing a tenant's upstream needs no Envoy reload.
- Throughput: four parallel 4 GB downloads completed at about 2.7 GB/s in total, with Envoy at 130% CPU and under 80 MiB of memory.

The `http_11_proxy` transport socket does not chain raw CONNECT tunnels and is not used. An `ORIGINAL_DST` cluster that reads the upstream from a request header also failed, so the upstream address always comes from ext_authz dynamic metadata.

The third round repeated the baseline on both versions and added these results:

- **ext_authz in HTTP mode** works only when Envoy reaches the decision service over HTTP/2 (h2c). Over HTTP/1.1, Envoy sends the check with method `CONNECT` and every request fails with 503. `Proxy-Authorization` reaches the check only when `allowed_headers` lists it, and the peer address needs `headers_to_add` with `%DOWNSTREAM_REMOTE_ADDRESS%`. The gRPC form needs neither and remains the default.
- **The pod's credential on chained routes.** Unless the decision service removes it (`x-envoy-auth-headers-to-remove: proxy-authorization` in HTTP mode, or the equivalent header mutation in gRPC mode), Envoy forwards the pod's `Proxy-Authorization` to the tenant's upstream proxy. `lenny-egress-authz` removes it on every chained route, and a conformance test pins that.
- **Upstream proxy replies.** By default a chained upstream's 407 or 403 reaches the pod with its `Proxy-Authenticate` headers and body. The `custom_response` filter, matched per route, maps them to a fixed 502 or 403 with no upstream headers. `response_headers_to_remove` strips headers but passes the body, and `local_reply_config` does not apply to upstream replies.
- **IPv6.** IPv6-only targets work by literal and by AAAA-only names. `dns_lookup_family: AUTO` prefers IPv6 for dual-stack names, and `V4_PREFERRED` prefers IPv4. The address filter blocks ULA, link-local, loopback, mapped, and NAT64 addresses.
- **DNS rebinding.** The address filter runs on every resolution and refresh, and no private address was ever contacted. A name that rebinds to a private address keeps its last public address until `host_ttl` expires and is then refused. A tunnel that is already open keeps its address. Envoy flags the filter's range type as work in progress and outside its threat model at startup. The `lenny-egress` NetworkPolicy and the decision service's literal checks back it up.
- **Load** (2 CPUs, `--concurrency 2`). 1,000 concurrent tunnels had a CONNECT setup latency of 2.4 ms at p50 and 10.8 ms at p99, at 22% CPU and about 54 KiB per tunnel. 2,000 new CONNECTs per second, each with a decision call, had a p99 of 8.7 ms at 59% CPU. The default cluster circuit breaker caps each cluster at 1,024 concurrent tunnels and then queues and fails with `503 UO`, so the chart raises the breakers on every tunnel cluster.
- **A slow decision service** adds its latency to each CONNECT. Beyond the ext_authz timeout, every CONNECT fails closed at the timeout. With the decision service stopped, CONNECTs got 503 after about 0.5 s, and service recovered about 2 s after restart. The chart sets the ext_authz timeout to 250 to 500 ms, a decision-service connect timeout of about 250 ms, a breaker on the decision-service cluster sized to the CONNECT rate times the timeout, and a global downstream connection limit.
- **Close timing.** The default 1 s `delayed_close_timeout` delays the close of a tunnel that `max_stream_duration` ends, and the chart lowers it.
- **Decision-service resilience** (round 5, 500 CONNECTs per second against two decision endpoints). With HTTP/2 keepalive (1 s interval and timeout), outlier detection on local-origin failures, an active gRPC health check that ejects in about 1 s and readmits only on a passing check, and one retry on `previous_hosts`, killing one endpoint and freezing one with `SIGSTOP` each caused zero failed CONNECTs out of 30,000. Without the health check, the base configuration lost about 1% on a kill, mostly when the ejected endpoint was probed with live traffic after its ejection expired, and about 2% on a freeze. One pitfall: Envoy uses the retry policy's `max_interval` as the per-try timeout, so it must equal the per-try budget (0.4 s); at 50 ms it failed half of all CONNECTs.
- **Long streams** (round 5). 200 tunnels each carrying one chunk per second ran 20 minutes with none cut and Envoy's memory flat at about 27 to 33 MiB, with `stream_idle_timeout` 300 s, `idle_timeout` 3600 s, `max_stream_duration` 7200 s, and periodic access-log entries every 60 s. Two silent tunnels were cut at exactly 300 s by the idle timeout, as intended, and with a 120 s cap all 200 ended at 120 s with a clean close.
- **Decision latency** (round 5). Envoy has no ext_authz response cache, so caching lives in the decision service. At 1,000 CONNECTs per second with 50 ms decisions, Envoy added about 1.6 ms and held about 50 checks in flight per endpoint, so the decision service is sized at rate times latency times two.
- **Drain** (round 5). `POST /drain_listeners?graceful` with `--drain-time-s 20` kept accepting CONNECTs for 20 s and reset every tunnel between two thirds of the drain time and the drain time minus the HTTP connection manager's 5 s drain timeout, with a clean close. `/ready` stays 200 during the drain, so the Service endpoint must be withdrawn separately; a plain `SIGTERM` closes every tunnel at once. The chart's preStop hook calls the graceful drain and sleeps for the drain time, with `terminationGracePeriodSeconds` above it.

Envoy marks CONNECT alpha in a proto comment on `RouteMatch.connect_matcher` and `UpgradeConfig.connect_config` ("Note that CONNECT support is currently considered alpha in Envoy."), with a TODO to replace it with a formal tag. `upgrades.rst` marks only CONNECT-UDP and raw UDP tunnelling as alpha, and Envoy logged no alpha warning at runtime. The dynamic forward proxy, ext_authz, and `tcp_proxy` are marked stable. Envoy terminating CONNECT runs in production in Istio ambient waypoints (HBONE, HTTP/2 CONNECT) and in Google Cloud Secure Web Proxy, which is built on an Envoy proxy pool and accepts HTTP CONNECT. Recent advisories touch CONNECT and the dynamic forward proxy, among them CVE-2025-64763 (early CONNECT data in TCP proxy mode) and CVE-2025-54588 (a use-after-free in the DNS cache). The chart therefore pins a patched Envoy release and the project tracks Envoy security advisories. A conformance suite in the Lenny test tiers covers every behaviour above.

Its own egress NetworkPolicy allows the public internet minus `egressCIDRs.excludePrivate`, cluster CIDRs, and IMDS, using the same rule as the gateway's upstream rule (§13.2.5, NET-057). It also allows the registered upstream proxies and private git services listed in Helm values, which are the only private addresses the decision can name. That policy is the backstop for the `ORIGINAL_DST` cluster, which dials any address the decision names.

### Default proxy choice

| Candidate                     | Result in the hands-on spike                                                                                                                                                                                | Lenny code                                     | Main trade-off                                                                                                                                                                                            |
| :---------------------------- | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | :--------------------------------------------- | :-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Envoy 1.40                    | Passed CONNECT, per-connection decisions, SSRF filtering, the lifetime cap, and per-tenant chaining                                                                                                         | None (configuration only)                      | No per-tunnel kill. `netagent` closes tunnels in the pod, and `max_stream_duration` bounds a misbehaving pod.                                                                                             |
| Go proxy on Smokescreen       | Passed every test, including the lifetime cap and kill by lease within a second                                                                                                                             | About 200 lines in the spike, plus hardening   | Lenny owns a security-critical proxy and tracks a library with no tagged releases. The spike found two pitfalls Lenny code must close: a client header that selects an upstream, and a 407 on every deny. |
| Squid 7.7 with a Lenny helper | Passed everything except the lifetime cap: `client_lifetime` did not end active tunnels                                                                                                                     | A helper of about 30 lines, plus configuration | Revocation waits for the helper cache TTL. Per-tenant upstream credentials are static configuration lines. Squid has a heavy CVE history.                                                                 |
| Others                        | HAProxy, Caddy forwardproxy, nginx with the CONNECT module, Pomerium, Tinyproxy, mitmproxy, Apache Traffic Server, and the agent sandbox proxies each failed one or more requirements, or were unmaintained | n/a                                            | Ruled out. agentgateway 1.7 merits a later look.                                                                                                                                                          |

Envoy is the recommended default because it met every requirement with configuration alone, it implements the decision API natively over a widely used wire protocol (ext_authz, also used by Istio's CUSTOM authorization), and deployers who replace the default at level 2 can use the same protocol. The missing per-tunnel kill is covered by `netagent` and the lifetime cap. The Smokescreen-based proxy is the fallback if Envoy's CONNECT support regresses.

### Egress decision service (`lenny-egress-authz`)

`lenny-egress-authz` is a Lenny-built Deployment that serves the decision API (I3) and the pod binding API (I7). Credentials come from the Token Service on I8, with the decision service as one of two independent gates.

- It validates the caller's service account token and resolves the pod's binding.
- It computes the effective policy. The rules are under "Effective policy".
- It returns allow or deny, the route (`direct` or a named upstream with its address and credential or identity headers), the route's tunnel cap, and the attribution tags.
- It enforces quotas (P7). Rates (new CONNECTs and decision calls per second, per pod and per tenant) are counted in the decision service, which sees every open. Concurrency needs a close signal that ext_authz lacks, so `netagent` enforces the per-pod concurrent-tunnel quota in the pod, and Envoy streams its access log to the decision service through its gRPC access log service, which gives the per-tenant concurrency count and the tunnel-close visibility the audit trail needs. That stream is lossy: Envoy buffers and drops when the stream is down or full, and a restarting Envoy emits no close for its open tunnels. The periodic long-tunnel log entries therefore serve as heartbeats; a counted tunnel with no record for three intervals expires; the count resets per Envoy instance when the stream's identity changes; and the per-tenant concurrency quota is approximate, with an alert when the count and the per-pod sums disagree. The per-pod limit in `netagent` is the exact bound. Unbound warm pods count against a per-pool bucket. The chart refuses a configuration whose tenant quotas sum to more than Envoy's global downstream limit and tunnel breakers, and the defaults leave room for warm-up parallelism (`npm ci`) and for `maxConcurrentSessions` times the LLM streams a session holds. The mirror caps `upload-pack` concurrency per tenant.
- It rejects IP-literal targets in non-canonical forms (for example `0177.0.0.1` or `2130706433`) and private literals, because the proxy and the decision service can interpret those forms differently.
- It records every decision for audit (P5).
- It caches decisions per pod and host for a short TTL (30 seconds by default). The cache key includes a policy version that every tenant policy, host set, route, overlay, and binding write increments, so an edit takes effect at the next decision rather than at TTL expiry, and a pod's entries are dropped when its binding changes.
- It answers the Token Service's scope checks (see **Credential requests** below). It never holds a pod's Token Service token and never requests a credential itself, so a compromised decision service can misdecide, but it cannot mint.

**Credential requests.** `netagent` requests upstream git tokens, binding secrets, federation JWTs, and `metadata` credentials from the Token Service directly, on I8, presenting its `lenny-token-service` audience token. Two components gate every mint independently, and each reads its inputs itself:

- The Token Service verifies the pod token offline against the cluster issuer and resolves the pod itself through the shared `pkg/binding` package, which both components run as one implementation with two independent instances. The package is Kubernetes-backed for identity: an informer over the agent namespaces and `lenny-system` (the latter for the mirror and gateway service identities) gives the pod UID, the `lenny.dev/tenant-id` label (the authoritative tenant pin, `tenantpin.go`), the Sandbox and its pool, and `status.podIPs` for the peer-address check. The informer stores a projection of each pod (UID, name, namespace, labels, `podIPs`, and phase) rather than whole Pod objects, so three components caching the agent pods at the §17 scale stay small. Before the informer has synced, every pod is unknown and every request fails closed; on API server loss client-go serves its last cache, so a deleted pod's token stays valid until its expiry (600 s), which is the stated bound. The same outage stops the kubelet from renewing projected tokens, so every pod's egress fails closed about 600 s into it, the Kubernetes minimum for `expirationSeconds`; that is a stated residual with an alert, and it bounds how long existing pods keep working during an API server outage, and the component reports the stale cache in its readiness detail without failing the Token Service's readiness for the gateway lease path (F-4.3.3). It reads the pool record (owner tenant and overlay) and the session binding, including the provisional one, from the stores through the pooler, and the binding definitions (the VCS pool, the credential binding with its `hosts` and `attach` template, and the session's writable set) from the same stores. `agent_pod_state` is not an identity source: its `tenant_id` is a convenience column set on claim, as its schema comment says. The Token Service therefore gains `list` and `watch` on pods in the agent namespaces and `lenny-system`, and a pooler read of the pool and session stores (§13.2.4), and it enforces the writable set, the tenant allowlist, and the peer check from its own reads.
- It then asks `lenny-egress-authz` on I3 for the scope decision. That I3 call carries the verified pod UID, the observed peer address, and the requested scope, and never the pod's token, so a compromised decision service collects nothing it can replay. The decision returns allow or deny and the attribution tags, and no secret; tenant identity never comes from I3 in either component. The mint happens only when both gates agree, and each has derived the tenant from Kubernetes on its own.
- The I8 response carries the binding's authoritative `hosts` and `attach` template from the Token Service's own read, and `netagent` gates injection and TLS termination on that set only. The copy of the bindings in the I7 subscription is informational, so a compromised decision service cannot widen the hosts a secret travels to, and cannot widen a write mint within a tenant either. It can misdecide a CONNECT, which is the bound of a compromised policy decision point.

- The gateway stamps `lenny.dev/tenant-id` at bind, so a component's informer can see the label after the pod's first request arrives. `pkg/binding` therefore reports a pod whose session binding exists and whose label has not yet appeared as not yet visible, which it keeps distinct from an unknown pod. Every `pkg/binding` instance (the Token Service, the decision service, and the mirror) answers a retryable error for that state, `netagent` retries with backoff for up to 5 seconds, and nothing is minted or allowed until the label is seen. A label that never appears fails closed when the retry bound ends. Service mode has no claim and no session binding (§5.2), and it also has no arbiter today: `tenantaffinity.Router` is in-memory per gateway replica and picks an unpinned pod with no shared record, and `statelessproxy` treats a label failure as best effort and proxies anyway. Tenants on two replicas can therefore pin one pod, and the second tenant's request reaches a pod labelled for the first, under the first tenant's policy and `always` bindings, which breaks P4. The design requires a durable pin record:

  - **Arbitration.** A conditional insert keyed on pod UID in the session store decides the pod's tenant. A replica that loses to another tenant reroutes the request to another pod, a replica that loses to the same tenant adopts the pin, and a replica that cannot write aborts the request.
  - **Router.** The router keys on pod UID, which the EndpointSlice target carries, because it polls every 5 seconds and an IP reused between polls would otherwise leave a replica's pin on a new pod with no insert. It reads the tenant's records on a route miss, so a gateway rollout does not strand the tenant's pinned pods.
  - **Order.** The gateway inserts the record, pushes it as a binding change, and stamps the label before it forwards the first request, and a reconciler retries the label until it is visible, because `NewlyPinned` fires once and a failed stamp would otherwise wedge the pod. `pkg/binding` treats a record with no visible label as not yet visible and fails closed on a store read error, so a pod with a record is never evaluated under the warm-up policy. The decision service does not cache decisions for an unlabelled service-mode pod, so a warm-up decision cannot outlive the pin.
  - **Lifecycle and scope.** The record is removed when its pod's deletion is observed, and a periodic sweep removes records whose UID no longer exists. The table is platform-scoped, because a lookup by pod UID has no tenant context, and §12 declares it as such. §12.8 erasure deletes a tenant's records, and §12.9 classifies them.
  - **Availability.** This adds Postgres to the service-mode request path for a new pin and for a replica's route miss. During a store outage, requests that an already loaded pin can serve continue, and any other request returns `WARM_POOL_EXHAUSTED` with a retry hint. §5.2's in-memory statement changes accordingly.

`netagent` caches each minted credential until its `expiresAt` and refreshes it shortly before, so a binding under `npm ci` or `gh` costs one I8 call per lifetime rather than one per request. The cache is keyed on the binding generation, the session (the tenant pin for `always` bindings), and the binding definition's version, and `netagent` flushes it on every I7 binding change and session end. A tenant admin's edit of a binding definition (a host removed, a binding deleted) bumps that version, and the gateway pushes it as a binding change, so the edit reaches the pod at once rather than at the credential's expiry, so a credential never outlives the session it was minted for on a recycled, service-mode, or concurrent-session pod. A stored secret has no provider expiry, so the Token Service assigns it a cache lifetime of 15 minutes, which bounds how long a rotation takes to reach a pod. Revision 5 had the decision service relay the pod's token to the Token Service; a compromised decision service would then have held a valid token for every active pod and could have replayed each for anything the pod's binding allowed. The direct path removes the relay.

**The I8 listener.** I8 is the first pod-reachable surface of the component that holds every provider credential, and today the Token Service admits only the gateway over mTLS (§13.2.4). I8 therefore runs on its own port and listener with its own connection and goroutine budget, so pod load cannot starve the gateway-facing lease path or trip the gateway's Token Service breaker (F-4.3.3); it caps connections and requests per source address before authentication, and per pod and per tenant after it; it serves TLS with the deployment CA, which `netagent` verifies, so a stored push token or an API key never crosses the cluster network in cleartext before R14; and P7 counts credential calls with the other quotas. The `proxied`, `restricted` with `gitAccess`, and `internet` with `netagent` policies gain the I8 port as a pod-side peer, and the `lenny-system` ingress rule for the Token Service gains the agent namespaces on that port only. The mirror's own fetches use the same listener on the mirror's mTLS identity, scoped to the store's tenant and upstream.

**When the Token Service is unavailable**, `netagent` fails closed: a request to a bound host with no valid cached credential gets 503 from `netagent`, a federation token that cannot be rewritten before expiry is removed at expiry, and a `metadata` fetch returns 503. Cached credentials keep working until they expire, which bounds the outage a session notices to the credential lifetimes. The other consumers fail closed the same way: the gateway's `ls-refs` for a private repository returns `GIT_CLONE_REF_RESOLVE_TRANSIENT`, the adapter's materialization clone retries within its timeout, and a tenant-owned pool's warm fill retries with backoff while the pod stays out of READY, as 0111 requires, which the pool capacity alert surfaces.
- It receives `netagent`'s local decisions over I7 (path enforcement refusals, SSH remote refusals, redirect refusals, scrub hits, and tunnels closed at a binding change) and records them with the central ones, each with a decision identifier that the refusal returned to the agent carries. `lenny-ctl` queries decisions by session, so a 403 is traceable whichever hop produced it.
- It serves I3 on a listener that the `lenny-system` NetworkPolicy admits only from `lenny-egress`, mirror, and Token Service pods, and from gateway pods at level 1, I7 on a separate port with a per-pod rate limit, so a pod cannot flood the decision path, and the binding push on a port admitted only from gateway pods.

**Port scoping.** Every pod-side egress rule names a port: the proxy port on `lenny-egress`, the I7 port on `lenny-egress-authz`, the minting port on the Token Service, the git port on the mirror, and 53 on the resolver, as the chart already does for every existing peer. Envoy's admin interface, whose endpoints (`/quitquitquit`, `/drain_listeners`, `/runtime_modify`, `/config_dump`) authenticate nothing, binds to loopback inside the `lenny-egress` pod, and its metrics are served on a separate listener that the `lenny-system` policy admits only from the scrape source. The access log service port on `lenny-egress-authz` admits only `lenny-egress`. A tier 9 test confirms that a pod cannot open any other port on any of these pods.

**Private destinations.** A destination on a private address (a corporate gateway, an internal GitLab, or any platform-registered private service) is never resolved by Envoy. The registration stores the address, the grant names the tenants, and the decision returns the address in dynamic metadata for a granted tenant, so an `ORIGINAL_DST` cluster dials it. A private origin (a GitLab or a CRM) is reached by terminating the CONNECT and tunnelling raw bytes to that cluster, whereas a private upstream proxy receives a forwarded CONNECT; the spike validated the forwarded form, and round 6 validated the raw-tunnel form, including stock git through a pinned address. At the default level and level 2 a pod therefore has no network path of its own to a private destination, so a raw socket from any pod is dropped, and the `ipBlock` entries belong to the central proxy alone. A level 2 proxy must dial the address the decision returns and never resolve the name itself. `ORIGINAL_DST` gives Envoy this behaviour, and a proxy without it declares so in `centralProxy.level2.pinnedDestinations: false`, in which case validators refuse private destinations on that deployment. A tenant route may name only upstreams, endpoints, and groups the tenant holds, and the decision refuses any other. A compromised decision service can steer a tenant to any registered `ipBlock`; that residual is bounded by the `lenny-egress` NetworkPolicy, which admits only the registered addresses. The address filter keeps every private range with no exemption, which closes the path where a tenant points a hostname it controls at another tenant's private service. A tenant-registered public service (an upstream proxy or a git endpoint) is resolved at registration, its addresses are stored, the registration is refused when any address falls in the deployment's full exclusion list, in a registered `ipBlock`, or in the cluster's own node and load-balancer addresses (a hairpin to the cluster's public address is public by range), and the decision service re-resolves it periodically and suspends the registration when a later answer fails the same check. Private registrations stay with the platform admin, because each needs an `ipBlock` in the `lenny-egress` NetworkPolicy.

### Local git endpoint (`netagent`)

`netagent` serves git smart HTTP on `127.0.0.1:3129`. The pod's git config rewrites every remote URL to this endpoint, for any host. The config is under "Git configuration". `netagent` reads the operation from the `service=` parameter and the request path.

- **Fetch** goes to the git endpoint that the tenant's git routes name for the host: `lenny-git-mirror`, a deployer git service, or the upstream host through `lenny-egress`. A deployer git service on a private address is reached through `lenny-egress`, as a raw tunnel to the address the decision pins. One rule overrides the route: a repository under a VCS pool whose provider is not `mint-scoped` (`mint-coarse` or `stored`) is never served through the mirror, because the mirror may hold only tokens the host itself limits to one repository. `netagent`, the gateway resolver, and the mirror apply the same rule from the pool's provider level, so no component needs to know whether a repository is private before a 401: such a repository goes `direct` through `lenny-egress`, the resolver resolves it upstream, and the mirror refuses it. A tenant-owned pool's `gitRepository` warm fill on such a provider fills from the pod through `netagent`. For private repositories, `netagent` attaches the endpoint's credential (its service account token for the mirror, or a read token from I8 for a deployer service or the upstream). On a pool with `maxConcurrentSessions > 1`, the readable and writable sets are the union over the pod's sessions, because `netagent` cannot attribute a request to a slot (§13.1 **Per-slot credential-read scope**). The tenant accepts that with `acknowledgeProcessLevelIsolation`, and the documentation states that any slot can push to any repository in the union.
- **Push** requests a write token from the Token Service (I8) for the repository in the request path. The Token Service checks the session's write scope and the tenant's repository allowlist, with the decision service's scope decision, before it mints the token. `netagent` forwards the push through `lenny-egress` with the token, which stays in `netagent` memory (P3).
- **Read after push**: after a successful push through the mirror route, `netagent` asks the mirror to refresh that repository and holds the next fetch of the same repository from the same pod until the refresh completes, so a stale mirror cannot rewind `origin/*`. The mirror accepts a refresh request only for a repository in the caller's binding, and rate-limits it per repository.
- **Path enforcement**: `netagent` refuses repositories outside the binding's readable or writable sets, whatever a token's scope.
- **Redirects**: `netagent` rewrites an in-scope redirect (same provider, HTTPS, allowlisted repository) into loopback form and refuses every other redirect with 403. A passed-through redirect would send git straight to the host, around the endpoint. Neither GitHub nor GitLab redirects renamed repositories on smart HTTP, so redirects are rare in practice.
- **LFS**: git-lfs derives its batch URL from the rewritten remote, so batch requests reach the local endpoint, which forwards them to the host's LFS API. `netagent` reads the `operation` field of the batch request body: `upload` is a write, which takes the write-scope path (a write token and path enforcement), and `download` is a read. The object transfers that the batch response returns go to the host's storage domains through the egress proxy, so those domains belong to each provider's host set. LFS objects are not mirrored.
- **Streaming**: the prototype streamed 100 MB clones and a 50 MB chunked push with a peak RSS of 11 MB, and it handled protocol v2, partial clone, and shallow clone.

The local git endpoint shares the `netagent` container with the egress endpoint, for the same reasons. A separate process for git would add a second token holder and a second identity, with no isolation gain, because both endpoints hold credentials of the same class.

### Git mirror (`lenny-git-mirror`)

`lenny-git-mirror` is a Lenny-built StatefulSet with persistent volumes in `lenny-system`. It is a thin Go server over stock git, because no maintained, multi-tenant-aware git caching proxy exists to adopt (the first spike surveyed Goblet, git-cache-http-server, ngitcached, FINOS git-proxy, Artifactory, Nexus, and the hosting products' replicas).

- It serves upload-pack by running `git upload-pack --stateless-rpc` directly and flushing after each write. The second spike showed that Go's `net/http/cgi` holds keepalive and progress output until the response ends, and it rejects chunked request bodies.
- It keeps one bare repository per authorization boundary. A store's identity is the upstream URL, the fetch route, and the credential used. A public store is one filled anonymously through the platform default route, and it is the only store shared across tenants. A repository fetched through any tenant route, tenant-registered endpoint, or tenant credential gets a store keyed by that tenant as well, so a tenant cannot fill the store another tenant's warm fill reads. Objects never cross stores, because protocol v2 serves any object that exists by SHA without a reachability check.
- A public store whose upstream fetch starts failing with an authentication or not-found error (a repository made private or deleted) stops serving after one failed refresh, rather than serving its last copy to every tenant. Tenants that still need it fetch through their own credential into their own store.
- It authorizes each request twice: it resolves the caller's tenant through its own `pkg/binding` instance (the pod's label, or the gateway's asserted tenant) and refuses a store outside that tenant, and it asks I3 with the caller's service account token as the second gate, so a compromised decision service cannot serve one tenant's store to another. The mirror therefore runs the same informer and holds the same RBAC and API server egress as the other two instances, which proposal 5 carries.
- It runs every `git fetch` and `upload-pack` in a sandboxed subprocess, because a hostile pack exploiting a git parsing defect would otherwise reach every tenant's stores and credentials. The sandbox bounds a compromised subprocess to one store:
  - **Capability posture.** Every `lenny-system` workload drops all capabilities and runs non-root (§13.1), and switching UIDs needs capabilities. Kubernetes writes `capabilities.add` to the bounding and permitted sets and never to the ambient set, so a non-root entrypoint holds nothing after `exec` (kubernetes/kubernetes#56374), and file capabilities and setuid bits are void under `allowPrivilegeEscalation: false`. The mirror container is therefore the one stated exception to both rules: its entrypoint starts as root with every capability dropped except `CAP_SETUID`, `CAP_SETGID`, and `CAP_CHOWN`, and before it does anything else it sets `PR_SET_KEEPCAPS`, switches to the parent UID (65532) with its supplementary groups kept (so the parent keeps the `fsGroup` that reads the 0440 Secret), restores the three capabilities to its effective and inheritable sets, and then raises them into the ambient set, in that order, because the kernel clears the ambient set on a root-to-non-root switch; `setpriv` implements the same sequence. The long-running parent is then non-root and keeps the three capabilities across its own `exec`s. `allowPrivilegeEscalation: false` and the read-only root filesystem stay. The forked child sets its fetch UID and groups and drops every capability before it `exec`s git. RuntimeDefault seccomp already permits `setuid`, `setgid`, `setgroups`, and `chown`, so no custom profile is needed. The exception needs the `lenny-system` namespace at the `baseline` Pod Security level or the mirror in its own `lenny-mirror` namespace labelled `baseline`, which the chart documents and preflight checks; preflight's posture check also extends to StatefulSets, which it skips today, and it reads namespace labels only, so a cluster-wide enforced default that the labels do not show is documented as a gap. §13.1 and §17 gain a row for the exception. The ambient-capability start is listed under "Validation still owed". User namespaces (`hostUsers: false`, stable in Kubernetes 1.36) reduce the risk of this exception without removing it. Pod Security stops constraining `runAsNonRoot` and `runAsUser` for such a pod, so the root entrypoint no longer needs an exception at that level, and a process that escapes the container is an unprivileged UID on the node. The capability rules do not relax: `restricted` still refuses `CAP_SETUID`, `CAP_SETGID`, and `CAP_CHOWN`, so the `baseline` prerequisite and the §13.1 drop-all exception stay. Adoption also needs Linux 6.3 or later, containerd 2.0 or CRI-O 1.25 or later, runc 1.2 or crun 1.9 or later, and idmap mount support on every volume filesystem, which excludes NFS, and the chart's floor is Kubernetes 1.27 today. A user-namespace pod's IDs default to the range 0 to 65535, which also has to hold the fetch UIDs and the store GIDs, so adoption needs a safe rule for reusing fetch UIDs or an extended range. The extended range is the kubelet's `userNamespaces.idsPerPod`, which is node configuration the chart does not control.
  - **Per-store ownership.** Every store, public or tenant-keyed, has its own GID from a range the mirror owns, recorded in the store index. A fetch runs under a per-fetch UID with exactly its store's GID as its only group, and the store directory is group-writable (`2770` for a tenant store, `2775` for a public store, which is public content), so a compromised fetch can write one store and no other, which is what closes the shared public store to poisoning through a hostile public repository. Each store sets `core.sharedRepository=group`, so the directories and packs a fetch creates are group-writable and a later fetch under another UID can write them, and after each fetch the parent re-chowns the store's contents to the store's owner UID, which never executes anything, so no fetch UID keeps ownership of files once it exits. The re-chown walks the tree through directory file descriptors with `lchown`, never following a symlink a fetch may have planted, and it changes the owner only, so no group or mode change can be steered into another store; the parent takes the store's GID for the walk, because `CAP_CHOWN` grants no directory traversal. A fetch UID is never reused within the parent's lifetime, and the sweep runs at fetch exit, before the re-chown and before the fetch counts as complete: the parent, which is a child subreaper (`PR_SET_CHILD_SUBREAPER`) so that orphaned descendants reparent to it, forks a helper that switches to the fetch UID and sends `kill(-1)`, then waits for every process under that UID to exit. The parent holds no `CAP_KILL`, and the helper needs none, because a process may signal processes of its own UID. A descendant a compromised fetch left behind therefore cannot keep writing the store after the fetch is reported done, and cannot inherit a later fetch's forwarder; the range is large enough that a parent exhausts it only after a restart. The re-chown is best-effort for a directory a fetch made unreadable to its group, which the sweep makes harmless. `upload-pack` for a public store runs with no group and reads through the world-readable bits, so a hostile client cannot write a public store. `upload-pack` for a tenant store runs with a per-store reader group that a POSIX default ACL grants read and search only, so a client-side exploit by the tenant's own agent cannot poison the tenant's store either; on a volume without ACL support it runs with the store's group, and the residual is a same-tenant poisoning the documentation states. The mirror pod keeps the `fsGroup` the chart gives every `lenny-system` workload with `fsGroupChangePolicy: OnRootMismatch`, so the kubelet leaves the volume's ownership alone once the parent has initialized it on first start, and the mounted Secret that carries the I8 client key stays readable by the parent through the group at mode 0440 while fetch UIDs, whose only group is their store's, cannot read it.
  - **Identity and credentials.** The mirror's identity material (its service account tokens and its I8 client certificate) is readable by the parent UID only. The credential forwarder is per fetch, a Unix socket in the fetch's working directory whose parent end accepts only the fetch's UID (`SO_PEERCRED`) and pins the upstream host and repository path as well as the credential, so a subprocess cannot borrow a concurrent fetch's token or send its own token elsewhere.
  - **Network.** The subprocess shares the pod's network namespace, so network egress is not a sandbox property; what bounds it is that the subprocess holds no credential and no identity, that I3 and I8 require the parent's client certificate, and that the `lenny-system` policy allows the mirror pod to reach only `lenny-egress`, I3, I8, the API server, and the pooler. At level 1 the mirror also holds `ipBlock` rows for private git endpoints, so a subprocess under a hostile pack can open raw connections to every registered private git endpoint, across tenants, with no credential (see "Level 1 replacement").
  - A tier 9 test runs a compromised-subprocess stand-in against each boundary. Tenant-keyed stores can be sharded onto separate StatefulSet replicas when a deployment wants a harder boundary.
- It fetches upstream through `lenny-egress` on its own `lenny-egress` audience token (the mirror's service account in `lenny-system`) with an `x-lenny-tenant` header naming the store's tenant. The decision service honours a tenant assertion only from the mirror and gateway service identities, which it recognizes from the token's service account claim and checks for liveness through its `lenny-system` informer, and it then applies that tenant's routes (a private git endpoint or an egress group). Read credentials come from I8 on the mirror's client certificate, with (tenant, upstream) as the request: the Token Service checks that the tenant's VCS pool covers the upstream and that the tenant's git route names the mirror for that host, and releases `mint-scoped` read tokens only, so a compromised mirror parent cannot read the repositories of a tenant that never routed through it. `mint-coarse` tokens (an Azure DevOps Entra token is the identity's full token, with write capability) and `stored` credentials are never released to the mirror, because the Token Service cannot verify a mirror's tenant claim beyond the pool match, so repositories on those providers route `direct` from the pod under the rule stated for the local git endpoint. The mirror's parent process holds the credential and attaches it on a loopback forwarder that the sandboxed git subprocess fetches through, so the subprocess never holds it. A public store fetches anonymously through the platform default route. It serializes fetches per repository, sets `transfer.fsckObjects=true`, disables `gc.auto`, schedules bitmap repacks, and caps `pack.threads` and `pack.windowMemory` per request.
- It purges a tenant's stores under the §12.8 erasure flow (`DeleteByTenant`), and §12.9 classifies mirror contents and credential bindings.
- It serves the warm fill from proposal 0111 over smart HTTP.

The mirror is optional. These capabilities depend on it:

| Capability                      | With the mirror                                            | Without the mirror                                                                      |
| :------------------------------ | :--------------------------------------------------------- | :-------------------------------------------------------------------------------------- |
| Warm fill of large repositories | Pods fill from a local copy at LAN speed.                  | Each warm pod clones from the upstream host.                                            |
| Upstream load and rate limits   | One upstream fetch serves every pod.                       | Every pod fetch counts against the host's limits, and private fetches mint read tokens. |
| Upstream outage                 | The mirror resolves refs within its age limit and serves the objects it holds. | Clones fail until the host recovers.                                                    |
| Read credentials                | Held centrally in the mirror, for `mint-scoped` providers. | `netagent` requests a read token for each private fetch.                                |
| Consistency across pods         | All pods see the same refs until the next refresh.         | Each pod sees the upstream at its own fetch time.                                       |
| Validation                      | Every fetched object passes `fsckObjects` once, centrally. | Each pod relies on its own `transfer.fsckObjects`.                                      |

Without the mirror, git still works through `netagent` and `lenny-egress`, more slowly and with more upstream traffic.

## Credentials

### Current state

- A tenant admin creates credential pools. Since proposal 0112, each credential's Secret lives in the `lenny-credentials` namespace, and the Token Service reads it on each request.
- A VCS credential pool has a provider (today `github`), `hostPatterns` naming the git hosts it serves, and credentials. A tenant has at most one pool per host (`ResolveVCSPool`).
- The `github` provider's Secret holds a single stored `token`. §4.9 describes minting GitHub App installation tokens, but no code mints anything (F-4.9.44).
- In-pod git uses `git-credential-lenny`, which calls the `lenny/vcs_token` tool through the adapter. The tool returns the pool's stored token for any matching host. It ignores the requested mode and never checks the session's plan or the repository.
- The gateway's own clone (`gitClone`) sends the stored token as an HTTP header and never shows it to the pod. That path is broken in deployed images, which have no `git` binary (F-14.1.37).

### Target state

**Minting.** The Token Service mints short-lived tokens from long-lived provider credentials that never leave the `lenny-credentials` namespace.

- For `github` and GitHub Enterprise Server, the Secret holds `appId`, `privateKey`, and `installationId`. The Token Service signs an App JWT and calls `POST /app/installations/{id}/access_tokens` with `repositories` and a `permissions` subset (for example `contents: write`, and `pull_requests: write` for a credential binding). Tokens last one hour.
- GitHub documents caching installation tokens until they expire. The Token Service caches one token per (installation, sorted repository set, permissions) and refreshes it shortly before expiry. GitHub's documented secondary limits that bound minting are 900 points per minute per REST endpoint, where a POST costs 5 points (180 mints per minute per App), and 100 concurrent requests. The limit of "2,000 OAuth access token requests per hour" is documented for user access tokens, and GitHub does not say whether installation tokens count against it. GitHub Enterprise Server disables rate limits by default. The Token Service counts mints per App against the lower of these bounds and raises an alert as the count approaches it.
- The Token Service needs an egress route to the host APIs it mints from. It gets a NetworkPolicy rule with the same exclusions as the gateway's upstream rule.

**Provider capability levels.** Each provider declares what it can mint, which determines what Lenny allows by default.

| Level         | Providers                                                                                                                                                                                             | Token                                                         | Default use                                                                                                      |
| :------------ | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | :------------------------------------------------------------ | :--------------------------------------------------------------------------------------------------------------- |
| `mint-scoped` | GitHub, GitHub Enterprise Server, AWS CodeCommit (STS session policy on the repository ARN)                                                                                                           | Short-lived and limited to named repositories and permissions | Fetch, push, and credential bindings                                                                               |
| `mint-coarse` | GitLab project access tokens (minted per session and revoked at session end, because expiry has day granularity); Azure DevOps Entra ID tokens (about one hour, scoped to the identity's permissions) | Short-lived or revocable, but wider than one repository       | Fetch, push, and credential bindings, with `netagent` path enforcement narrowing use to the session's repositories |
| `stored`      | Bitbucket Cloud, Gitea, Forgejo, generic `git-https`, and GitLab without project tokens                                                                                                               | A long-lived token the tenant supplies                        | Fetch only, unless the tenant opts in to stored-token push                                                       |

**Stored-token push.** Some hosts cannot mint short-lived tokens. On those hosts, a tenant can push only with a long-lived token it stores in a credential pool, such as a personal access token, an app password, or a repository access token created by hand.

- `allowStoredTokenPush` on the VCS credential pool enables this. The tenant admin sets it, and only when the platform admin has granted the tenant the `storedTokenPush` class.
- When it is enabled, `netagent` fetches the stored token through I8 for each push and keeps it in memory. The agent UID never sees it.
- `netagent` limits the token's use to the session's writable repositories. That limit is enforced by Lenny code in the pod, whereas a `mint-scoped` limit is enforced by the host.

These risks remain:

- The token is usually wider than one repository. A personal access token often covers every repository its owner can write.
- A compromise of the `netagent` container, or of the node, exposes the token's full scope for its lifetime.
- Revocation is manual.

To reduce the risk, the documentation recommends the narrowest token each host offers: GitLab project tokens, Bitbucket repository access tokens, and Forgejo repository-specific tokens.

**Repository routing.** A VCS credential pool gains repository path prefixes (for example `github.com/acme/` and `github.com/acme-labs/`), so a tenant can hold one GitHub App installation per organization. This replaces the one-pool-per-host rule.

**Write scope.** A token is minted with write permission only when the session's plan declares the repository writable and the VCS credential pool allows writes. The mechanism has these steps:

1. **Declaration.** The workspace plan already marks each `gitClone` source with `auth.leaseScope` `vcs.<provider>.read` or `vcs.<provider>.write` (§14). The design adds `writableRepositories` for repositories the session pushes to without cloning them, such as a fork. At session bind, the gateway records the session's writable set: every source with a write scope plus `writableRepositories`, each checked against the tenant's repository allowlist and the pool's `allowWrite`. A plan that declares a repository outside those bounds is rejected at create.
2. **Detection.** git push is a smart HTTP request for the `git-receive-pack` service on a repository path. `netagent` sees the service name and the path in plain text on `127.0.0.1:3129`, because the agent's git sends plain HTTP to the loopback endpoint.
3. **Request.** `netagent` asks the Token Service on I8 for a write token for that repository. Fetches ask for read tokens, and a fetch never receives a write token.
4. **Check.** The Token Service re-reads the pod's binding and asks `lenny-egress-authz` for the scope decision; the request is refused unless the repository is in the session's writable set. For a refused push, nothing is minted, and the push fails with 403.
5. **Mint.** The Token Service mints a token limited to that repository (`repository_ids: [<id>]`, `permissions: {contents: write}`) and caches it for its hour. The pool stores each allowlisted repository's numeric identifier, resolved when the admin adds it, because GitHub serves renamed repositories under their old names and a name is therefore not a stable identity. GitHub enforces the limit, so a token used outside `netagent` still cannot write elsewhere.
6. **Use.** `netagent` attaches the token to the push it forwards and keeps it in memory. The agent never receives it.

Tokens are minted per operation rather than at session start, so a session that never pushes never holds a write token, and a pod whose session ended can obtain none. GitHub requires `workflows: write` to push changes under `.github/workflows/`. The default token omits it, and a session gets it only when its plan requests it and the pool's `allowWorkflowEdits` permits it. For `mint-coarse` and `stored` providers, step 5 yields a wider token, and step 4 plus `netagent` path enforcement are the only repository limit.

**Replaced pieces.** `cmd/git-credential-lenny`, the `lenny/vcs_token` tool, and §14's in-pod credential helper sentence are removed.

**The gateway runs no git.** Today the gateway clones each `gitClone` source (`gitref.Clone`), packs the tree into an archive, decodes it through the archive gate in the gateway, and streams the files into the pod (§14 `gitClone` row, §7.1 step 12). The shipped gateway image has no git binary, so that path fails in every deployed image (F-14.1.37). The design removes it:

- The adapter runs the clone in the pod against `http://127.0.0.1:3129/...`. It holds no credentials, because `netagent` attaches them. In the embedded model, the embedded adapter runs it at the agent UID.
- Ref resolution (`resolvedCommitSha`) stays in the gateway and stays synchronous at plan bind, so the §14 contract and its errors (`GIT_CLONE_REF_UNRESOLVABLE` and `GIT_CLONE_REF_RESOLVE_TRANSIENT`) are unchanged and a bad ref costs no claimed pod. The gateway no longer runs `git ls-remote`; it sends a protocol v2 `ls-refs` request over smart HTTP in Go, with a protocol v0 `info/refs` fallback for a host that refuses v2. Round 6 found GitHub, GitLab, Bitbucket Cloud, Gitea, Forgejo, kernel.org, and sourcehut all answering v2. The request is bounded: `ref-prefix` narrows the v2 advertisement, strict pkt-line parsing applies, the whole call is capped at 10 seconds and a v2 response at 1 MiB, and each tenant has a concurrency limit, so a hostile host cannot hold gateway goroutines or stream an unbounded advertisement. v0 cannot narrow, and the advertisements of `kubernetes/kubernetes` and `gitlab-org/gitlab-runner` exceed 1 MiB, so the v0 path streams under a 32 MiB cap, keeps only the candidate refs, and stops once the sorted advertisement passes them. Redirects are refused, with one exception: a redirect to the same scheme and host whose path differs only by a trailing `.git` is followed once, because GitLab answers the suffixless path with a 301. The request goes through `lenny-egress` on the gateway's own identity, carrying the tenant and the session being created, and the decision service applies that tenant's effective policy and routes to it, including a private git endpoint or an egress group, and requires the host to match one of the tenant's VCS pool prefixes or git routes. The read credential is attached only when the URL lies within a pool's repository prefix, which replaces today's `hostMatchesPatterns` wildcard match in `credentialpoolstore/vcsresolve.go`. One shared canonicalizer serves the gateway resolver, the create-time validation of `gitClone` and `writableRepositories`, and `netagent`'s path enforcement: it lower-cases the host, drops userinfo and default ports, refuses non-default ports unless the prefix names one, resolves dot segments and percent-encoding, refuses `;` parameters and a trailing `.git` mismatch, and attaches a credential only to the canonical form, so `bitbucket.org/acme/../evil/repo` cannot carry a tenant's stored token outside the allowlist. When the tenant's git route for the host is the mirror, the gateway resolves against the mirror on the gateway's mTLS identity carrying the tenant. The mirror forwards the same bounded `ls-refs` upstream and answers from that, so a session gets the host's current commit whether or not the store has caught up. The mirror's upstream call has an 8-second limit, below the gateway's 10-second cap, and it coalesces identical `ls-refs` calls per store and ref for 5 seconds, so one tenant's volume does not multiply against a host that throttles by source address. It answers from the store only for an explicit list of causes: no reply from `lenny-egress`, a CONNECT 503, a failed connection or TLS handshake to the host, a timeout, or a host 502, 503, or 504 inside the tunnel. Every other answer is passed on and never falls back: a host 401, 403, 404, or 429, so a repository made private does not resolve from a public store, and a CONNECT 403 or 502, because `custom_response` turns a chained gateway's refusal into a 502. An unavailable mint counts as a listed cause when the provider or the Token Service is down, and a refused mint does not. The fallback also requires a confirmation within the last 6 hours (a Helm value). A confirmation is a (ref, SHA) pair recorded by a successful `ls-refs` or fetch, including a prefetch or a warm fill, and the store index persists it across a mirror restart. The fallback answers only with the ref's most recent pair, and only when the store holds that commit, so a force-push seen by `ls-refs` just before a host failure returns the transient error rather than the retracted commit. A host 401, 403, or 404 and a refused mint stop the store's fallback and clear its confirmations until the next success, so a later 503 cannot serve a repository made private or removed from an App installation. The session records `resolvedFromStore` and the confirmation time, which `GET /v1/sessions/{id}` reports, so an old commit is visible and its age is bounded. Beyond that age the answer is `GIT_CLONE_REF_RESOLVE_TRANSIENT`. The mirror still asks I3 on every request, so the fallback covers upstream and `lenny-egress` outages only, and a decision service outage returns the transient error. When the answer names a commit the store lacks, the mirror schedules a fetch, so the first session for a new repository is not held by a full fetch; the pod's later fetch of an object the store lacks waits on that scheduled fetch: the mirror reads the `want` lines before it runs `upload-pack` and answers 503 with a retry hint when the covering fetch is still running. A `want` the store lacks with no covering fetch, which a plan that pins a full SHA produces because it skips resolution, schedules a fetch of that repository under the caller's binding and gets the same answer; a fetch that completes without the object ends the wait with a failure. An agent can trigger these misses by looping `git fetch origin <random-sha>`, so scheduling is bounded by a 60-second negative cache per store and SHA, which any completed fetch of the repository clears so a plan pinned to a newly pushed commit is not refused, a per-tenant rate on scheduled fetches (P7), and a minimum interval between fetches of one repository, which keeps one tenant from delaying a public store or spending the mirror's shared source address. git does not retry, so `netagent` holds the pod's request and retries the mirror with backoff for the pool's fetch retry window before it returns the failure to git. The window applies once per initial materialization, over every `gitClone` source of the plan together, and it must sit below the watchdog that bounds it: `maxFinalizingTimeoutSeconds` (600 s by default, §6.2) less the Runtime's setup bound (`setupPolicy.timeoutSeconds`, 300 s by default), less 60 s, which admits the 180 s default. The pool validator reads the Runtime for that bound, taking the derived-runtime maximum where one applies (§5.1) and the §6.2 default for a pool without a Runtime reference, and a Runtime update that raises the setup bound is refused while any pool referencing it holds a window the new bound no longer admits, with the refusal naming the pools. A snapshotless resume rebuild runs the clone and the setup commands under the 300 s `resuming` watchdog (§7.3), which leaves no room for the window, so on the resume path `netagent` waits 30 s at most and then fails fast; a resumed session's repositories were fetched when the session first ran, so a pending fetch there means the mirror lost its store, and the next resume attempt succeeds once the fetch completes. The mirror's fetch continues after the pod gives up, so a repository too large for the window fails the first session at finalize as `SESSION_CREATION_FAILED` with the reason `git_fetch_pending` (§6.2 surfaces materialization failures there, and the `GIT_CLONE_REF_*` codes keep their plan-bind meaning) and serves the next one from the store; the gateway's `ls-refs` miss at plan bind already starts that fetch before the pod is claimed, which covers most repositories. For a large repository a tenant admin prefetches it through the mirror prefetch resource of the admin API (a tenant-scoped request naming a repository under one of the tenant's pools, which the mirror fetches into the tenant's store, or into the public store for an anonymous fetch), or lists it as a `gitRepository` warm fill, so no session pays for its first fetch. An upstream outage therefore blocks session creation only for refs the upstream has not confirmed within the age limit. The SHA then travels to the pod in the plan, and the adapter clones that SHA. No SHA reported from the pod is trusted.
- Resolution depends on the egress path at session create, and round 6 ran that path through Envoy against real hosts. The gateway mounts a projected token with the `lenny-egress` audience and has no fallback that dials a host directly.
  - **One tunnel per call.** The client disables connection reuse. With reuse, 20 calls alternating between two tenants produced one decision, attributed to the first tenant, so the second tenant's policy and routes were never consulted.
  - **Attempts.** Each attempt must receive its CONNECT reply within 1.5 seconds, and a failed attempt retries once on a new connection, inside the 10-second cap. A stopped or frozen Envoy replica under load then caused no failed call, at a cost of 1.5 seconds at worst.
  - **Errors.** No CONNECT reply, a proxy 503, 502, or 429, the time cap, and a host 5xx or 429 map to `GIT_CLONE_REF_RESOLVE_TRANSIENT`, and a proxy 502 also raises an alert because it can mean a chained gateway refused Lenny's credential. A CONNECT 403 or 407 maps to `GIT_CLONE_REF_UNRESOLVABLE` with the reason `egress_denied`, and the byte cap, a malformed response, a refused redirect, and an absent ref map to `GIT_CLONE_REF_UNRESOLVABLE`. The CONNECT reply decides the class, because a dial timeout in Go also satisfies `context.DeadlineExceeded` and would otherwise read as a slow host.
  - **Private git endpoints.** The resolver reaches a platform-registered private endpoint the way pods do, as a raw tunnel through `lenny-egress` to the address the decision pins, so it shares their availability. Level 1 differs, as "Level 1 replacement" states.
  - **Scope of the dependency.** Sessions without `gitClone`, and plans that pin a full commit SHA, need no resolution and are unaffected. During a `lenny-egress` outage, callers get a retryable error within 3 seconds, and a mirror-routed repository resolves from the store within its age limit. During a decision service outage every resolution returns the retryable error. Pods that clone through `lenny-egress` fail closed in the same outage (P6), so for them the dependency adds no outage that a directly dialing resolver would have avoided. A pod that clones from the mirror can still clone during a `lenny-egress` outage, and its session can start when the store resolves the ref.
  - **Cost.** Envoy added about 0.3 ms per call against a local host, and the hosts' own latency (280 to 390 ms at p50) dominated against real ones. The path served 4,271 calls per second with a p99 of 130 ms on a laptop, far above any session create rate.
  - **Deployments without a central proxy.** A plan that needs resolution fails with `GIT_CLONE_REF_UNRESOLVABLE` and the reason `no_egress_path`. Level 2 proxies must pass `x-lenny-tenant` to their I3 call, and level 1 follows "Level 1 replacement".
- F-14.1.37 is closed by deleting the gateway clone path rather than by adding git to the gateway image. The adapter image, and the embedded runtime images that run the clone, need a git binary. The adapter image is distroless today.
- A session whose plan has a `gitClone` source is rejected at create when its pool has no git path (a `restricted` pool without `gitAccess`).
- The tree checks that the archive gate applied are covered as described under "Residual risks and limits", **Tree validation**.

The 0111 open item on workspace ownership carries over: a repository the adapter checks out is owned by UID 65532, so the runtime UID's git refuses it as dubious ownership unless the checkout is made group-writable and owned for the runtime. Proposal 3 resolves it.

## Credential delivery to runtimes

Pull requests, issues, releases, private package registries, and any other HTTP API a runtime calls go through the egress route with standard HTTP. Lenny adds no platform tool for them, and the delivery mechanism is generic: it knows hosts, headers, and files, and nothing about git, GitHub, or any other product.

### Credential bindings

A tenant admin configures access with a **credential binding**:

- `secret`: the source of the value. It is either a credential pool whose provider mints a token (for example a GitHub App, with a `permissions` ceiling such as `pull_requests: write`), or a stored secret in the credential namespace, such as an API key.
- `hosts`: the exact hosts the value may be sent to. Wildcards are refused, and each host must lie within the tenant's effective allow set, so a prompt-injected agent cannot make the tenant's key travel to a host the tenant never allowed. `netagent` never attaches the value to a request for any other host. A binding whose `secret` is a stored token is refused when any of its hosts is a git host that one of the tenant's VCS pools covers, because `git push` over the proxy to that host would otherwise carry the long-lived token with no path enforcement and outside the `storedTokenPush` grant; git access to those hosts goes through the local git endpoint only, and API access to them uses a minted token or a binding on the host's API hostname.
- `attach`: how the value goes on the request, as a header template. Examples are `Authorization: Bearer {{secret}}`, `Authorization: Basic {{base64 user ":" secret}}`, and `X-Api-Key: {{secret}}`.
- `placeholder`: the non-secret value a tool sees in place of the secret. It defaults to the constant `lenny-injected` (see **Placeholders under `inject`.** below).
- `scope`: `always`, which attaches the binding to every session of the tenant, or `onRequest`, which attaches it only to a session whose plan names it.
- `delivery`: `inject`, `federation`, `metadata`, or `file`.
- `audience` and a provider template, for `federation` delivery.
- `projections`: for `file` delivery, the extra file formats to render (see **Which files `netagent` writes.** below).

A session names its `onRequest` bindings and, for a minted token, can narrow the permissions. The session does not enumerate APIs. One binding covers one secret and its hosts, however many endpoints those hosts expose.

### Delivery modes

| Mode | Where the secret is | Who can read it | Rotation | Availability |
| :-- | :-- | :-- | :-- | :-- |
| `inject` (default) | `netagent` memory only. The pod sees the placeholder. | No process in the runtime or adapter container | Transparent to the tool | Always available |
| `federation` | A 15-minute Lenny-issued JWT in a file, which the provider's SDK exchanges at the provider | Any process at the agent UID | `netagent` rewrites the token, and the SDKs re-read it on each exchange | Tenant opt-in behind the `agentReadableDelivery` grant. It is an exception to P3, limited to a short-lived token bounded by the tenant's trust conditions. |
| `metadata` | `netagent` memory, served on loopback in a provider's metadata endpoint format | Any process at the agent UID that reads the endpoint's token file | Each fetch returns current credentials, and the SDKs refresh before expiry | Tenant opt-in behind the `agentReadableDelivery` grant. A provider plugin; AWS ships first. |
| `file` | A file in `/run/netagent/credentials`, written by `netagent` | Any process at the agent UID | `netagent` swaps the directory atomically, and the tested tools re-read their files on each invocation | Tenant opt-in behind the `agentReadableDelivery` grant. It is an exception to P3. |
| Environment variable | Never used | n/a | n/a | Never offered |

**`inject`** keeps the secret out of the pod. The tool sends a request carrying the placeholder, or no credential, and `netagent` sets the header from the `attach` template when the request is for one of the binding's hosts. The check is on the request's authority, derived once: `:authority` on HTTP/2, and on HTTP/1.1 the request-target's host when the request is in absolute form, with a `Host` header that disagrees refused. The authority, with its port, must equal the bound host exactly, and the check is not on the CONNECT authority alone: on a shared edge, a request for `attacker.example` inside a connection to `api.example.com` would otherwise carry the injected header to the attacker's origin. `netagent` opens the upstream connection with the bound host as the server name and validates the upstream certificate against the public roots and the deployer CA only, never against the per-pod CA. `netagent` overwrites the header rather than substituting text inside it, so encoded forms such as Basic authentication work, and a placeholder copied into a request body or query is never replaced. A request to any other host leaves `netagent` unchanged, where the placeholder is meaningless. Two transports reach `netagent` in plain text:

- **Plain HTTP to a bound host.** A tool configured to call `http://<host>/...` sends an absolute-form request through `HTTP_PROXY`. `netagent` attaches the header and forwards the request over TLS through `lenny-egress`, so the cleartext stays on loopback. No CA is involved. Fly.io's tokenizer uses the same upgrade. The third validation round showed that `glab` with `api_protocol: http` sends exactly this request through `HTTP_PROXY`, and that curl does so for `http://` URLs.
- **TLS termination for bound hosts only.** For tools that hard-code HTTPS (for example `gh`), `netagent` terminates TLS for the binding's hosts with a per-pod CA and passes every other host through as an opaque tunnel. `netagent` generates the CA at first start and keeps its key in its private emptyDir, so a restart reuses it and the runtime's trust stays valid. The CA carries X.509 name constraints that permit only the hosts of the pod's `inject` bindings, and `netagent` re-issues it when a binding adds a host. OpenSSL, Go, rustls, NSS, and Chromium enforce constraints on a trust anchor, so for those clients a leaked key cannot sign for any other name. Java's PKIX ignores a trust anchor's extensions, so JVM clients would trust a leaked key for any name; the limit is per pod and matters only after a `netagent` or node compromise, and the documentation states it. It writes the certificate into `/run/netagent` before the runtime starts (native sidecar ordering, validated in round 3), and the client proxy profile adds it to the CA trust variables. Vercel Sandbox, Cloudflare Sandbox, Daytona, E2B, and the Claude Code cloud GitHub proxy use the same pattern. Round 3 validated it with mitmproxy 12.3.1 standing in for `netagent` (see "Validation record").

`inject` cannot serve these cases, which need `federation`, `metadata`, or `file` delivery:

- Request signing computed from the secret on the client, such as cloud request signing (`federation` or `metadata`) or HMAC-signed requests (`file`).
- mTLS client certificates.
- Tools that pin certificates or use compiled-in roots, under TLS termination.
- Tools that validate the token format locally and reject the placeholder.
- OAuth flows in which the client must hold a refresh token.

A response that reflects the secret (an echo endpoint, or an error that quotes the request) would carry it into the pod. `netagent` scrubs the exact value from response headers, bodies, and WebSocket server frames on bound hosts:

- It decodes `gzip` and `deflate` bodies before scrubbing. Round 3 confirmed that reflections in plain, compressed, and finely chunked bodies never reached the client.
- A streaming response (SSE, or any body `netagent` does not buffer) is scrubbed with a sliding window that holds back one byte less than the secret's length, so a secret split across chunks is still caught and events still arrive as they are sent. Buffering the whole body instead delayed every SSE event to the end of the stream.
- WebSocket frames from the server need their own scrub. Without it, a reflected secret in a frame reached the client.
- A reflection re-encoded by the server, such as base64 or hex, defeats the scrub. The documentation states this as a residual risk.

`netagent` also strips hop-by-hop headers such as `Proxy-Connection` before forwarding, which mitmproxy passed upstream in the plain-HTTP test.

**Gates.** The opt-ins that place a credential in the agent's reach fall into three risk classes, and each class has its own gate: `storedTokenPush` (a long-lived secret held by `netagent`), `agentReadableDelivery` (`file`, `federation`, and `metadata`), and `verbatimClientFiles`. A gate is a platform-admin grant per tenant, not a deployment-wide switch, and the tenant admin then opts in per binding or pool, so enabling `file` for one tenant enables nothing for another and nothing in another class.

**`federation`** gives a runtime a short-lived identity token that it exchanges itself, at the provider, for the provider's own credentials. It serves SDKs that sign requests and so cannot use `inject`, and it is provider-neutral: Lenny's part is one JWT issuer and a set of file templates, and every provider that accepts OIDC workload identity federation consumes it through its own SDK. The clouds (AWS, GCP, and Azure), HashiCorp Vault, and most secrets managers accept it, and Kubernetes itself uses the same mechanism for its own workload identity.

- **Issuer.** The Token Service is an OIDC issuer with one issuer URL per tenant, `https://<lenny>/oidc/<tenant-id>`, keyed by the tenant identifier rather than the name. The identifier is caller-supplied at creation (`admin/tenants.go`), and the §12.8 tenant tombstone prevents its reuse, so a deleted and recreated tenant cannot inherit the trust the old one's admin configured; the issuer's uniqueness rests on that tombstone. Each tenant has its own federation key set, held in the Token Service's key store (`kms.go`) beside its other keys, signing nothing else, and destroyed when the tenant is deleted, so a federation token is useless at any Lenny endpoint and dies with its tenant. Rotation keeps the previous key published for the token lifetime plus the providers' JWKS caching period. The `/oidc/` path on the gateway ingress is reserved for the Token Service, and discovery must be reachable by the providers' token endpoints, which means a publicly routable ingress with a publicly trusted certificate; a deployment without one cannot offer `federation`, and uses `metadata` instead. A tenant's trust configuration names its own issuer, so another tenant's tokens fail the issuer check by construction, and the tenant admin needs no claim condition to keep P4. A binding deleted and recreated under the same name keeps its subject, so the tenant admin treats a binding name as the identity its provider role trusts.
- **Token.** For a session with a `federation` binding, `netagent` requests a JWT from the Token Service on I8 and writes it to `/run/netagent/credentials/<name>/token`. The claims are the tenant's issuer, the binding's `audience`, the stable subject `lenny:<tenant>:<binding>`, and separate `pool` and `session` claims for audit. The subject is stable because AWS trust policies condition only on `aud`, `sub`, and `amr`, and Azure requires an exact subject match. The token lasts 15 minutes, and `netagent` rewrites it at two thirds of its lifetime. The SDKs re-read the file on each exchange; the Azure SDKs cache it for 5 to 10 minutes, which is within that lifetime.
- **Trust.** The tenant admin configures the provider to trust the tenant's issuer for that audience and subject, and grants the resulting identity the provider role or permissions the agent needs. This is the same step a platform team performs for a Kubernetes cluster's workload identity today. Where the provider supports it (AWS `aws:SourceIp`, GCP and Azure conditional access), the tenant also binds the exchange to the egress address of its egress group, which makes a token taken out of the pod useless elsewhere.
- **Client configuration.** The binding's provider template renders the provider's standard client configuration file into the binding's directory, naming the token path. The profile sets the constant variable that names that file. Lenny ships templates for the three clouds as data, and a deployer adds others.

| Provider | File rendered | Launch variable | Per-tenant values | Validation (round 4) |
| :-- | :-- | :-- | :-- | :-- |
| AWS | A shared config profile with `role_arn`, `web_identity_token_file`, `role_session_name`, `region`, and optional `services` endpoints | `AWS_CONFIG_FILE` | All in the file | boto3, AWS SDK for Go v2, JavaScript v3, and Java v2 assumed the role, signed requests, and re-read a changed token on refresh |
| GCP | An `external_account` JSON with `audience`, `token_url`, `credential_source.file`, and optional impersonation | `GOOGLE_APPLICATION_CREDENTIALS` | All in the file, except the project lookup below | google-auth for Python, Node, and Go exchanged the token and re-read it on refresh |
| Azure | None. The SDKs read `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, and `AZURE_FEDERATED_TOKEN_FILE` from the environment only | The three variables | The tenant ID and client ID cannot come from a file | azure-identity for Python, Node, and Go exchanged the token and re-read it after their cache expired |

Two caveats come from the validation:

- The Python and Node Google libraries look up the project at `cloudresourcemanager.googleapis.com` during initialization. The `googleapis` host set includes that host, so the lookup succeeds through the proxy when the policy allows it. Otherwise the binding sets `GOOGLE_CLOUD_PROJECT`, which then becomes a per-tenant launch variable with the limit below.
- Azure's identifiers are per-tenant launch variables, and launch variables cannot vary per tenant on a shared pool. An Azure `federation` binding therefore works on a tenant-owned pool, or in runtime code that passes the identifiers from `binding.json` to the credential constructor through the SDK accessor. The validation confirmed that all three Azure SDKs accept them as constructor arguments.

The JWT is readable by any process at the agent UID, which makes `federation` an exception to P3 in the same way as `file`, limited to a 15-minute token whose use the tenant's trust conditions bound. The `agentReadableDelivery` grant controls it. The provider exchange is an HTTPS request to the provider's token endpoint, which the effective policy must allow. A token taken out of the pod can be exchanged from anywhere within its lifetime unless the trust binds the source address, so the documentation recommends that binding and roles with the narrowest permissions.

**`metadata`** emulates a provider's instance metadata or container credentials endpoint on loopback, for a tenant that cannot configure federation trust. The Token Service holds the provider credential and mints the short-lived credentials that `netagent` serves. Round 3 and round 4 validated the three formats: the AWS container credentials endpoint with boto3, the AWS CLI, and the Go, JavaScript, and Java SDKs; the GCE metadata server with google-auth for Python, Node, and Go; and the Azure App Service managed identity endpoint with azure-identity for Python, Node, and Go. Each format needs its own paths, headers, and response document, so `metadata` is a provider plugin in `netagent` with a stated interface rather than data. Lenny ships the AWS plugin, because AWS is the one provider whose endpoint the SDKs restrict to loopback, and the other two follow when a deployer needs them. Three details from the validation hold for every plugin: a token file must not end in a newline, the Python Google library needs `GCE_METADATA_IP` as well as `GCE_METADATA_HOST`, and the emulator must serve each probe path the SDK sends before the token path.

**`file`** writes the secret into the pod.

No standard governs how a platform hands a secret to a workload as a file. The practices in common use are these:

| Practice | Examples | Fit for Lenny |
| :-- | :-- | :-- |
| One file per secret in a directory that an environment variable names | Kubernetes Secret volumes, Docker and Compose secrets in `/run/secrets/<name>`, and systemd credentials in `$CREDENTIALS_DIRECTORY` | Generic. Any program can read a file, and Kubernetes rotates such directories by an atomic symlink swap. |
| `.netrc` | curl, git, GNU wget, Python requests, pip, and the Go toolchain | Host-scoped Basic credentials only. Round 3 found uneven support: curl needs `--netrc`, httpx needs explicit `NetRCAuth`, and BusyBox wget, npm, Node fetch, and Java ignore it. Only requests, pip, and Go read the `NETRC` variable. |
| OIDC workload identity federation | Kubernetes service account tokens consumed by AWS IAM Roles for Service Accounts, GKE Workload Identity, Azure Workload Identity, and Vault JWT auth | A standard, provider-neutral mechanism with one issuer on the platform side. This is the `federation` mode. |
| Cloud workload credential endpoints | AWS container credentials, the GCP metadata server, and the Azure managed identity endpoint | Standard within each cloud's SDKs, and short-lived, but each has its own format. This is the `metadata` mode. |
| Tool-native config files | `gh` `hosts.yml`, `.npmrc`, `pip.conf`, and Docker `config.json` | Each tool owns its format. |
| Exec credential helpers | git credential helpers, Docker credential helpers, kubectl exec plugins, and AWS `credential_process` | Each is a tool-specific protocol, and each needs a helper binary in the image. |

The `file` contract follows the first practice:

- The launch environment sets `LENNY_CREDENTIALS_DIR=/run/netagent/credentials`. The path sits outside `/run/lenny`, which §13.1 reserves for the credential tmpfs.
- Each `file` binding of the session gets a directory, `<name>/`. It holds `value`, the raw secret with no trailing newline, and `binding.json`, with the name, hosts, `attach` template, `expiresAt`, and a version counter.
- `netagent` replaces a binding's directory atomically by writing a new directory and swapping a symlink, as the kubelet does for Secret volumes. A reader that opens the file on each use always sees a complete value.
- The files have mode 0444 in an emptyDir that only `netagent` writes, and the runtime container mounts it read-only.

**Which files `netagent` writes.** `netagent` writes the generic directory for each `file` binding and nothing else, unless the binding lists `projections`.

- Lenny ships one projection, `netrc`. It is a single file at `/run/netagent/credentials/netrc` with an entry for each binding whose `attach` template is Basic, and the profile sets `NETRC` to it. It serves requests, pip, and Go.
- Lenny ships no tool-specific projections. A deployer adds one as a file template in the client proxy profile, such as a `gh` `hosts.yml`.
- `glab` refuses a config file unless its mode is exactly 0600, which a file written by the `netagent` UID cannot give the agent UID. `glab` therefore uses `inject` with TLS termination, configured through `GITLAB_HOST` and a `GITLAB_TOKEN` placeholder.

**Translation by the runtime.** A runtime that builds its own clients reads the generic directory, through the SDK accessor (see "Runtime SDKs") or directly, and configures those clients itself. Tool knowledge then stays with the runtime author, who knows which tools the image carries. Lenny adds no template engine or tool formats to the SDKs, for two reasons:

- A command the agent runs in a shell never passes through runtime code, so a translation in the runtime would not reach it.
- A rendering step in the runtime would have to watch for rotation.

**Placeholders under `inject`.** `inject` needs no file. The tested tools send no credential, or a placeholder, and `netagent` overwrites the header. A tool that refuses to run without a token (round 3: `gh`) gets a placeholder through an environment variable in the profile, such as `GH_TOKEN` or `GH_ENTERPRISE_TOKEN`. `netagent` ignores the placeholder's value, so the placeholder is the constant `lenny-injected` rather than a per-binding value, and it can be fixed at pod creation.

**Environment variables** are never used for secrets, and §5.1 already forbids them. Compared with a file, an environment variable has these additional exposures:

- `/proc/<pid>/environ` keeps the start-time value even after the process unsets it.
- Every child process inherits it.
- Diagnostic reports include it, such as Node's report on an uncaught exception.
- The Lenny supervisor renders the runtime's environment as command-line arguments.
- It cannot be rotated without restarting the process.

OWASP's secrets management guidance and CWE-526 make the same recommendation. A placeholder in an environment variable is not a secret, so `inject` can expose one as `GH_ENTERPRISE_TOKEN=lenny-injected`. Round 3 confirmed that `gh` refuses to run with no token and runs with an environment token.

## Interfaces

| Interface                 | Between                                                                    | Protocol                                                                                                                                                                                                                                                                                                                                                                                   | Defined by                                |
| :------------------------ | :------------------------------------------------------------------------- | :----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | :---------------------------------------- |
| I1. Local egress endpoint | tools in the pod and `netagent`                                            | HTTP CONNECT on loopback, no authentication                                                                                                                                                                                                                                                                                                                                                | Standard                                  |
| I2. Proxy hop             | `netagent` and the central proxy (Lenny default or a deployer replacement) | HTTP CONNECT with `Proxy-Authorization: Basic`. The password is the service account token for the default or a level 2 proxy, and the identity's current credential for a level 1 proxy.                                                                                                                                                                                                          | Standard                                  |
| I3. Decision API          | proxy, git endpoint, or Token Service and `lenny-egress-authz`             | Envoy ext_authz v3, in both its gRPC and HTTP forms. A documented mapping to the OpenID AuthZEN Authorization API 1.0 (final since 2026-01-12) is optional, because no proxy calls AuthZEN today.                                                                                                                                                                                          | Standard (ext_authz)                      |
| I4. Policy document | Lenny and a level 1 proxy                                  | A versioned Lenny JSON document listing each credential identity with its allowed host patterns, ports, and credential verifiers, served by the gateway admin API. Lenny also publishes a plain-text domain list per identity in the External Dynamic List format that security gateways and firewalls ingest over HTTPS. No standard exists for per-identity egress allowlists. NPEP-133 `domainNames` selects pods rather than proxy identities. | Lenny, with EDL output                    |
| I5. Git endpoint          | `netagent` and a git service                                               | git smart HTTP, protocol v2 for fetch, with Basic or Bearer authentication                                                                                                                                                                                                                                                                                                                 | Standard                                  |
| I6. Upstream chaining | `lenny-egress` and a deployer upstream proxy | Forwarded HTTP CONNECT carrying `Proxy-Authorization: Basic` when the upstream accepts it. Identity is otherwise the egress group's source address. `X-Authenticated-User` and `X-Forwarded-For` are optional, for upstreams that read them on CONNECT or that decrypt. | Standard (de facto for security gateways) |
| I7. Pod binding API       | `netagent` and `lenny-egress-authz`                                        | A Lenny gRPC API authenticated by the `lenny-egress` audience token. It covers the binding subscription (including the proxy Service to dial), the base-destination list, `netagent`'s local decision reports, and at level 1 a decision call for each CONNECT.                                                                                                                                                                  | Lenny                                     |
| I8. Pod credential API    | `netagent` and the Token Service; also the mirror on its mTLS identity     | A Lenny gRPC API over TLS, authenticated by the `lenny-token-service` audience token (or the mirror's client certificate). It covers requests for git tokens, binding secrets, federation JWTs, `metadata` credentials, and level 1 proxy credentials, and its responses carry the binding's authoritative hosts and attach template. The Token Service consults `lenny-egress-authz` on I3 for the scope decision. | Lenny                                     |

## Configuration by scope

### Scopes and who manages them

| Setting                                                                                                                                              | Description                                                                                                                                                                                                                                                       | Applies to                                       | Managed by                                                              | Notes                                                                                                                                                                                                |
| :--------------------------------------------------------------------------------------------------------------------------------------------------- | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | :----------------------------------------------- | :---------------------------------------------------------------------- | :--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Components enabled (`netagent`, `lenny-egress`, `lenny-egress-authz`, `lenny-git-mirror`), images, and replicas                                      | Selects which of the Lenny egress and git components the chart installs, the images they run, and the replica count of each.                                                                                                                                      | Deployment                                       | Platform admin (Helm)                                                   |                                                                                                                                                                                                      |
| Central proxy implementation: the Envoy default or a replacement address and contract level                                                          | Chooses whether pod egress goes to the shipped Envoy `lenny-egress` to a deployer proxy that calls the decision service (level 2), or to a deployer proxy that enforces a published host list (level 1, not recommended; see "Level 1 replacement").                                                       | Deployment                                       | Platform admin (Helm)                                                   |                                                                                                                                                                                                      |
| SSRF exclusion ranges (private, cluster, IMDS, IPv4-mapped, and NAT64)                                                                               | Lists the destination address ranges the central proxy refuses after DNS resolution, so an allowed hostname cannot resolve to an internal address.                                                                                                                | Deployment                                       | Platform admin (Helm)                                                   | They can be extended but never removed below the shipped list.                                                                                                                                       |
| `ipBlock` entries for private upstream proxies and private git services                                                                              | Adds the addresses of private upstream proxies and private git services to the NetworkPolicy of the central proxy, so traffic to them is allowed. Pods receive no such entry at the default level or level 2, and level 1 scopes them by tenant.                                                                                                  | Deployment                                       | Platform admin (Helm)                                                   | NetworkPolicy matches addresses only, so private destinations need these entries.                                                                                                                    |
| Tunnel lifetime menu, and the pool's choice                                                                                                          | The chart renders a menu of `max_stream_duration` values as Envoy routes, and each pool names one. The cap bounds how long a tunnel outlives a revoked lease or an ended session.                                                                                  | Deployment (menu) and pool (choice)              | Platform admin                                                          | The pool's choice must exceed its longest expected stream, such as an LLM completion.                                                                                                                |
| Gates on tenant opt-ins, one per risk class, granted per tenant                                                                        | Grants a tenant the right to turn on the opt-ins of one risk class: stored-token push, agent-readable delivery (`file`, `federation`, and `metadata`), or verbatim client files.                                                                                                                                                          | Deployment and tenant                            | Platform admin (admin API)                                              | Without the grant, the tenant cannot enable that class's opt-ins.                                                                                                                                            |
| Client proxy profile (default) and CA trust bundle                                                                                                   | Defines the environment variables and file templates that point in-pod tools at the local endpoints, and the CA certificates the pods trust when an upstream intercepts TLS.                                                                                      | Deployment                                       | Platform admin (Helm or admin API)                                      | See "Injected configuration".                                                                                                                                                                        |
| Host sets                                                                                                                                            | Names reusable lists of host patterns and ports, such as `github` or `pypi`, that ceilings, tenant policies, and warm-up policies reference.                                                                                                                      | Deployment (catalog)                             | Platform admin. Lenny ships defaults, and tenants can define their own. | Named lists of host patterns and ports.                                                                                                                                                              |
| Egress upstreams on private addresses, and private git endpoints                                                                                     | Registers proxies and git servers on private addresses, such as a corporate gateway or an internal GitLab, and grants each one to specific tenants.                                                                                                               | Deployment (catalog)                             | Platform admin (admin API), granted to tenants                          | These need `ipBlock` entries in the `lenny-egress` policy. The decision pins their addresses, so the address filter keeps no exemption.                                                              |
| Egress groups | Names additional `lenny-egress` Deployments that the chart renders, each leaving the cluster from a dedicated address, so a corporate gateway or a cloud trust policy can identify a tenant by source address. | Deployment (Helm) | Platform admin, granted to tenants | The address comes from cloud NAT, a CNI egress gateway, or a dedicated node pool. A granted tenant's pods dial the group's proxy instead of the default (see "Defaults, replacement, and augmentation"). |
| `egressProfile`                                                                                                                                      | Selects the network posture of the pool's pods (`restricted`, `proxied`, or `internet`), which selects the NetworkPolicy each pod receives. `provider-direct` is removed (see "Relationship to the current pool egress configuration").                           | Pool                                             | Platform admin                                                          | A tenant admin sets it only on a pool its tenant owns. Today a tenant admin can change it on a pool shared with other tenants (§10.2 "Manage egress profiles"), and the design restricts that write. |
| Egress ceiling (host sets and patterns, or `*` for any public host)                                                                                  | Sets the largest set of hosts any pod in the pool can reach through the proxy, whichever tenant or session it serves.                                                                                                                                             | Pool                                             | Platform admin                                                          | The outer bound for every tenant using the pool. A tenant overlay can narrow it, and can extend it only when the platform admin allows that for the pool.                                            |
| Warm-up policy (hosts and repositories reachable before binding)                                                                                     | Lists the hosts and repositories a pod can reach while it warms, before it is bound to a tenant or a session.                                                                                                                                                     | Pool                                             | Platform admin                                                          | It covers warm fills, shared assets, and SDK-warm processes. It applies before any tenant is known, so tenants cannot override it. Bound pods use the tenant warm-up additions instead.              |
| Fetch size cap and fetch retry window                                                                                                                | Caps the bytes one fetch may transfer through `netagent` or the mirror, and bounds how long `netagent` retries the mirror for a repository whose first fetch is still running.                                                                                | Pool                                             | Platform admin                                                          | The retry window applies once per initial materialization and must sit below the finalize bound less the Runtime's setup bound; the resume path uses a fixed 30 s wait.                                                             |
| `gitAccess` for `restricted` pools                                                                                                                   | Allows pods in a `restricted` pool to reach the git endpoint and the mirror while all other egress stays closed.                                                                                                                                                  | Pool                                             | Platform admin                                                          | Lets a `restricted` pool reach git endpoints without general egress.                                                                                                                                 |
| Client proxy profile override or extension                                                                                                           | Replaces or extends the deployment's client proxy profile for one Runtime or one pool.                                                                                                                                                                            | Runtime or pool                                  | Platform admin                                                          | For toolchains the default profile misses.                                                                                                                                                           |
| Tenant egress policy: always-allowed hosts, session-requestable hosts, and denied hosts                                                              | Defines the hosts every session of the tenant reaches, the hosts a session can request, and the hosts that are always refused.                                                                                                                                    | Tenant                                           | Tenant admin                                                            | Bounded by the pool ceiling.                                                                                                                                                                         |
| Tenant warm-up additions                                                                                                                             | Adds the tenant's own hosts and repositories to warm-up for pods that already belong to the tenant.                                                                                                                                                               | Tenant (tenant-owned pools and pinned idle pods) | Tenant admin                                                            | Bounded by the pool ceiling.                                                                                                                                                                         |
| Egress routes (host patterns to `direct` or an upstream)                                                                                             | Maps host patterns to the path the central proxy takes for them: a direct connection, or a chain through a named upstream proxy.                                                                                                                                  | Tenant                                           | Tenant admin                                                            | The upstream is one granted by the platform, or one the tenant registered on a public address.                                                                                                       |
| Tenant-registered upstreams and git endpoints on public addresses                                                                                    | Registers upstream proxies and git servers that the tenant operates on public addresses.                                                                                                                                                                          | Tenant                                           | Tenant admin                                                            | Resolved and pinned at registration, refused when any address is private or in a registered `ipBlock`, and re-resolved periodically (see "Egress decision service", **Private destinations**).      |
| Credentials for upstream proxies (Basic credential or identity header value)                                                                         | Stores the secret the central proxy presents when it chains a connection to an upstream proxy.                                                                                                                                                                    | Tenant                                           | Tenant admin (`secretData`)                                             |                                                                                                                                                                                                      |
| Mirror prefetch                                                                                                                                      | Asks the mirror to fetch a repository under one of the tenant's pools ahead of any session, so a large repository's first fetch is paid before a session needs it.                                                                                       | Tenant                                           | Tenant admin                                                            | Rate-limited per tenant and bounded by a per-tenant mirror storage quota (P7); refused for repositories the routing rule sends `direct`.                                                                                                                   |
| Git routes (host to the mirror, a git endpoint, or `direct`)                                                                                         | Maps git hosts to the place `netagent` sends their git traffic: the mirror, a registered git endpoint, or the host itself.                                                                                                                                        | Tenant                                           | Tenant admin                                                            |                                                                                                                                                                                                      |
| VCS credential pools: provider credential, repository prefixes, repository allowlist, `allowWrite`, `allowWorkflowEdits`, and `allowStoredTokenPush` | Defines the provider credential `netagent` uses for each repository prefix, the repositories sessions can use, and whether push, workflow-file edits, and stored-token push are allowed.                                                                          | Tenant                                           | Tenant admin                                                            | `allowStoredTokenPush` requires the `storedTokenPush` grant.                                                                                                                                         |
| Credential bindings                                                                                                                                  | Defines a secret for any HTTP API or registry (a minted provider token or a stored API key), the hosts it may be sent to, how it is attached (header and format), whether it attaches to every session or only on request, and its delivery (`inject`, `federation`, `metadata`, or `file`). | Tenant                                           | Tenant admin                                                            | Every delivery but `inject` requires the `agentReadableDelivery` grant. See "Credential delivery to runtimes".                                                                                      |
| Client config files (for example `.npmrc` with a private registry, or `pip.conf` with an index URL)                                                  | Supplies the tenant's tool configuration files, which `netagent` writes into the pod when the pod is bound to the tenant.                                                                                                                                         | Tenant                                           | Tenant admin                                                            | Written verbatim by `netagent` into `/run/netagent/config/<name>` at binding. A verbatim file is an arbitrary-content channel into the pod, so it needs the `verbatimClientFiles` grant. |
| `gitClone` sources with read or write scope, `writableRepositories`, and workflow-edit requests                                                      | Names the repositories the session clones, the repositories it can push to, and whether it edits workflow files.                                                                                                                                                  | Session                                          | Client                                                                  | Bounded by the tenant's VCS pools and repository allowlist. Rejected at create when out of bounds.                                                                                                   |
| `egress.allowedHosts`                                                                                                                                | Lists the hosts (host sets or patterns) the session has access to. Without it, the session gets the tenant's always-allowed hosts.                                                                                                                                | Session                                          | Client                                                                  | Each entry must lie within the tenant's always-allowed or session-requestable hosts. Not available on concurrent-session pools.                                                                      |
| Credential bindings attached                                                                                                                         | Names the tenant's on-request credential bindings the session uses, and for a minted token optionally a narrower permission set.                                                                                                                                  | Session                                          | Client                                                                  | Bindings marked `always` attach without a request. Not available on concurrent-session pools.                                                                                                        |

Tenant admins hold most of the configuration surface. The platform admin sets the outer bounds (the ceiling, the SSRF ranges, private-address services, and the opt-in gates) and the settings that must be fixed at pod creation. Tenant admins can register services on public addresses themselves, which follows the connector precedent. Services on private addresses need a platform registration, because they need a NetworkPolicy `ipBlock` on `lenny-egress` and a pinned address in the decision.

Tenant admins cannot create runtimes or pools. §15.1 states that runtime and pool records are platform-global, that only `platform-admin` creates runtime and pool definitions (derived runtimes included) or grants access, and that `tenant-admin` updates configuration for records already granted to its tenant.

### Tenant pool overlays

Today a pool is one platform-global record. A tenant admin whose tenant has a grant edits that record in place (§10.2, the "own tenant" scoping note), so the edit changes the pool for every tenant that shares it. No per-tenant layer exists: `pool_tenant_access` holds only the grant. Per-tenant override layers exist for other resources, such as the tenant credential policy (§14) and the tenant-level `leaseExtension` override (§8).

The design adds a **tenant pool overlay**: one record per pool and tenant, written by the tenant admin, which applies only to that tenant's pods. The platform admin controls each overridable setting with a per-pool flag, `tenantOverride`, whose values are `none`, `narrow` (the default), and `narrowOrExtend`.

- Narrowing reduces only the tenant's own access, so `narrow` is the default.
- Extending needs `narrowOrExtend`, set by the platform admin per pool and setting.
- A setting applied at pod creation (the NetworkPolicy label, environment variables, and the CA trust bundle) cannot vary per tenant on a shared pool, because warm pods exist before any tenant. The overlay can set those only on a pool the tenant owns (proposal 0111 `ownerTenantId`), where every pod belongs to the tenant from creation.
- The tenant admin's global write on a shared pool is restricted for the egress and git settings below. The tenant admin writes the overlay instead.

| Pool setting | Overlay on a shared pool | Overlay on a tenant-owned pool | Flag |
| :-- | :-- | :-- | :-- |
| Egress ceiling | Narrow, or extend when allowed. The extension still excludes the SSRF ranges. | Same | `tenantOverride.egressCeiling` |
| Warm-up policy | None. Unbound pods have no tenant. Bound pods use the tenant warm-up additions. | Replace, within the ceiling | `tenantOverride.warmUp` |
| `gitAccess` | Disable, which the decision service enforces. Enabling needs the NetworkPolicy, which is fixed at creation. | Enable or disable | `tenantOverride.gitAccess` |
| `egressProfile` | None | Choose among the profiles the platform admin lists in `tenantEgressProfiles` | `tenantEgressProfiles` |
| Client proxy profile | File templates only, under the `verbatimClientFiles` grant, because a tenant template is an arbitrary-content channel | Environment variables and file templates, with the same grant for templates | `tenantOverride.clientProfile` |
| CA trust bundle | None | Add the tenant's CA, such as its gateway's interception CA | `tenantOverride.caBundle` |
| Fetch size cap | Narrow, or extend when allowed | Same | `tenantOverride.fetchSizeCap` |
| Fetch retry window | Narrow | Same, within the pool's validated bound | `tenantOverride.fetchRetryWindow` |

The overlay mechanism is general, and other pool settings, such as scaling, can adopt it later. This design defines it only for the egress and git settings above. Two edges are stated: a platform admin who lowers a flag from `narrowOrExtend` to `narrow` invalidates stored overlays that extend, and the write is refused until those overlays are reduced; and enabling `gitAccess` through an overlay on a tenant-owned pool changes the pods' NetworkPolicy label, which takes effect on the next pod roll, so the API reports it as pending until then.

### An open ceiling

A pool ceiling of `*` (any public host) with tenant policies as the effective control is a supported setup. Its safety rests on four points:

- The SSRF exclusions still apply, so `*` covers public addresses only.
- A tenant's policy defaults to the platform's shipped default policy, which allows nothing beyond the default host sets.
- Every connection is decided and logged per tenant, so an open ceiling still produces a complete audit trail.
- What a tenant opens affects only its own pods.

The remaining risk is the exfiltration limit described under "Residual risks and limits". A tenant that allows a host accepting uploads gives its agents a channel to that host.

### Relationship to the current pool egress configuration

| Current setting                                                                                                     | Effect of the design                                                                                                                                                                                  |
| :------------------------------------------------------------------------------------------------------------------ | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `egressProfile: restricted`                                                                                         | Kept. `gitAccess` can add the git endpoints.                                                                                                                                                          |
| `egressProfile: provider-direct`                                                                                    | Removed. See "Consolidating `provider-direct`" below.                                                                                                                                                 |
| `egressProfile: internet`                                                                                           | Kept for unfiltered egress and non-HTTP protocols. `netagent` can still run in these pools for git credentials and audit, with an added NetworkPolicy rule that allows the in-cluster Lenny services. |
| New `egressProfile: proxied`                                                                                        | Pod egress goes only to the base destinations, the `proxied` resolver, `lenny-egress`, `lenny-egress-authz` (I7), the Token Service (I8), and `lenny-git-mirror`, each on its port.                                                              |
| `deliveryMode`, the base policy, the object store and OTLP policies, and the `egressCIDRs.excludePrivate` and `excludeIMDS` Helm values | Unchanged. `egressCIDRs.providers` goes with `provider-direct`, and `proxied` pods get a per-profile resolver address (see "Residual risks and limits", **DNS channel**). |
| §26.2 "coding runtimes default to `restricted` with an allowlist" and the non-existent `unrestricted` value         | Removed. See "Runtime categories in §26.2" below.                                                                                                                                                     |

### Consolidating `provider-direct`

`provider-direct` works as follows today (§13.2.6):

- It exists for `deliveryMode: direct`, where the pod receives a short-lived LLM provider lease and calls the provider itself. NET-006 pairs `direct` with `provider-direct`, and pairs `proxy` with `restricted`, where the gateway calls the provider.
- The chart renders `allow-pod-egress-provider-direct`, with one `ipBlock` per entry of the Helm value `egressCIDRs.providers` on TCP 443, minus the IMDS addresses (`agent-network-policies.yaml`).
- The list is deployment-wide and holds CIDRs only, because NetworkPolicy cannot match names. The default is empty, so a pod with `provider-direct` and no CIDRs reaches nothing beyond the base destinations.

Its weaknesses are that provider CIDR ranges are wide and shared (a cloud provider's ranges include every tenant's endpoints on that cloud), that they change without notice and are maintained by hand, and that one list serves every pool.

Recommendation: remove it now. No deployment depends on it, so no migration path is needed. `deliveryMode: direct` pairs with `proxied` and a pool ceiling that includes an LLM provider host set (for example `anthropic`, `bedrock`, and `vertex`). The pod then reaches the provider by name, with per-connection decisions and audit, and the tenant overlay can narrow the providers per tenant. The removal touches NET-006 and NET-044; the chart template `agent-network-policies.yaml`, `credentialpool-egress-guard.yaml`, and `admission-policies/direct-mode-isolation-webhook.yaml` with their tests; `egressCIDRs.providers` in `values.yaml`; the admission validators in `pool_config_validator` and `direct_mode_isolation`; the CRD enum and `pkg/embedded/crds`; `pkg/sandbox/egress/profile.go`; `poolstore.go`, `admin/pools.go`, `bootstrap_resources.go`, `sessionserver/credential_preclaim.go`, and `warmpool/controller.go`; preflight; and `tests/tier9_security/pool_admission_isolation_test.go`. LLM traffic through `lenny-egress` is a long-lived streaming CONNECT. The tunnel lifetime cap must exceed the longest LLM stream, or the client retries.

### Runtime categories in §26.2

§26.2 "Shared Patterns for Coding-Agent Runtimes" states that "Coding-agent runtimes default to `egressProfile: restricted` ... HTTPS to the operator-configured allowlist" and that unrestricted egress needs "`egressProfile: unrestricted`". Neither the allowlist nor the value exists. The same section keys other defaults on the coding-agent category: `isolationProfile: sandboxed`, `deliveryMode: proxy`, `setupCommandPolicy`, and `preConnect: false`.

The owner commits of June 2026 (`f79b3b788` and `5b98a6d08`) removed platform enforcement keyed on a coding-agent category, and BUILD-GAPS records that the §26.1 catalog table "carries a documentation-only \"Category\" column". The egress paragraph and the other category-keyed defaults survived that change. No record of the owner's broader feedback that the specification should not define runtime types and their defaults was found.

The design removes the category-keyed prose from §26.2. Platform defaults apply to every Runtime alike, and §5 and §13.2.6 state them. Each shipped reference runtime documents its own `defaultPoolConfig` descriptively in its §26 entry (for example, "the `claude-code` reference runtime ships `egressProfile: proxied` with the host sets `github`, `npm`, and `pypi`"). This change sits in proposal 2, and T-26.2.3 closes with it.

## Effective policy

`lenny-egress-authz` computes the effective policy from the binding:

In these formulas, `ceiling` is the pool's egress ceiling with the tenant's pool overlay applied (see "Tenant pool overlays"). Every pool-level setting therefore applies in every case, including concurrent-session pods. The cases differ only in which tenant and session inputs apply.

- **Unbound pod** (warm, before any tenant): `ceiling ∩ pool warm-up policy`, where `ceiling` is the pool's own ceiling, because no overlay applies without a tenant.
- **Tenant-bound pod without a session** (a tenant-owned pool's warm pod, a pinned idle pod, or a service-mode pod): `ceiling ∩ (tenant always-allowed ∪ tenant warm-up additions) − tenant denied`.
- **Pod with one session**: `ceiling ∩ session allowed − tenant denied`. `session allowed` is the session's `egress.allowedHosts` list when the plan sets one, and the tenant's always-allowed hosts otherwise. Each entry of the list must lie within `tenant always-allowed ∪ tenant session-requestable`, or the session is rejected at create.
- **Pod with concurrent sessions**: `ceiling ∩ tenant always-allowed − tenant denied`. The pool settings and the tenant settings apply. Per-session inputs (`egress.allowedHosts` and per-session credential bindings) do not, for a mechanical reason: one runtime process serves every slot (§28.5.3 **Intra-pod**), and all slots share one agent UID and one network namespace (§13.1 **Per-slot credential-read scope**). `netagent` sees connections from one process and cannot tell which session opened them, and any slot could use another slot's grant. A session on such a pool that sets `egress.allowedHosts` is rejected at create. The pod is tenant-pinned (§5.2), so the policy is per pool and tenant.

Readable repositories are the session's `gitClone` sources plus the tenant's repository allowlist, together with the public `gitRepository` entries of the Runtime's `sharedAssets` (an entry without `auth`, as proposal 0111 defines it). Writable repositories are described under "Credentials", **Write scope**. The route for each host is the first matching tenant route, then the platform default (`direct`).

### Scenarios

**A coding agent with defaults.** Pool `coding` is `proxied` with ceiling `npm, pypi, go-proxy, github`. Tenant `acme` allows the same host sets and has a GitHub App pool for `github.com/acme/`. Alice starts a session that clones `acme/web` with write scope.

- `npm ci` and `pip install` reach their registries.
- `curl https://example.com` gets 403, which is logged with tenant `acme` and Alice's session.
- `git push` mints a one-hour token for `acme/web` with `contents: write`, which stays in `netagent`.
- A push to `acme/payments` is refused by `netagent`, because that repository is outside the session's write scope.

**A research agent with open web access.** Pool `research` is `proxied` with ceiling `*`. Tenant `acme` allows `*` but denies `pastebin.com` and `*.ngrok.io`.

- The agent browses any public site.
- `http://169.254.169.254` and `http://10.0.0.5` get refused by the SSRF filter.
- Every connection is logged per session.

**A regulated tenant behind its corporate gateway.** The platform admin registers upstream `globex-swg`, Globex's security web gateway, and egress group `globex-egress`, whose replicas leave the cluster from a dedicated NAT address. The admin registers that address with Globex's gateway as a trusted location and grants both to tenant `globex`. Globex routes `*` to `globex-swg` through `globex-egress`.

- `lenny-egress` forwards every Globex connection to the `globex-egress` replicas, which chain it to the gateway from the registered address. The gateway identifies Globex by that address.
- Globex's gateway applies its own policy and can intercept TLS. Globex's pods then need the gateway's CA, which the pool's CA trust bundle provides.
- Tenant `acme` on the same pool goes direct.

**A self-hosted GitLab on a private address.** The platform admin registers git endpoint `initech-gitlab` (`https://gitlab.initech.internal`, on a private address with a Helm `ipBlock`) and grants it to tenant `initech`. Initech adds a GitLab credential pool and a git route.

- Pods clone and push through `netagent`, which reaches the endpoint as a raw tunnel through `lenny-egress` to the pinned address.
- Tenant `acme`'s pods cannot reach it. Acme has no route to it, `lenny-egress-authz` refuses the host for Acme, and no pod has a network path to the address. Level 1 replaces that last point with a tenant-scoped policy rendered from Helm values.

**A session that lists its hosts.** Tenant `acme` has always-allowed `npm, pypi` and session-requestable `huggingface.co`. Bob's session sets `egress.allowedHosts: [pypi, huggingface.co]`, so it can download models and Python packages, and `npm` is closed to it. Alice's session sets no list and gets the always-allowed `npm, pypi`, so `huggingface.co` gets 403. A session that lists `example.com` is rejected at create, because that host is neither always-allowed nor session-requestable.

**Warm fill and setup before claim.** Pool `monorepo` has the warm-up policy `npm` plus repository `github.com/acme/monorepo` (public). Each warm pod fills its repository store from `lenny-git-mirror` and runs `npm ci` before any session. A tenant-owned pool with a private repository uses the tenant's warm-up additions and the tenant's credentials from the moment the pool is bound to the tenant.

**A coding agent opens a pull request.** Tenant `acme` has a GitHub App credential pool for `github.com/acme/` and a credential binding `acme-github-api`. The binding's hosts are `api.github.com` and `uploads.github.com`, its scope is `always`, its permissions are `pull_requests: write` and `contents: read`, and its delivery is `inject`. Alice's session clones `acme/web` with write scope, and the agent runs `git push origin fix-login` and then `gh pr create`.

- The push mints a `contents: write` token for `acme/web` in `netagent`, as in the first scenario.
- `gh` runs because the profile sets `GH_TOKEN=lenny-injected`. `netagent` terminates TLS for `api.github.com`, mints a token for the session's repositories with the binding's permissions, overwrites `Authorization`, and forwards the request.
- `gh pr create --repo acme/payments` gets 404 from GitHub, because the minted token covers only `acme/web`. The host enforces the limit, so `netagent` needs no knowledge of GitHub's REST paths.
- The agent can read no token in the pod. The SDK accessor shows the binding with its placeholder.

**An agent calls a remote MCP server.** Tenant `globex` stores an API key for a hosted MCP server at `mcp.tracker.example.com`, with the binding `globex-tracker` (scope `onRequest`, delivery `inject`, attach `Authorization: Bearer {{secret}}`). Bob's session names the binding, and the runtime's MCP client connects over streamable HTTP.

- `netagent` attaches the key to each request and streams the server-sent events through the sliding-window scrubber, so tool results arrive as they are sent.
- Carol's session in the same pool does not name the binding. Her requests to the same host carry no key, and the server answers 401.

**A data agent reads S3.** Tenant `initech` creates an IAM OIDC provider for its Lenny issuer (`https://<lenny>/oidc/<initech's tenant ID>`) and a role `initech-analytics` whose trust policy requires Initech's issuer, audience `sts.amazonaws.com`, and subject `lenny:initech:initech-analytics`, with a permissions policy on `s3://initech-analytics/*`. Its binding `initech-analytics` has delivery `federation`, provider template `aws`, and the role ARN. The pool's ceiling includes the `aws-s3` and `aws-sts` host sets. The session runs Python with boto3 and the AWS CLI.

- `netagent` writes the JWT and a config profile naming it, and the profile's `AWS_CONFIG_FILE` points boto3 at the file. boto3 calls `AssumeRoleWithWebIdentity` through `lenny-egress` and receives one-hour credentials for the role.
- SigV4 signing happens in the client, and `lenny-egress` sees CONNECTs to STS and to `initech-analytics.s3.us-east-1.amazonaws.com`.
- The agent can read the JWT. That is why `federation` needs the `agentReadableDelivery` grant and the tenant opt-in. The trust policy limits the token to one role, and the role to one bucket.
- Tenant `globex`, which cannot create an OIDC provider in its AWS account, stores a long-lived access key in a credential pool and uses `metadata` delivery instead. The Token Service mints one-hour session credentials from the key, and `netagent` serves them on the AWS container credentials endpoint.

**A private package registry.** Tenant `initech` mirrors npm and PyPI through `initech.jfrog.io`. It writes two tenant client config files, an `.npmrc` with `registry=https://initech.jfrog.io/artifactory/api/npm/npm/` and a `pip.conf` with the matching `index-url`, neither holding a credential. It adds a binding with hosts `initech.jfrog.io`, `attach: Authorization: Basic {{base64 "svc-agent" ":" secret}}`, and delivery `inject`.

- `npm ci` and `pip install` send unauthenticated requests, and `netagent` attaches Basic authentication on the way out. Round 3 showed both tools installing through this path.
- The tenant's always-allowed hosts list `initech.jfrog.io` and omit `registry.npmjs.org`, so a dependency that bypasses the mirror fails with 403.

**A merge request on self-managed GitLab.** Tenant `initech` uses the private GitLab endpoint from the earlier scenario, with a GitLab project access token that the Token Service mints for each session and revokes at session end. Its binding for the GitLab API uses delivery `inject` with TLS termination.

- The pool's profile sets `GITLAB_HOST=gitlab.initech.internal` and `GITLAB_TOKEN=lenny-injected`. `glab mr create` sends HTTPS through `HTTPS_PROXY` and trusts the per-pod CA through `SSL_CERT_FILE`, and `netagent` overwrites the token header and forwards the request. Round 3 confirmed that `glab` honours all of these variables. The plain-HTTP option (`api_protocol: http`) is not usable here, because it lives in `glab`'s config file, which `glab` accepts only at mode 0600.
- The token is a `mint-coarse` token, wider than one repository. `netagent` path enforcement limits git use to the session's repositories, and the API calls rely on the project token's own scope.

**A chat runtime in service mode calls a tenant API.** Pool `assistant` runs a service-mode runtime (§5.2 **`service`**). Tenant `globex` has a CRM on a private address. The platform admin registers it as a private service with an `ipBlock` and grants it to `globex`, and Globex adds a binding with scope `always`.

- The first request pins a warm pod to `globex`. From then on the pod's policy is per pool and tenant, and every conversation on the pod can reach the CRM with the binding attached.
- A session-level binding is unavailable on this pool, because service-mode pods serve several sessions from one process.

**An air-gapped deployment.** The cluster has no direct internet route. The platform admin sets the platform default route to upstream `corp-proxy` for every tenant, registers internal mirrors as host sets (`npm-internal`, `pypi-internal`, and `git-internal`), and sets pool ceilings to those host sets.

- `lenny-egress` chains every connection to `corp-proxy`. The Envoy SSRF filter and the decision service still apply first.
- The mirror's warm fills come from `git-internal` through the same path.
- The `lenny-egress` NetworkPolicy allows only the proxy's address, so a misconfigured direct route fails closed.

**A single-tenant deployment on an existing Squid.** Acme's policy forbids adding a proxy product, and its Squid cannot call a decision service. The platform admin sets `centralProxy.contract: level1` with the acknowledgement, and a sync job renders the policy document into the Squid configuration every minute.

- Preflight fails until Squid serves TLS and requires credentials, and warm pods do not fill until the post-install probe shows that Squid refuses private destinations.
- Pool `coding` has one identity per tenant. A session's `egress.allowedHosts` narrows what `netagent` allows, and Squid enforces the pool's whole list.
- The platform admin cannot register an upstream proxy or an egress group, and the validators name level 1 as the reason.

**Long LLM streams in direct mode.** Pool `agents` uses `deliveryMode: direct` with `proxied` egress and a ceiling that includes the `anthropic` host set. The runtime streams completions that last up to 40 minutes.

- Each completion is a CONNECT tunnel to the provider through `lenny-egress`, decided and logged per session.
- The pool names the 2-hour entry of the tunnel lifetime menu, above the longest stream. A cap below it would end streams mid-completion, and the client would retry.
- When the session ends, `netagent` closes the session's tunnels at once, before the cap.

## Injected configuration

Tools find the local endpoints through configuration. No formal standard defines proxy configuration for command-line tools:

- `HTTPS_PROXY`, `HTTP_PROXY`, and `NO_PROXY` are a convention that curl started. Most HTTP libraries follow it, with differences in letter case, leading dots, and CIDR support.
- PAC files and WPAD are standards for browsers, and command-line tools ignore them.
- The trust variables (`SSL_CERT_FILE` and the per-ecosystem variables listed under "TLS interception") are conventions of the same kind.

The design layers the configuration so that Lenny maintains only these conventions, and tool-specific knowledge stays with the runtime image or the deployer.

**1. Fixed endpoints.** In every pod, the local egress endpoint is `127.0.0.1:3128`, the local git endpoint is `127.0.0.1:3129`, and the directory `netagent` writes is `/run/netagent`. A deployment can change the ports. Because these values are constants, a runtime image can carry tool configuration that names them, baked in at build time, such as a Maven `settings.xml`. Files baked into the image are available at launch. Image `ENV` is not, because the supervisor starts the runtime from an empty environment (§4.7.10, **Environment**).

**2. Convention variables at launch.** The controller adds the proxy and trust variables to the runtime's launch environment. In the sidecar model they are supervisor `--env` pairs next to `LENNY_ADAPTER_SOCKET`. In the embedded model they are container env. The values are not secret, so rendering them as arguments is acceptable. A Runtime `env` entry that names one of them is rejected for `proxied` pools. Commands the agent runs inherit the variables from the runtime process, unless the runtime builds an empty environment for them. The default set is:

- `HTTPS_PROXY`, `https_proxy`, `HTTP_PROXY`, `http_proxy`, `ALL_PROXY`, `all_proxy`, `NO_PROXY`, and `no_proxy`. Both cases are set, because curl reads only lowercase `http_proxy`. The controller derives `NO_PROXY` from the base destinations. Today it receives only the gateway gRPC address; the chart therefore gains one Helm value for the LLM proxy host, consumed by both the gateway's `--llm-proxy-public-url` flag (which no template renders today) and the controller, and passes it with the OTLP collector host to the controller as inputs on the same path as `GatewayGRPCAddr`, and the controller renders all three into `NO_PROXY`. On `proxied` pools that value must be an in-cluster Service name: the agent NetworkPolicy admits LLM proxy traffic to gateway pods by selector only, and the `proxied` resolver answers no public name, so a public hairpin URL would fail twice. Preflight checks the value, and the same rule applies to the OTLP host. so platform traffic from the runtime is never sent to `netagent`, where the address filter would refuse it. Presigned object-store hosts are not enumerable and are used by the adapter only, which bypasses the proxy explicitly (see "Runtime SDKs", **Embedded runtimes**). `netagent` receives the same list over I7 and logs a CONNECT for one of those hosts as a configuration error instead of tunnelling it. `NO_PROXY` lists exact, fully qualified names only: .NET and Go read leading dots differently, curl, Node, and Go do not match a short Service name against `.svc`, and Node ignores CIDR entries. `ALL_PROXY` is always `http://`, because a `socks5://` value makes curl resolve names locally.
- Switches that are settings rather than files:
  - `NODE_USE_ENV_PROXY=1`, which needs Node 22.21 or 24.5 and later.
  - `npm_config_proxy`, `npm_config_https_proxy`, and `npm_config_noproxy`.
  - `YARN_HTTP_PROXY` and `YARN_HTTPS_PROXY`. `YARN_NO_PROXY` is never set.
  - `CARGO_HTTP_PROXY` and `CARGO_NET_GIT_FETCH_WITH_CLI=true`.
  - `JAVA_TOOL_OPTIONS` with the JVM proxy properties.
  - `GLAB_CHECK_UPDATE=false` and `GLAB_SEND_TELEMETRY=false`.
- The trust variables, pointing at `/run/netagent/ca-bundle.pem`. That bundle holds the image's public roots, any deployer CA, and the per-pod CA. `NODE_EXTRA_CA_CERTS` points at the added CAs only, and npm needs it because npm ignores `SSL_CERT_FILE`.
- `GIT_CONFIG_SYSTEM=/run/netagent/gitconfig`. Lenny's rules take the system scope, which is read-only by convention, so the agent's `git config --global` keeps working against its writable home and an image-baked `~/.gitconfig` still applies. The rendered file starts with `[include] path = /etc/gitconfig`, which git ignores when the file is absent, so an image that ships a system config (for example with `safe.directory`) keeps it, and Lenny's rules follow it. `NETRC` when a `netrc` projection exists, `LENNY_NETWORK_DIR=/run/netagent`, `LENNY_CREDENTIALS_DIR`, and the `inject` placeholders (for example `GH_TOKEN=lenny-injected`).
- The pointer variables for tenant client config files at their fixed paths: `NPM_CONFIG_USERCONFIG=/run/netagent/config/npmrc` and `PIP_CONFIG_FILE=/run/netagent/config/pip.conf`. They are constants, so they can be launch variables, and a tenant file at another name needs a pointer from the runtime image or a deployer profile.

**3. The network descriptor.** `netagent` writes `/run/netagent/network.json` for runtimes that configure clients in code, such as a headless browser, aiohttp without `trust_env`, or an MCP client. It holds:

- the endpoints and the CA bundle path;
- the `NO_PROXY` list;
- the launch variables of layer 2;
- per session, the credential bindings with their name, hosts, delivery, placeholder, and file path.

`netagent` rewrites the descriptor atomically whenever the binding changes. Session entries are keyed by `sessionId`.

**4. Extensions.** A platform admin replaces or extends the launch variables per deployment, Runtime, or pool, and adds file templates for tools the conventions miss. Tenant client config files (for example an `.npmrc` that names a private registry) are written verbatim per tenant, with no templating. Lenny ships no tool-specific file templates. The runtime author guide documents the gaps round 3 found and how an image or a profile closes them:

- Maven 3.9 and 4.0 ignore the JVM proxy properties, because their default Apache transport does not read them. An image ships `settings.xml` with a `<proxies>` entry naming `127.0.0.1:3128`, and `MAVEN_ARGS=-s <path>` points Maven at it, or the deployer adds the same file as a template.
- Gradle 8 and 9 honour `JAVA_TOOL_OPTIONS`, `GRADLE_OPTS`, and `systemProp.*` lines in `gradle.properties`, for dependencies and for the wrapper download, and need nothing more.
- System-wide files are out of reach, because the root filesystem is read-only and the pod runs as non-root. A runtime that needs `apt` bakes its proxy config into the image.

**What `netagent` writes, in total**: the git config, the CA bundle, the descriptor, the credential directories for `file` bindings and their projections, the token and client configuration files for `federation` bindings, the token file for a `metadata` binding, the profile's file templates, and the tenant's client config files. It writes nothing else.

Tools that ignore every proxy setting fail to connect, because the NetworkPolicy allows no direct route, so they fail closed. The runtime author guide lists the known cases: aiohttp without `trust_env`, and headless browsers without an explicit proxy option.

## Runtime SDKs

### Current state

The runtime SDKs in Go, Python, and TypeScript (`sdks/runtime/`) are protocol codecs. Each implements the following:

- the frame loop and multiplexing by `sessionId`;
- the MCP dial for Standard level;
- CH-RUNTIMEOPS for Full level;
- reading the credential file that `session_start.credentialsPath` names, and rebinding on `credentials_rotated`.

None of them builds an environment, spawns subprocesses, reads proxy variables or a CA bundle, or helps with git. The supervisor builds the runtime's environment at launch, and commands the agent runs inherit it. Basic-level runtimes may implement the protocol without any SDK (§26 preamble).

### Design rule

Every mechanism in this design works with no runtime code:

- the launch variables;
- the git config, named by `GIT_CONFIG_SYSTEM`;
- `inject` in `netagent`;
- `federation` and `metadata` through the cloud SDKs' own provider chains;
- `file` in a fixed directory.

A runtime without an SDK, and a shell command the agent runs, never pass through SDK code. A mechanism that depended on SDK code would fail without notice for them, which would make the design brittle. The SDKs therefore add only conveniences that read what the platform already provides.

### What the SDKs add

The SDKs add one read-only accessor over the network descriptor: `NetworkFrom(ctx)` in Go, `tools.network` in Python, and `ctx.network` in TypeScript. It returns the following:

- The endpoints, the CA bundle path, and the `NO_PROXY` list, for clients built in code.
- `env()`: the launch variables as a map, for a runtime that builds a clean environment for the commands it spawns and must keep the proxy and trust variables in it.
- `bindings()`: the session's credential bindings, with name, hosts, delivery, and placeholder.
- `credential(name)`: the current value of a `file` binding, read from disk on every call so that rotation needs no watcher.

The accessor reads the descriptor entry for the session, and it retries for up to a bounded time when the entry is not yet written. This covers the short gap between the binding and `netagent`'s rewrite. Its size per SDK is a few dozen lines, with tests that pin the descriptor schema.

### What the SDKs do not add

- No proxy, TLS, minting, or token handling. `netagent` owns all of it.
- No tool configuration, template engine, or file watcher.
- No git helpers. `git-credential-lenny` and `lenny/vcs_token` are removed, and git reaches the loopback endpoint through the git config.
- No new frames, channels, or `session_start` fields. The descriptor is a file, and CH-MSGSOCK and CH-RUNTIMEOPS are unchanged.

### Embedded runtimes

The embedded runtime container gains a read-only mount of `/run/netagent` and the launch variables as container env. The design adds no controller-rendered flags, because every embedded binary must define each flag the controller renders or it exits at `flag.Parse` (the comments in `echo-embedded` and `preconnect-echo` record this). An SDK-warm embedded process keeps its environment from pod start, which holds no tenant data, because the variables are constants.

The embedded container also runs the adapter, whose transports read the proxy environment today: `pkg/adapter/checkpointtransport.go` clones `http.DefaultTransport`, which uses `ProxyFromEnvironment`, and the gateway gRPC client sets no proxy option. Presigned object-store URLs carry bucket names as hosts that `NO_PROXY` cannot enumerate. The adapter therefore disables proxying explicitly on every platform transport (`Proxy: nil` on its HTTP transports and `WithNoProxy` on the gateway gRPC client), in both pod models, and a tier 2 test pins that checkpoint uploads and the gateway link reach their endpoints directly under the launch variables.

### Runtimes without an SDK, and commands the agent runs

These read the environment and the files directly, and they need nothing else. A runtime that strips the environment of the commands it spawns must keep the proxy and trust variables. The runtime author guide states this, and the `env()` accessor gives SDK runtimes the list.

### SDK gaps found during the review

The review found these existing gaps, which are outside this design and should be filed as findings:

- §15.7 names the Go module `github.com/lennylabs/runtime-sdk-go` and the Apache-2.0 licence. The tree uses the root-module path and the MIT licence, and the scaffolder templates import the spec's path.
- §15.7 promises SIGTERM handling, a wrapper for the lease-refresh loop, workspace utilities, telemetry, and a test server. None of them exists.
- No SDK populates `CreateRequest.WorkspacePlan`.
- The session `env` that §14 says is "injected into the agent session" has no delivery path to a shared runtime process.
- No SDK helper acts on `llm.apiKeyEnv`.
- The Python scaffold's Dockerfile sets `ENV PATH`, which the supervisor drops at launch.
- `git-credential-lenny` reads the current manifest nonce, so it is fragile on recycled `keep` pods and unusable on concurrent pods. This design removes it.

## Git configuration

The rendered git config is identical for every host:

```
[url "http://127.0.0.1:3129/https/"]
    insteadOf = https://
[url "http://127.0.0.1:3129/http/"]
    insteadOf = http://
[url "http://127.0.0.1:3129/ssh/"]
    insteadOf = ssh://
[url "http://127.0.0.1:3129/scp/"]
    insteadOf = git@
[url "http://127.0.0.1:3129/"]
    insteadOf = http://127.0.0.1:3129/
[credential]
    helper =
[http]
    followRedirects = initial
[transfer]
    fsckObjects = true
```

- **Paths.** The spike observed these request paths for the hosts the owner asked about:
  - `https://github.com/org/repo` became `/https/github.com/org/repo`.
  - `https://git.kernel.org/pub/scm/git/git.git` became `/https/git.kernel.org/pub/scm/git/git.git`.
  - `https://gitlab.example.com/group/sub/repo.git` became `/https/gitlab.example.com/group/sub/repo.git`.
  - `https://host:8443/x.git` became `/https/host:8443/x.git`.
  - `git@github.com:org/repo.git` became `/scp/github.com:org/repo.git`.
  - Submodules (absolute and relative), `git ls-remote`, and `go get` with `GOPROXY=direct` followed the same rewrite.
- **Hosts with no credentials.** A public host such as kernel.org needs no credential binding. `netagent` forwards the fetch through `lenny-egress` (or through the mirror's public store) when the host is in the effective policy. A push to such a host is refused unless a VCS credential pool covers it.
- **SSH remotes.** `netagent` speaks only HTTPS to git hosts, and pods have no SSH egress. The `ssh://` and `git@` rules send SSH-form remotes to `netagent` so that they fail with a clear message instead of a connection timeout. `netagent` maps an SSH remote to HTTPS only when the remote's host matches an `sshForms` entry in the host data, and Lenny code holds no per-provider logic. Round 3 checked the rule "keep the host and path, and switch the scheme" against public hosts:
  - It holds for github.com, gitlab.com (including subgroups), Codeberg, gitea.com, sourcehut, KDE, Debian Salsa, Launchpad, OpenDev Gerrit, and googlesource.com. The shipped host sets declare that identity mapping for those hosts.
  - The host changes for `ssh.github.com:443`, `altssh.gitlab.com:443`, GNOME's and freedesktop's `ssh.gitlab.*` hosts, Bitbucket Cloud (`ssh.bitbucket.org`), and kernel.org pushes (`gitolite.kernel.org`). An `sshForms` entry maps the SSH host to the HTTPS host.
  - The path changes for Azure DevOps (`ssh.dev.azure.com:v3/org/proj/repo` against `dev.azure.com/org/proj/_git/repo`), Bitbucket Data Center (`:7999/PROJ/repo` against `/scm/PROJ/repo`), Gerrit sites with a context path such as Wikimedia's `/r/`, and forges served under a relative URL. An `sshForms` entry with a path template covers these, and a deployer or tenant adds it with the git endpoint registration.
  - For a host with no entry, `netagent` answers the discovery request with a git `ERR` packet. git prints it as `remote error: SSH remotes are not supported for <host>; use the HTTPS clone URL`.
  - The `origin` remote that materialization writes is always the HTTPS URL, so SSH forms appear only when an agent types one.
- **Remotes the rewrite misses.** SCP remotes with a user other than `git`, and `git://` remotes, match no rule. git then tries a direct connection, which the NetworkPolicy blocks, so those remotes fail closed.
- **`pushurl` is harmless.** git normally pushes to the fetch URL after applying any `pushInsteadOf`. When a remote has an explicit `pushurl`, git skips `pushInsteadOf` for it and applies `insteadOf`. That difference matters only when fetch and push go to different places. With a single `insteadOf` rule, fetch, push, and any `pushurl` all reach `netagent`, which tells them apart by the service name. A `pushurl` naming another host also arrives at `netagent`, where the policy refuses it. The design does not use `pushInsteadOf`, also because git-lfs and git disagree on applying it when a `pushurl` is set.
- **Overrides.** An agent can override the rewrite with repository-local config or `git -c`, and send git directly through `HTTPS_PROXY` to an allowed host. That request carries no credentials, so it reaches public repositories on hosts the policy already allows.
- **`origin` remote.** Clones that the gateway produces have no `origin` remote today (`gitref.Clone`). The materialization step adds `origin` with the upstream URL, so push works without agent configuration.

## Pod models

| Pod model                                         | Identity before claim | Policy                                                                              | Notes                                                                                                                                                                                                                                                                 |
| :------------------------------------------------ | :-------------------- | :---------------------------------------------------------------------------------- | :-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Pod-warm (default)                                | Service account token | Pool warm-up policy, then the session policy after claim                            | Warm fills and shared assets use the warm-up policy.                                                                                                                                                                                                                  |
| SDK-warm (`preConnect`)                           | Service account token | Pool warm-up policy, then the session policy after claim                            | The runtime process starts at warm-up, before any tenant, when the pool's SDK-warm circuit breaker is closed, and continues into the session (§4.7.10 **SDK-warm.**); a demoted pod launches a new process at session start instead. Its env is fixed at warm-up, which is why tenant-specific settings arrive as files. `netagent` closes its pre-claim tunnels when the session binds. Warm-up requests are expected to complete before the pod reports READY, so an in-flight warm request at bind is an error the runtime author guide documents. |
| Service mode                                      | Service account token | Pool warm-up policy until the first request pins a tenant, then per pool and tenant | Service mode has no claim and no credential lease (§5.2 **`service`**). The tenant pin is the binding. Messages from several sessions reach the same process, so per-session policy is unavailable.                                                                   |
| One session per pod                               | Service account token | Per session                                                                         |                                                                                                                                                                                                                                                                       |
| Sequential reuse (recycle)                        | Service account token | Per session. Between sessions, the pinned idle pod uses the tenant warm-up policy.  | `netagent` closes the previous session's tunnels when the binding changes.                                                                                                                                                                                            |
| Concurrent slots (`maxConcurrentSessions > 1`)    | Service account token | Per pool and tenant                                                                 | Sessions share one process and network namespace.                                                                                                                                                                                                                     |
| Tenant-owned pool (0111 private repository store) | Service account token | Tenant warm-up policy from warm-up                                                  | The pool's ownership binds every warm pod to the tenant before any session. The controller stamps `lenny.dev/tenant-id` with the owner at pod creation, which needs a new `unset → owner` rule for the controller in the label webhook (§5.2, staged in proposal 2), so egress-group admission and the tenant warm-up additions apply from the first second. `ownerTenantId` can be set only when the pool is created and is immutable afterwards: adding an owner to a running pool would leave warm pods with no owner label, and a change would leave existing pods labelled with the old owner, which the gateway's re-stamp cannot correct and `tenantpin` would refuse. Making a pool tenant-owned or changing its owner therefore means creating a pool under a new name and draining the old one. A deleted pool's name is not reusable, because `sandbox_warm_pools.name` is the primary key and deletion is soft, and that also keeps a claim, which selects by pool name, from admitting an old pod. Pool deletion cascades to its overlays, grants, and level 1 identities, and `AdmitTenantPin` refuses an unlabelled pod on a tenant-owned pool. |
| Embedded adapter                                  | Service account token | Same as the sidecar model with the same session model                               | `netagent` is a separate container, so P3 holds.                                                                                                                                                                                                                      |

The first revision stated that pods are tenant-agnostic until claim. That holds only for unbound warm pods. Pinned idle pods, service-mode pods after the first request, and tenant-owned pools are bound to a tenant before a session exists, and the policy table covers each case.

## Network policy and placement

A new `egressProfile: proxied` value allows the base destinations (gateway, object store, and OTLP when configured), the `proxied` resolver, the `lenny-egress` pods, the `lenny-egress-authz` pods (I7), the Token Service's I8 port, and the `lenny-git-mirror` pods, selected by pod label. A deployer git service running in the cluster gets no pod-side rule, and pods reach it through the central proxy like any other private destination, because a label-selected rule would be a raw path for every tenant. `restricted` with `gitAccess` and `internet` with `netagent` add the `lenny-egress-authz` and Token Service rules for the same reason. Rules that match a Service IP do not work. The controller labels pods at creation, the gateway stamps `lenny.dev/tenant-id` at the first slot reservation (`podclaim/slotclaimer.go`), the controller stamps it with the owner at creation on tenant-owned pools, and Helm pre-creates every policy (§13.2.6), including the egress-group policies below. The profile is tenant-agnostic, and it needs no isolation profile beyond what `restricted` needs, because pods reach only Lenny services. The `lenny-label-immutability` webhook gains the new value.

`lenny-egress`, `lenny-egress-authz`, and `lenny-git-mirror` run centrally in `lenny-system`. A per-node DaemonSet is rejected for these reasons:

- The mirror holds private repository contents for several tenants, and the decision service holds the policy for all of them. A container escape on an agent node would reach them on every node that runs agent pods, and T4 nodes host several T4 tenants (§6.4).
- Kata nodes carry the `lenny.dev/isolation=kata:NoSchedule` taint, which keeps non-Kata workloads off them (§17.2, **Node isolation**). A runc DaemonSet needs an exception, and a Kata DaemonSet costs a microVM per node.
- A DaemonSet reserves capacity on every agent node, which lowers warm-pod density. Its local outage drops egress for that node with no failover, and its rolling updates break in-flight tunnels on every node.
- Corporate proxies are central, so a central default keeps replacement a matter of changing an address.

The specification defines no system node pool. The proposal adds a placement rule that keeps these components off nodes tainted for agent isolation, plus §13.2.4 `lenny-system` NetworkPolicy rows for them.

Native sidecars count toward the pod's effective request (the sum of the sidecars plus the largest of the regular containers is not used; a sidecar with `restartPolicy: Always` adds its request to the pod's), so a `netagent` request of, for example, 50m and 64Mi raises each pod's footprint by that amount and lowers warm density accordingly. The §6.4 memory bound accounts for it.

The CNIs checked match pod-to-ClusterIP traffic against the backend pod: Calico (Tigera documentation), GKE Dataplane V2, Cilium, and kube-network-policies. AWS VPC CNI does so by translating Service selectors. AWS VPC CNI also needs `NETWORK_POLICY_ENFORCING_MODE=strict`, or a new pod starts with no restrictions. The requirement already holds for `restricted`, but nothing checks it today: §13.2.1 recommends Calico in policy-only mode on VPC CNI, and `pkg/preflight` has no VPC CNI check. Preflight gains one.

Round 3 confirmed from source that Calico's eBPF dataplane and Cilium evaluate egress policy against the post-NAT backend (`felix/bpf-gpl/tc.c` and `bpf/bpf_lxc.c`). On kind, kindnet enforced a `podSelector` egress policy through a ClusterIP and blocked other Services, pod IPs, the default CoreDNS, and the internet. It did not block the API server or the kubelet on the pod's own node, because traffic delivered to the local node bypasses its hook, and its queue rule fails open when its userspace program is absent. kindnet is a test CNI. The Lenny kind tiers must not treat it as evidence of production enforcement, and the conformance tests for `proxied` run against Calico or Cilium.

Round 5 ran the `proxied` policy (default-deny egress plus `podSelector` peers for the proxy, the resolver, and the gateway, with no `ipBlock`) on two-node kind clusters with Calico 3.33 in its iptables and eBPF dataplanes and with Cilium 1.20 with kube-proxy replacement. On every dataplane, all allowed paths worked through ClusterIP Services, including a backend on the client's own node, and every denied destination was dropped: the API server ClusterIP, both nodes' kubelet ports, hostNetwork pods on the same and the other node, a pod in another namespace, a Service fronting that pod (which confirms post-DNAT evaluation), the default kube-dns, and the internet. Calico's iptables dataplane evaluates workload egress policy on the INPUT path as well as FORWARD, and Calico eBPF and Cilium attach policy at the pod's interface, which is why they catch node-local traffic that kindnet's postrouting hook misses. Two ordering points from that run carry into the chart: the `proxied` resolver's ClusterIP must be known when the controller renders a pod's `dnsConfig`, so the chart fixes the Service's address; and the policies were applied to a running client, so the tier 5 tests also cover a pod created before its policies exist.

## Defaults, replacement, and augmentation

Every default can be replaced, and the egress and git defaults can also be augmented.

| Mode                    | What runs                                                                                                              | What the deployer configures                                                                                                                |
| :---------------------- | :--------------------------------------------------------------------------------------------------------------------- | :------------------------------------------------------------------------------------------------------------------------------------------ |
| Default                 | `netagent`, `lenny-egress` (Envoy), `lenny-egress-authz`, `lenny-git-mirror`, and the shipped host sets                | `egressProfile: proxied` on pools                                                                                                           |
| Augment egress          | The default, with `lenny-egress` chaining selected destinations or tenants to deployer upstream proxies (I6)           | Upstream registrations and grants, `ipBlock` entries for private upstreams, and a stable egress address registered with the upstream vendor |
| Replace egress, level 2 | A deployer proxy that calls I3 (Envoy, Squid with the Lenny helper, or a Smokescreen-based proxy with a Lenny decider), which must dial pinned addresses to serve private destinations | The proxy, its I3 integration, and its address in Helm values                                                                               |
| Replace egress, level 1 (not recommended) | A deployer proxy with no Lenny integration, which enforces a published host list per identity. `netagent` and `lenny-egress-authz` still run, and Envoy does not. | The proxy, a sync job for the policy document, the proxy contract, and the acknowledgement described under "Level 1 replacement" |
| Augment git             | `lenny-git-mirror` for some hosts and deployer git services for others                                                 | Git endpoint registrations and tenant git routes                                                                                            |
| Replace git             | No mirror. Each host is reached through a deployer git service or directly through egress.                             | Git endpoint registrations, or none                                                                                                         |

Corporate security gateways fit the augment mode better than replacement. None of the gateways surveyed documents per-workload proxy Basic authentication. Round 3 also found that none documents reading identity headers from a downstream proxy on an undecrypted CONNECT:

- Netskope requires the downstream proxy to insert `X-Forwarded-For` or `X-Authenticated-User` inside the decrypted TLS session.
- Prisma Access accepts `X-Authenticated-User` only from a trusted source address and needs decryption for header identity. Without decryption, it maps users by known source address.
- Forcepoint reads `X-Authenticated-User` from a downstream proxy, which must decrypt HTTPS.
- Cisco Umbrella maps `X-Forwarded-For` to network identities and registers the downstream proxy's egress address. No user header is documented.
- Zscaler uses `X-Forwarded-For` for sub-locations, and no documentation of reading a downstream `X-Authenticated-User` was found. Its help pages could not be read directly, so this rests on search snippets.
- Cloudflare Gateway proxy endpoints identify by source address or by an Access login with TLS inspection.
- Broadcom Cloud SWG and Skyhigh have no usable documentation on this point.

`lenny-egress` does not decrypt, so the documented identity that works without decryption is the source address. The design therefore identifies tenants to a corporate gateway by **egress groups**:

- An egress group is an additional `lenny-egress` Deployment that the chart renders with the same configuration and the same decision service, placed so that its traffic leaves the cluster from a dedicated, stable address. The platform provides the address with cloud NAT, a CNI egress gateway (Cilium, Calico, or OpenShift EgressIP), or a dedicated node pool. There is no second proxy tier and no new contract: the group's Envoy is `lenny-egress` under another Service name.
- Groups and their grants are Helm values, because a group is infrastructure. From the same values the chart renders the group's Deployment and Service, an ingress NetworkPolicy on the group's pods that admits only pods labelled `lenny.dev/tenant-id` in the granted set (the label the gateway stamps at bind), and the decision service's grant table. No Lenny component writes NetworkPolicies, and the `proxied` egress rule keeps selecting every `lenny-egress` pod, so the group's ingress policy is what restricts it at the network layer.
- Each group's bootstrap adds a fixed `x-lenny-egress-group` header to every ext_authz check, and the decision service refuses a mismatch in both directions: a pod of an ungranted tenant at a group's Envoy, and a pod of a granted tenant at the default Envoy. Without the second refusal, a tenant could leave the cluster from another tenant's registered address, which every service that trusts that address (a corporate gateway's trusted location, an address-allowlisted service, or an `aws:SourceIp` condition) would read as that tenant.
- The group's ingress policy also admits the gateway and mirror pods, so the gateway's `ls-refs` requests and the mirror's fetches for a granted tenant can use the group, as the decision requires of them. Warm pods of a shared pool carry no tenant label and use the default Envoy under the pool warm-up policy; warm pods of a tenant-owned pool carry the owner label from creation.
- A grant change is a Helm change and a rollout. Pods already bound learn the new proxy at the next bounded re-read of their binding, and tunnels open to the old group run to the pool's cap, which is a stated residual.
- The platform admin registers the group's address with the gateway as a location or a trusted proxy, and grants the group to one or more tenants.
- A tenant granted a group is pinned to it: the binding that `netagent` receives on I7 names the proxy Service to dial, and every CONNECT of that tenant's pods goes to the group's Envoy, whose decisions route some hosts to the corporate gateway and the rest directly, all from the group's address. The group's NetworkPolicy label admits only the pods of granted tenants' pools once they are bound, and the decision service refuses a pod that dials a group its tenant does not hold.
- `X-Authenticated-User` and `X-Forwarded-For` remain available on I6 for gateways that read them on CONNECT, and for a deployer whose gateway decrypts.

Per-tenant identity at the gateway therefore costs one egress address per identity the gateway must distinguish.

Level 2 and level 1 differ in what a component outside the pod can enforce:

- **Level 2** gives per-session policy, revocation at the proxy, and decision-level audit, through a protocol (ext_authz) that Envoy, Squid with a helper, and a Smokescreen-based proxy can all speak.
- **Level 1** gives a host list per identity at the proxy, and everything finer is enforced by `netagent` in the pod. The next section states what that gives up.

Cloud FQDN firewalls identify clients by source address or node identity, so they cannot implement either level. They serve as perimeter controls behind `lenny-egress`.

### Level 1 replacement

Level 1 lets a deployer use a proxy that cannot call the decision service. The proxy authenticates each connection with a static credential for an identity and enforces a host list that Lenny publishes, and Envoy does not run. It exists for deployers whose policy forbids running Envoy, or whose proxy cannot call the decision API and cannot sit behind `lenny-egress`. Lenny does not recommend it. Level 2, or chaining the deployer's proxy behind `lenny-egress` (I6), keeps the guarantees listed under **What level 1 gives up**.

**Selecting it.** The choice is deployment-wide: `centralProxy.contract: level1` with `centralProxy.level1.acknowledgeLimitations: true`, and the chart refuses to render without the second value. Every tenant in the deployment is affected, so the effective-policy API reports `enforcement: level1` and the tenant admin can see the weaker enforcement.

**Identities.** Each identity has a name, a host list, and credentials:

- One identity per pool and tenant, whose list is the pool ceiling intersected with the tenant's egress policy, including any host a session may add, the tenant's warm-up additions, and a tenant-owned pool's warm-up overlay.
- One warm identity per pool, whose list is the pool warm-up policy.
- One service identity per tenant for the gateway resolver and the mirror's fetches, whose list is the tenant's VCS pool hosts and public git routes.
- One public identity for the mirror's public stores, whose list is the public git hosts the platform default route allows.

The warm and public identities span tenants, and they carry no tenant credential or tenant data. Every identity that carries tenant traffic belongs to one tenant, which closes the cross-tenant shared secret the revision 4 review found. The number of identities is the number of pools times the tenants that can use them, and the document and metrics report it. Identity names are generated from opaque identifiers and contain no tenant or pool string.

The Token Service derives the identity from the pod's state, and a request never names one:

| Pod state | Identity released |
| :-- | :-- |
| Warm pod of a shared pool that holds no tenant pin | The pool's warm identity |
| Warm pod of a tenant-owned pool | The pool-and-owner identity |
| Provisional binding (`materializing`) | The pool-and-tenant identity, which the decision call limits to the session's `gitClone` hosts |
| Bound session, pinned idle pod, recycled pod (§5.2 keeps its pin), or service-mode pod after its pin | The pool-and-tenant identity |

The gateway obtains service and public identities from the Token Service on its existing lease path, and the mirror on I8 with its client certificate.

**Credentials.** The Token Service generates a 256-bit random credential for each identity and rotates it every 24 hours with a 1-hour overlap, both as Helm values, and an admin API call rotates one at once. `netagent` obtains the credential on I8 after both gates, holds it in memory only, and flushes it on a binding change, so the agent UID cannot read it. Each generation has its own username (`<identity>.g<n>`). Round 6 showed that stock Squid's Basic helper keeps one verifier per username, so two passwords on one name cannot overlap, and that a retired generation is refused at once, despite a 7,200 s credential cache, because the access rule names the username. A new generation is published in the document and released to pods only after the contract's apply window has passed, so a healthy proxy already knows it. A `netagent` that gets a 407 refreshes its credential once and retries.

**Enforcement.** Two layers apply, and `netagent` holds the finer one:

- For each CONNECT, `netagent` asks the decision service on I7 and dials the proxy only on an allow. The decision uses the same effective policy, session host lists, quotas, and audit as the other levels. I3 stays unreachable from pods.
- The proxy then enforces the identity's host list.
- The gateway resolver and the mirror make the same decision call on I3, carrying the tenant, and then use the tenant's service identity.
- `netagent` enforces the pool's tunnel lifetime, closes a session's tunnels when its binding ends, and reports tunnel opens and closes on I7, which gives the decision service per-tenant concurrency.
- `netagent` reads the ClientHello of each tunnel and closes the tunnel when its server name differs from the CONNECT authority, under the same per-host-set setting as the default proxy's check, because stock Squid lets a prompt-injected agent front through a listed host without any compromise. A client using Encrypted Client Hello remains the documented limit.
- At level 1, a CONNECT the decision allowed and the proxy then refuses (403 or 407) means sync lag or a contract failure. It is a retryable error with an alert, and it maps to `GIT_CLONE_REF_RESOLVE_TRANSIENT` for the resolver. A CONNECT the decision denied maps to `egress_denied`.

**The policy document (I4).** The gateway admin API serves it to a platform role that holds only this read. It carries `version`, `expiresAt` (1 hour after generation by default), and for each identity `allowAll`, `hosts`, `denyHosts`, ports, and each live generation's username and SHA-crypt verifier. `allowAll` with `denyHosts` represents an open ceiling, and a proxy applies the denials first. Hosts come from tenant policies, host sets, VCS prefixes, and routes, and a sync job renders them into proxy configuration, so a newline or proxy syntax in a host would inject rules that widen reach for every tenant. A strict grammar therefore applies on write and again on document generation: lower-case DNS labels with an optional leading `*.`, at most 253 characters, and a final label that is neither numeric nor `0x`-prefixed, which excludes address literals such as `169.254.169.254`, `2130706433`, and `0x7f.1`. Lenny ships a reference renderer for Squid; round 6 used about 40 lines of Python, and a policy edit reached new CONNECTs 0.21 s after `squid -k reconfigure`.

A separate per-identity endpoint serves the hosts alone in External Dynamic List format to the same role, with no verifiers. EDL is a perimeter feed for firewalls: an EDL consumer has no credentials or expiry and cannot meet the proxy contract, so EDL alone does not make a proxy a level 1 proxy.

**The proxy contract.** A level 1 proxy must:

- serve its proxy listener over TLS with a certificate `netagent` verifies, because the shared credential works from any source and must not cross the network in cleartext;
- accept HTTP CONNECT to TLS ports with HTTP Basic authentication by generation username, and refuse a connection with no valid credential;
- apply each new document within 60 seconds, and refuse all traffic once the document it holds has passed `expiresAt`;
- refuse any destination that resolves to a private, loopback, link-local, metadata, cluster, IPv4-mapped, or NAT64 address, with the list `lenny-egress` uses;
- keep `Proxy-Authorization` from reaching any upstream; and
- log the identity and the host of every connection.

A proxy that identifies clients by source address alone does not meet this contract, because any process in the pod could then use it with no credential, bypassing `netagent`. The checks below refuse it, and a corporate gateway of that kind belongs behind `lenny-egress` as an upstream.

Preflight runs as a pre-install hook, before any gateway can serve the document to a sync job, so it checks only what needs no identity: TLS against the configured CA, and a 407 for no credential and for an invented one. After install, `lenny-ops` runs the full probe through probe identities, once at start and then periodically. `lenny-ops` accepts no cluster-internal traffic (§25.4), so it writes the result to a `lenny-level1-probe` ConfigMap, which the controller reads with a new RBAC row. The controller does not fill warm pods on `proxied` pools until the ConfigMap shows a pass, and a later failure raises an alert without draining pools. `lenny-ops` obtains its probe credentials from the Token Service on I8 with its client certificate, so its egress row gains the proxy address and the I8 port, and the Token Service's ingress row on I8 gains `lenny-ops`, on both sides as NET-051 requires. The full probe checks:

- **Authentication.** No credential and a wrong password must return 407. A retired generation must be refused; stock Squid answers it with a 403 from its access rule.
- **Positive control.** The probe identity reaches an allowed public host, so a proxy that refuses everything fails.
- **Separation.** A probe identity gets a 403 for a host only another probe identity lists.
- **Address rule.** A separate probe identity lists `127.0.0.1`, `169.254.169.254`, and a deployer-supplied name that resolves privately. Each must be refused before any connection is attempted. A 503 from stock Squid means it attempted the connection, so it fails. The grammar's exclusion of literals does not apply to this identity alone.
- **Sync health.** A canary generation retired, and a canary host added, within the last apply window must already be in effect, so a dead sync job is detected.

The probe samples the contract and cannot prove it. Expiry in particular is enforced by the same sync job whose failure it guards against, and stock Squid has no expiry primitive, so the canary is what detects a stale proxy.

**Refused configuration.** Validators refuse the settings that need a decision at the proxy: upstream chaining (I6), egress groups, private destinations reached through the proxy, and tenant-registered upstream proxies.

**Private destinations.** A level 1 proxy must refuse private addresses and no Envoy exists to tunnel to a pinned address, so a private destination, such as a self-hosted git service or a tenant's CRM, needs a network path of its own:

- These registrations and grants are Helm values at level 1, as egress groups are. From them the chart renders the decision service's grant table and a NetworkPolicy that admits pods labelled `lenny.dev/tenant-id` in the granted set to that address. A revocation is a Helm change and a rollout. Pods of other tenants are dropped, and a warm pod of a shared pool carries no label and cannot reach it.
- `netagent` dials the address the decision returns directly, on the local git endpoint and on the egress endpoint alike, so `inject`, LFS transfers, and API tools such as `glab` work on a private host. The decision call on I7 still applies to each connection.
- The gateway resolver and the mirror dial the endpoint directly after their I3 call, through `ipBlock` rows the chart renders for them. Each checks the decision's address against its own read of the registration, so a compromised decision service cannot send a read token to another registered address.
- A tenant-registered git endpoint on a public address goes through the proxy, which loses the decision's address pinning, so its SSRF protection rests on the deployer's address rule.

**Network rules.** The `proxied` policy gains the level 1 proxy's address as an `ipBlock` peer on its port, in place of the `lenny-egress` selector, and the gateway's, the mirror's, and `lenny-ops`'s `lenny-system` rows gain the same peer.

**What level 1 gives up.**

| Guarantee | Default and level 2 | Level 1 |
| :-- | :-- | :-- |
| Destination enforcement outside the pod | Every CONNECT is decided per session at the proxy. | The proxy enforces the identity's list only. Session host lists and binding state are enforced in the pod, so a compromised `netagent` reaches the whole list. |
| Proxy credential | A pod-bound service account token that lasts 600 s, checked against the live pod and its peer address. | A static credential shared by every pod of the identity. Round 6 confirmed it works from any container that can reach the proxy, until it rotates: up to 25 hours while the sync job is healthy, and until the canary alert is acted on when it is not. |
| Revocation | The proxy refuses the next CONNECT when a binding ends. | `netagent` stops, and the proxy keeps accepting the identity. A tunnel open before a list edit stayed open in round 6. |
| Tunnel lifetime | Envoy's `max_stream_duration` caps every tunnel, whatever the pod does. | `netagent` enforces the cap, so a compromised `netagent` has no outside limit unless the proxy adds one. `client_lifetime` did not end active tunnels on Squid. |
| SSRF protection | The Envoy address filter, pinned by the conformance suite. | The deployer's configuration. Stock Squid reached a listed name that resolved privately until the rule was added. The probe samples this. |
| Server-name check | Enforced by the default proxy, outside the pod. | Enforced by `netagent`, so a compromised `netagent` bypasses it. Stock Squid alone passed a CONNECT to a listed host with TLS naming an unlisted one in round 6. A deployer can add Squid's own peek check. |
| Audit and attribution (P5) | The decision and the connection are recorded at the proxy, per session. | Decision records come from `netagent`'s calls, and the proxy's log names an identity (pool and tenant). A compromised `netagent`'s connections appear in the proxy log with no session. |
| Quotas (P7) | Rates at the decision service, and per-tenant concurrency from the access log stream. | Rates at the decision service, per-pod tunnels in `netagent`, and per-tenant concurrency from `netagent`'s reports, which a compromised `netagent` can understate. The shared limits are the deployer's. |
| Private destinations | A pod has no network path to one. Envoy tunnels to the address the decision pins, for a granted tenant. | A granted tenant's pods have a network path, so a process at the agent UID can open a raw socket to the endpoint with no credential, skipping `netagent`, the decision, audit, and quotas. A mirror subprocess under a hostile pack has the same reach to every registered endpoint, across tenants. |
| Per-tenant routing | Upstream proxies, egress groups, and private destinations per tenant. | One route per deployment, which is the proxy's own. |
| Policy freshness | The next decision. | The sync interval at the proxy, plus the document's expiry. An EDL consumer has no expiry. |
| Corporate gateways | Source-address identity through egress groups. | Unavailable. No surveyed gateway documents per-workload Basic authentication, so these gateways do not meet the contract. They work behind `lenny-egress` through chaining. |
| Warm and public traffic | Decided per pod at the proxy. | The warm and public identities span tenants, so their lists are the reach of any holder. |

P6 holds for unavailability: if the proxy, the decision service, or the credential is unavailable, `netagent` fails closed. It does not hold for a stale proxy, which keeps enforcing its last list and accepting its last credentials until the sync job recovers.

**Why it is not recommended.** Level 1 moves the per-session boundary into the pod, and it replaces a credential bound to one pod with one shared across every pod of an identity. Compromise of one `netagent` gives reach across that pool's list for that tenant for up to 25 hours, from any source that can reach the proxy, whereas the default bounds it to that pod's current policy for the token's life. A dead sync job extends that until an operator acts on the alert. It also makes SSRF protection, which protects the cluster and every tenant, depend on deployer configuration Lenny can only sample. Level 1 suits a single-tenant deployment, or tenants that trust each other, on a proxy that cannot do better.

## TLS interception

Without TLS interception, `lenny-egress` filters by hostname only. A deployer proxy can intercept TLS: corporate gateways do, and so do Squid with `ssl_bump` and mitmproxy. Interception adds path filtering, data loss prevention, and inspection, so a deployer who needs those can close that gap. Lenny's part is distributing the deployer's CA so that tools in the pod trust it:

- **Bundle.** The controller assembles a combined PEM bundle per deployment or pool from a public root set the chart ships (the `netagent` image cannot read another container's `/etc/ssl`) and the deployer CA, and mounts it read-only into `netagent`. `netagent` appends only its own name-constrained per-pod CA and writes the result to `/run/netagent/ca-bundle.pem`, so a compromised `netagent` cannot add an unconstrained root. The PKCS12 truststore for the JVM holds the whole bundle, because `javax.net.ssl.trustStore` replaces `cacerts`. The deployer CA must satisfy Python 3.13's strict X.509 checks: it needs an Authority Key Identifier and RFC 5280 conformance.
- **Environment variables.** The client proxy profile points these at the bundle: `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, `PIP_CERT`, `GIT_SSL_CAINFO`, `CARGO_HTTP_CAINFO`, and `AWS_CA_BUNDLE`. `NODE_EXTRA_CA_CERTS` is set to the deployer CA alone. `JAVA_TOOL_OPTIONS` points the JVM at a PKCS12 truststore. `SSL_CERT_DIR` alone is never set, because .NET and OpenSSL replace their search path with it.
- **Scope.** Node and the JVM read their trust settings at process start, and SDK-warm processes start before any tenant, so the trust bundle is per deployment or pool. It cannot vary per tenant. A pool that serves a tenant behind an intercepting gateway carries that gateway's CA.
- **Breakage.** Tools with compiled-in roots break under interception and are listed as unsupported: rustls with webpki-roots, certificate-pinning tools, and some Electron apps.

## Residual risks and limits

- **Exfiltration.** Hostname filtering limits exfiltration but does not prevent it. Any allowed host that accepts uploads remains a channel: a package registry's publish endpoint, a git host, or an issue tracker. The documentation states this for each shipped host set. A `*` ceiling allows port 443 only unless the pool names other ports.
- **Logging.** Neither Envoy's access log nor `netagent`'s log records `Proxy-Authorization`, injected headers, or decrypted bodies. The conformance tests pin the log formats.
- **Node versions.** `NODE_USE_ENV_PROXY` exists from Node 22.21 and 24.5. An image on an older Node fails fast on every fetch with ENOTFOUND, and the runtime author guide states the floor.
- **SNI and domain fronting.** Without a server-name check, the proxy cannot confirm that a tunnel's TLS server name matches its CONNECT authority. An agent can CONNECT to an allowed host and then send a ClientHello naming another host on the same CDN, which domain fronting exploits on CDNs that do not enforce matching names.
  - **Mechanism.** Envoy's CONNECT termination hands the tunnel's bytes to a TCP proxy. Listener filters such as `tls_inspector`, which read the ClientHello, run only on new downstream connections. The check therefore routes the tunnel's bytes into an internal listener (`envoy.bootstrap.internal_listener`), a listener inside the same Envoy process that receives a connection from another Envoy filter rather than from the network. The CONNECT route passes the expected host to it through shared filter state. The internal listener runs `tls_inspector`, then an RBAC filter whose CEL condition compares the server name with the expected host, and then forwards with `sni_dynamic_forward_proxy` and `tcp_proxy`.
  - **Round 3 result.** The check works at about 175 lines of YAML. A matching server name passes. A different name, a missing name, or an IP-literal authority is refused. The cost is about 0.2 ms at p50 and 15 to 25% more Envoy CPU per tunnel.
  - **Caveats.** The decision service lower-cases the authority, because Envoy does not. Non-TLS payloads close or pass by policy, and server-first protocols wait for `listener_filters_timeout`, which defaults to 15 s. Missing headers render as `-` in `set_filter_state` until `omit_empty_values: true` is set. On this route an SSRF refusal arrives as 200 followed by EOF rather than 503. Encrypted Client Hello defeats the check when the client sets the outer public name to the authority: the server received a different inner name.
  - **Status.** Revision 2 listed the check as later work because it had not been built. Revision 3 places it in proposal 2 as a per-host-set setting, on by default for TLS host sets and off for hosts that carry non-TLS protocols. Squid can make the same check by peeking without decrypting.
- **DNS channel.**
  - Today one dedicated agent CoreDNS forwards every name upstream. An agent can encode data in queries for a domain it controls, and the attacker's DNS server receives them. The rate limit (10 responses per second per source) and the TXT size filter slow this, but A and AAAA queries still carry data.
  - Pods in `proxied` pools have no need for external names, because clients send the hostname inside CONNECT. The second spike confirmed this for Go, Node's undici, .NET, and CPython. Java's `URL.equals` resolves names, but it falls back to comparing strings when resolution fails.
  - The fix is a second CoreDNS Deployment for `proxied` pods. It answers cluster names and returns NXDOMAIN immediately for everything else. Dropping queries would stall Java.
  - The controller gives `proxied` pods that instance's address as their nameserver. That needs a per-profile DNS address, since today one address applies to every pod. The `proxied` NetworkPolicy allows only that instance.
  - The cost is one small Deployment plus a controller change for the per-profile address. Revision 2 listed it as later work because the existing `restricted` pods carry the same DNS channel today, so the gap is not new. The channel matters more once `proxied` pods run agents with open web access, and round 3 found no client that needs external names behind the proxy, so revision 3 moves the resolver into proposal 2.
  - Round 3 ran it on kind. Cluster names resolved in 0.3 to 4 ms, external names returned NXDOMAIN in about 1 ms after walking the search list, and curl, Python requests and pip, Node 24 fetch, and Go reached public HTTPS sites through the proxy with no local lookup of the target name. Node without `NODE_USE_ENV_PROXY` failed fast with ENOTFOUND rather than stalling.
- **Level 1 replacement.** See "Level 1 replacement", **What level 1 gives up**.
- **Stored-token push.** See "Credentials".
- **Tree validation.** Pod-side git skips the gateway's tree validation, which happens at materialization:
  - Today the gateway clones a `gitClone` source itself, packs the tree into a tar, decodes it inside the gateway through the archive gate (§7.1 step 12, §13.4, `gatedExtract`), and streams the files into the pod. The gate rejects absolute paths, `..`, excessive depth and length, hardlinks, device files, FIFOs, and sockets. It confines symlink targets to the slot and away from `/proc`, `/sys`, `/dev`, and `/run/lenny`. It strips setuid, setgid, and sticky bits, and it bounds total size, entry size, entry count, and compression ratio.
  - When the pod fetches with git, most of these threats cannot arise, because git trees hold only regular files with mode 644 or 755, directories, symlinks, and submodule links. git's checkout always rejects `.git` entries in any spelling, `..`, and writes through symlinks. With `transfer.fsckObjects=true`, fetch also rejects malformed trees and unsafe `.gitmodules`.
  - Two checks remain uncovered. git checks out symlinks that point outside the tree as written. And nothing bounds a repository's size before checkout.
  - The design closes both. A component that runs as the adapter or `netagent` UID never follows workspace symlinks (checkpoint and export already re-validate them). `netagent` and the mirror cap fetch size per pool. §4.7 **Hard workspace size limit.** also requires `emptyDir.sizeLimit` on the slot volume, but `podspec/volumes.go` sets none today, so the kubelet does not bound a fetch the agent runs. That gap is filed as a finding and closed in proposal 2, because the design relies on it.
  - The §13.4 exception states this mapping. When the agent UID runs git itself, the result is equivalent to files the agent could create at runtime.

## Specification sections affected

- §2 Goals and Non-Goals: "gateway-mediated file delivery", "Git-based or object-store-based workspace population by pods", and "no standing credentials in pods", which the gated opt-ins qualify.
- §4.7.1 and §28.5.1: the materialization RPC carries a `gitClone` source with its resolved SHA to the pod instead of streamed files.
- §4.7: the `netagent` container, the local endpoints, `/run/netagent` and the network descriptor, the launch variables in the supervisor environment, and the removal of `git-credential-lenny`.
- §4.9: provider capability levels, minting, token caching, repository routing, and credential bindings with their delivery modes.
- §10.2: the `tenant_id` pattern narrows to a valid Kubernetes label value, at most 63 characters with alphanumeric ends, because egress group and level 1 policies select on `lenny.dev/tenant-id`, the label stamp already fails on a longer identifier, and no deployment depends on the wider form.
- §5.1 and §5.2: the pool egress ceiling, the warm-up policy, `gitAccess`, the client proxy profile, pool ownership, policy granularity per pod model, and the reservation of the launch variable names in the Runtime `env` rule.
- §7.1: steps 11 to 13 (clone in the pod) and 0111's **Bind bounds.**.
- §7.3: the provisional binding during a resume rebuild, and the 30 s fetch wait on that path.
- §6.1 and §6.4: warm-up egress and the warm fill.
- §6.2: the `git_fetch_pending` materialization failure reason and the fetch retry window against the finalize bound.
- §7.5: setup-phase network access, which replaces "network blocked by default during setup" for `proxied` pools.
- §10.2: platform-admin-only `egressProfile` writes on shared pools, and the tenant pool overlay in the "own tenant" scoping note.
- §10.3: the two `netagent` token audiences beside the gateway audience, and the Token Service as a per-tenant OIDC issuer.
- §12.8: purge of tenant stores in the mirror under `DeleteByTenant`.
- §12.9: classification of mirror contents, federation tokens, and credential bindings.
- §13.1: the egress, file delivery, and credentials rows ("No standing credentials" gains the gated exceptions), the `netagent` container identity, the webhook coverage of `netagent`'s volumes, and the mirror container's capability exception.
- §13.2.2: the base policy, which `proxied` and `gitAccess` extend.
- §13.2.5: the exclusion list gains the ranges listed under "Central egress proxy", and the SSRF check on tenant-registered services.
- §13.2.7: the second resolver for `proxied` pods beside the dedicated agent CoreDNS.
- §13.3: credential bindings and federation tokens in the credential flow.
- §13.2.4: `lenny-system` rows for `lenny-egress`, `lenny-egress-authz`, and `lenny-git-mirror`; the Token Service's ingress row gains the agent namespaces on the I8 port, and its egress gains the decision service, the pool and session stores, the API server pod watch over the agent namespaces and `lenny-system`, and the provider APIs.
- §4.9 and §10.3: the Token Service's new reads (binding definitions and session bindings through `pkg/binding`) and its I8 listener beside the gateway-only mTLS listener.
- §13.2.6: the `proxied` profile, the removal of `provider-direct` (NET-006 and NET-044), the DNS resolver for `proxied` pods, and the IPv4-mapped and NAT64 exclusions.
- §13.4: the tree-validation exception for pod-side git.
- §14: the `gitClone` row (pod-side clone, ref resolution through `ls-refs` in Go, and no gateway clone), `writableRepositories`, `egress.allowedHosts`, session credential bindings, the `origin` remote, the reserved `env` names, and the removal of the in-pod credential helper.
- §15.1: the `GIT_CLONE_REF_*` rows keep their meaning with `ls-refs`, and the new egress, binding, and clone error codes.
- §15.7: the network accessor in the runtime SDKs.
- §16: the metrics and alerts of the new components.
- §17.2 and §17.8: the namespace layout, the admission inventory (the third UID), the mirror container's capability exception and its Pod Security prerequisite, placement, and sizing.
- §18: the materializer entry for `gitClone`.
- §21.9 and §26.3: the in-pod token-helper references.
- §24 and §25: `lenny-ctl` commands and operability tools, as the documentation section lists.
- §15: admin API resources for host sets, upstreams, git endpoints, tenant egress policy, tenant pool overlays, credential bindings, per-class gate grants, client proxy profiles, tenant client config files, and mirror prefetch.
- §13.1, §13.2, §15, §16, and §17: level 1 replacement, which covers the policy document and EDL endpoints, identities and their rotation, the I7 decision call, the validators' refusals, the probe, the alert, and the Helm values.
- §15.1: the `egress_denied` and `no_egress_path` reasons on `GIT_CLONE_REF_UNRESOLVABLE`.
- §23: "pods can only reach the gateway".
- §26.2: the removal of category-keyed defaults (egress, isolation, delivery mode, setup command policy, and `preConnect`), descriptive `defaultPoolConfig` text per reference runtime, and the `vcs.<provider>.*` scopes.
- §28: the new links and channels. The labels I1 to I8 are design-local; the proposal assigns `LNK-` and `CH-` identifiers under the naming law, which forbids positional names. §28.5.5's register of the pod-egress boundary (`CH-LLMPROXY` and `CH-OBJSTORE`) gains the new channels, and `CH-LLMPROXY` itself is unchanged.

## Relationship to proposal 0111 and open findings

Proposal 0111 is rewritten around this design.

- **Kept unchanged**: the slot repository rules (alternates, capture copy, and `storeCommit`), store immutability, the §7.1 **Bind bounds.**, and the defect filings. 0111's `ValidateGitURL` is replaced by the shared canonicalizer. 0111's `Dockerfile.git` for the gateway is dropped, because the gateway runs no git; F-14.1.37 closes with the clone path's removal.
- **Kept with changes**:
  - The warm fill and `gitRepository` shared asset are fetched by `netagent` over smart HTTP.
  - Private stores keyed by owner tenant are authorized through I3.
  - The mirror semantics move into `lenny-git-mirror`.
  - Restore and lineage authorization are kept.
  - The §13.4 exception is rewritten as above.
  - The `git-https` provider becomes a `stored` provider.
  - The pod identity prerequisite is met by the projected service account token instead of R14.
- **Dropped**: the `FetchGitPack` RPC, gateway pack serving, the gateway mirror limiters and memory table, and the gateway memory limit decision.

Findings closed or reshaped by this work:

- F-4.9.44 (minting).
- F-14.1.37 (git binary).
- The §26.2 and §13.2.6 contradiction.
- The unenforced mode and missing plan check in `lenny/vcs_token`.
- The Token Service breaker exposure from in-pod token requests.
- The tenant-admin write of `egressProfile` on shared pools, which should be filed as a finding.

A defect exists in the code today, independent of this design: service-mode pins have no arbiter across gateway replicas, and a label failure does not stop the request (`tenantaffinity.go` and `statelessproxy.go`). A service-mode pool with more than one gateway replica can route one tenant's request to a pod pinned to another. Proposal 2 closes it, and it should be filed as a finding in its own right.

## Proposal split

1. **Git prerequisites**: GitHub App minting with provider capability levels and the token cache (F-4.9.44), repository identifiers, the shared repository-path canonicalizer, repository routing, and write-scope checks. The `github` provider keeps accepting a stored `token` until proposal 3 removes its consumers, so proposal 1 lands without breaking `gitClone`.
2. **Pod network agent and proxied egress**: the `tenant_id` pattern, the durable service-mode pin record with UID-keyed routing, `netagent`, service account identity, `lenny-egress` on Envoy, `lenny-egress-authz` with I3 and I7, the binding feed with fan-out and the read-scoped provisional resume binding (§7.3), the owner label on tenant-owned pods at creation (§5.2), port scoping, the Kubernetes issuer verifier, the I8 listener, the `pkg/binding` resolution package, and the Token Service's new RBAC and ingress rows, the CA bundle assembly, the tunnel lifetime menu, quotas, pool ownership (`ownerTenantId`, taken from draft 0111 because the formulas depend on it), the `proxied` profile, the DNS resolver for `proxied` pods, ceilings, warm-up policies, tenant egress policy, session `egress.allowedHosts`, tenant pool overlays, host sets, the client proxy profile, the removal of `provider-direct`, the removal of category-keyed defaults from §26.2, and the `egressProfile` write restriction.
3. **Pod git path**: the local git endpoint, push with minted tokens, `writableRepositories`, the git config and `sshForms`, the `origin` remote, LFS, redirect handling, git in the adapter image, the pod-side clone, the gateway's Go `ls-refs` resolver through `lenny-egress`, the removal of the gateway clone path (which closes F-14.1.37), the workspace ownership fix, and the removal of `git-credential-lenny` and `lenny/vcs_token`.
4. **Credential delivery**: credential bindings with `inject` delivery (plain-HTTP upgrade and TLS termination for bound hosts), `federation` delivery with the Token Service as OIDC issuer and the AWS, GCP, and Azure templates, `metadata` delivery with the AWS plugin, and `file` delivery with the generic directory and the `netrc` projection. It also covers the network descriptor, the SDK network accessor, and tenant client config files.
5. **Git mirror and warm fill**: `lenny-git-mirror` with its sandbox and capability exception, per-store ownership, the fetch retry window, the mirror prefetch resource, and the rewritten 0111.
6. **Per-tenant routing and replacement**: upstream registrations and chaining (I6), private destination pinning, egress groups with their Helm-rendered grants and policies, tenant-registered public services, and deployer CA trust distribution.
7. **Level 1 replacement**: identities and their rotation, the policy document, its host grammar, the EDL endpoint, the reference Squid renderer, the I7 decision call and tunnel reports, the `netagent` server-name check, the service identities for the resolver and the mirror, the Helm-rendered private destination grants and tenant-scoped policies, the direct path for `netagent`, the resolver, and the mirror, the validators' refusals, the acknowledgement, the install order with its preflight and post-install probe, the canary, and the documentation of its limits.

Proposal 1 is independent. Proposal 2 lands I8, `pkg/binding`, and the I8 listener with no pod-side caller until proposals 3 and 4 use them, which its tests cover through a test client. Proposal 3 depends on proposals 1 and 2. Proposals 4, 5, and 6 depend on proposal 2, and proposals 4 and 5 also depend on proposal 3. The shared gateway key-source proposal (F-7.1.31, the per-process `uploadToken` HMAC ring) is unaffected and can run first.

Proposal 7 depends on proposals 2 and 3, and on proposal 5 only for the mirror's service identity. Later work: push forwarding through the mirror, GCP and Azure `metadata` plugins, and R14 certificates on I2, I5, I7, and I8. Revision 3 moves the DNS resolver for `proxied` pods and the server-name check into proposal 2, and TLS termination for bound hosts into proposal 4.

## Documentation

The documentation has no owner page for egress or for VCS credentials today. Egress profiles live in a section of the operator guide's security page and are restated, with drift, on several other pages. VCS credentials appear as one paragraph in the concepts page and one row of the workspace plan reference. Lenny's documentation rules say that each concept has one owner page and every other page links to it (`doc-content.md`), so this subsystem gets a dedicated chapter, and the existing pages shrink to summaries that link into it. The pages describe behaviour for readers who have not read the specification, with no section numbers, so the proposals' spec changes land before the pages that document them. Each proposal in "Proposal split" carries the documentation changes for its own surface. The chapter's landing page and the egress policy page land with proposal 2.

### New chapter in the operator guide

A chapter titled "Network egress and credentials" sits in the operator guide after "Security". Its landing page states the reading order. The pages below are listed with the subject each owns. The detail they carry comes from this design and the landed specification, and is not repeated here.

| Page | Owns | Notes |
| :-- | :-- | :-- |
| Overview (landing) | The components and the path of a request from a tool in the pod to the internet, to a git host, and through an upstream proxy. Also the security properties in reader terms, and the reading order. | Diagrams, under `doc-diagram-style.md`, of the request path, the decision flow, and the git path. |
| Egress policy | Egress profiles, ceilings, host sets, warm-up policies, tenant egress policies, session host lists, and how the effective policy combines them. | Uses a subset of the scenarios as worked examples. States the exfiltration limit of hostname filtering. Becomes the owner of the content now in the security page's "Network Policies" section. |
| Tenant configuration | Who manages each setting, tenant pool overlays and their flags, tenant-registered public services, and platform-registered private services. | The reader-facing form of the scope table. |
| Git access | VCS credential pools, provider capability levels, repository prefixes, write scopes, stored-token push and its risks, SSH remotes, `gitAccess`, LFS, and the git mirror (enabling, sizing, prefetch, the fetch retry window, its Pod Security prerequisite, and purge). | Includes a task section for setting up a GitHub App. Replaces the concepts page's description of gateway clones and the in-pod credential helper. |
| Credential bindings | The delivery modes, how to choose between them, placeholders, response scrubbing and its limits, and cloud credentials. | Distinguishes credential bindings from connector OAuth and from LLM credential pools, and links both. |
| Upstream proxies and corporate gateways | Chaining, egress groups and their grants, TLS interception and CA distribution, and replacing the default proxy with one that speaks the decision protocol. | States which identity methods each documented gateway accepts, as recorded in this design. |
| Level 1 proxies | The proxy contract, the policy document and the EDL output, identities and credential rotation, the probe, and the settings that select level 1. | Opens by stating that level 1 is not the recommended path. It then gives the table of what level 1 gives up against the default and level 2, the reasons, and when level 1 fits, and it directs most deployers to level 2 or chaining. Every page that describes a guarantee level 1 weakens links here. Runtimes need no change at level 1, and the runtime author guide says so. |
| Deploying and operating | CNI requirements, including AWS VPC CNI strict mode and the kindnet limits. Also Envoy sizing (circuit breakers, timeouts, and the tunnel lifetime against LLM stream length), version pinning and advisories, metrics and alerts, and troubleshooting. | Troubleshooting covers a 403 traced through the decision log, a failed in-pod clone, and a tool that ignores the proxy. Links to the runbooks. |

### Runtime author guide

The runtime author guide gets one new page, "Network and credentials in the pod", which owns the runtime's view of the subsystem:

- the launch variables and the fixed endpoints;
- the `/run/netagent` layout and the network descriptor;
- the tools that need extra configuration, and baking tool configuration into an image;
- keeping the proxy and trust variables for spawned commands;
- git from inside the pod;
- credential bindings as the runtime sees them;
- the SDK accessor;
- the embedded model;
- behaviour under local development.

Existing pages change as follows:

| Page | Change |
| :-- | :-- |
| `index.md` | The reference runtime table loses the category-keyed framing ("same workspace layout, same pre-installed toolchains, same sandbox profile"). The `coding` scaffold template description drops platform defaults. The pod diagram gains `netagent`. Links to the new page. |
| `runtime-configuration.md` | `defaultPoolConfig` lists `proxied` and drops `provider-direct`. The `env` field states which variable names are reserved on `proxied` pools. |
| `lifecycle.md` | **Runtime Supervisor → Environment** adds the launch variables. **Filesystem Layout** adds `/run/netagent`. **Workspace Materialization** describes the in-pod clone instead of gateway streaming for `gitClone` sources. The SDK-warm path notes that warm-up egress applies before claim. |
| `local-development.md` | States how `lenny up`, `make run`, and the compose stack run `netagent` and the egress proxy, and which enforcement is absent locally. |
| `integration-levels.md` | States that the network and credential mechanisms work at every integration level and need no level-specific code. |
| `runtime-sdk.md` and `sdk-examples/` | Document the network accessor, with one example per language. |
| `publishing.md` | The pool configuration table gains the egress fields, or links to the egress policy page. |
| `testing.md` | Describes the new conformance suite for network and credentials. |
| `platform-tools.md` | No change. `lenny/vcs_token` was never documented. |

### Changes to existing pages elsewhere

| Page | Change |
| :-- | :-- |
| `index.md` (home) | The egress paragraph names `restricted`, `proxied`, and `internet`. The credential delivery section and its diagram add credential bindings beside LLM delivery. The workspace template sentence reflects the in-pod clone. |
| `getting-started/concepts.md` | **Workspace sources** describes the in-pod clone and write scopes and drops "the pod itself has no network access during setup" and the in-pod credential helper. The egress profile line, "No ambient network", and "no git-based workspace population" are updated. **Credentials** adds credential bindings and VCS credential pools as categories. **Platform-global vs tenant-scoped** mentions tenant pool overlays. |
| `getting-started/architecture.md` | The component descriptions add `netagent`, `lenny-egress`, `lenny-egress-authz`, and `lenny-git-mirror`. The NetworkPolicy and egress rows, and the DNS sentence ("via the cluster DNS service"), are corrected. |
| `getting-started/quickstart.md` | "Try a coding agent" drops category assumptions and states how a coding agent gets git and network access. "no network access except back to the gateway" is updated. |
| `operator-guide/security.md` | **Network Policies** shrinks to a summary that links to the new chapter. **Provider-Direct Egress Profile** is removed. **Credential Leasing** links to credential bindings. Inbound anchors (`#network-policies`, `#dedicated-coredns`, and `#credential-leasing`) are kept or redirected. |
| `operator-guide/security-principles.md` | The control map adds the egress proxy, the decision service, per-connection audit, and the `proxied` resolver. The web-filtering row points to `proxied`. |
| `operator-guide/namespace-and-isolation.md` | The namespace layout adds the new `lenny-system` components. The policy summary adds `proxied`. |
| `operator-guide/configuration.md` | **Pool Dimensions** adds the egress fields. **Credential Pools** adds VCS pools, GitHub App keys, and repository prefixes, and links to credential bindings. The `github` `token` Secret row changes to the App key fields. |
| `operator-guide/multi-tenancy.md` | Adds tenant pool overlays and tenant egress policies to the scoping classification. |
| `operator-guide/installation.md` | Adds prerequisites, preflight rows, and verification steps for the new components. |
| `operator-guide/external-llm-proxy.md` | Pattern A (direct delivery) states that the pod reaches the provider through `lenny-egress`. |
| `operator-guide/observability.md`, `troubleshooting.md`, `lenny-ctl.md`, and `agent-operability.md` | Add the new metrics, alerts, troubleshooting entries, CLI commands, and operability tools and scopes. The `--template coding` description drops platform defaults. |
| `client-guide/wire-format.md`, `session-lifecycle.md`, and `error-handling.md` | Add `egress.allowedHosts`, `writableRepositories`, and session credential bindings, the in-pod clone's failure surfacing, and the new errors. |
| `api/admin.md` | Adds the resource groups for host sets, upstreams, git endpoints, tenant egress policies, tenant pool overlays, credential bindings, per-class gate grants, client proxy profiles, and mirror prefetch. States the `egressProfile` write restriction. Egress groups are Helm values and are documented in the operator guide and the Helm reference. |
| `api/rest.md` and `api/mcp.md` | `POST /v1/sessions` gains the new fields. The `GIT_CLONE_*` errors are revised for in-pod resolution. |
| `reference/workspace-plan.md` | The `gitClone` row describes the in-pod clone and write scope. Adds `writableRepositories`, `egress.allowedHosts`, and session credential bindings. `env` states the reserved names. |
| `reference/error-catalog.md` | Adds the egress, binding, and clone errors. |
| `reference/metrics.md` | Adds the metrics and alerts of the new components. Revises the direct-mode wording ("the pod egresses to the provider directly"). |
| `reference/configuration.md` | Adds a section for the Helm values of the new components and the egress settings. |
| `reference/adapter-contract.md` | Specifies the network descriptor and the credential directory as contracts. The pod container diagram gains `netagent`. |
| `reference/glossary.md` | Adds egress profile, `netagent`, host set, egress ceiling, credential binding, and tenant pool overlay. Revises Credential Pool. |
| `about/why-lenny.md` and `about/status.md` | Update the egress perimeter paragraph and add status rows for the new components. |
| `tutorials/wrap-coding-agent-cli.md` | Drops the category framing and shows `proxied`, `gitAccess`, and credential bindings for a coding agent. |
| `tutorials/multi-tenant-setup.md`, `deploy-to-cluster.md`, and `using-connectors.md` | Add tenant overlays, the new install and verification steps, and a cross-link that separates connector OAuth from VCS credentials. |
| `runbooks/` | New runbooks for each new alert. The decision service, tunnel saturation, mirror fetch failures, mint rate, and `netagent` restarts are covered. `dns-outage.md`, `network-policy-drift.md`, `llm-egress-anomaly.md`, `agent-to-llm-partition.md`, `token-usage-anomaly.md`, and the credential runbooks are revised. The runbook index gains the new entries in its alert map, component map, and catalog. |
| `alerting/` | Generated. The new alerts go into `pkg/alerting/rules` and the files are regenerated. |
| `adr/` | A new ADR records the decisions in this design. The ADR index states that a change to the credential model warrants one. |
| `testing/` | `domain-suites.md` and `forward-compatibility.md` reflect the in-pod `gitClone`. `documentation-tests.md` is brought up to date with the tier 11 tests it describes. |

### Existing inconsistencies to fix in the same change

- Agent DNS is described three ways: as a dedicated CoreDNS in the agent namespaces (the security page), as `lenny-coredns` in `lenny-system` (the DNS outage runbook), and as `lenny-agent-dns` (the alert rules). The pages adopt the specification's name, and they add the `proxied` resolver beside it.
- `api/mcp.md` shows a workspace source of type `git`. The type is `gitClone`.
- `GIT_CLONE_AUTH_UNSUPPORTED_HOST` and `GIT_CLONE_AUTH_HOST_AMBIGUOUS` appear in the REST and MCP pages and are missing from the error catalog.
- `llm-egress-anomaly.md` uses a `lenny-ctl admin egress-policies` command that the CLI page does not document. Its name collides with the new tenant egress policies, so one of them is renamed.
- `pool_admission_isolation_test.go` in tier 9 pins the `provider-direct` rejection. It changes with the profile's removal, together with the security page's text.

### Documentation tests

The tier 11 tests that pin phrases and paths on these pages constrain the edits, and they change in the same change as the pages:

- the credential path sweeps (`credentialsPath` and the slot credential file as the only credential path), with which the `/run/netagent` credential directory must stay consistent;
- the filesystem layout reconciliation for `lifecycle.md`;
- the runbook structure, trigger, step, and index-count tests;
- the alert-to-runbook resolution;
- the chaos runbook map, which needs an entry for each alert-driven runbook;
- the code-block parser, which every new YAML and JSON example must pass;
- the tier 0 fragment-link test, which guards the inbound anchors listed above.

## Tests

Each proposal in "Proposal split" carries the tests for its own surface, at the tiers that `test-coverage.md` maps that surface to. This section states what the tests must establish. The proposals turn it into test lists with `// spec:` annotations, and the implementation workflow writes and runs them. The validation rounds in this document are design evidence only. They ran against stand-ins such as mitmproxy and throwaway fakes, so each behaviour they established is tested again against the Lenny components.

### Tier 0: static

- The Envoy bootstrap that the chart renders passes Envoy's own configuration validation for each supported combination: the default, chaining, the server-name check, and ext_authz in HTTP mode.
- The schemas of the new CRD fields and admin API resources validate, and so do the network descriptor and `binding.json`.
- Every flag the pod-spec renderer emits for a container is defined by that container's binary, including the embedded runtimes. This turns the environment-preflight check on flag drift into a static test.
- No code path renders a secret into an environment variable or a command-line argument of `netagent` or of the runtime.

### Tier 1: unit

- **Effective policy.** The tests cover each binding state under "Effective policy":
  - ceilings, including `*`;
  - tenant pool overlays under each `tenantOverride` value;
  - the precedence of denied hosts;
  - the validation of `egress.allowedHosts` at create.
- **Host matching.** The tests cover:
  - wildcards, ports, letter case, internationalized names, and trailing dots;
  - IP literals in canonical and non-canonical forms (decimal, octal, hexadecimal, IPv4-mapped, and NAT64);
  - the lower-casing of authorities that the server-name check depends on.
- **Repository handling in `netagent`.** The tests cover:
  - every remote form the git configuration rewrites;
  - `sshForms` host and path templates, and the git `ERR` fallback;
  - encoded and traversing path segments;
  - readable and writable set enforcement;
  - the redirect rewrite rules.
- **Minting.** The tests cover token cache keys, refresh timing, and per-App rate accounting.
- **Binding feed.** The tests cover notification handling, read-through on a cache miss, the policy-version cache key, and the generation rule for a doubly bound session.
- **Private destination pinning.** The tests cover the registration-time resolution and refusal rules, the periodic re-resolution, and that no private address ever reaches the resolver path.
- **Credential attachment.** The tests cover:
  - `attach` templates for Bearer, Basic, and custom headers;
  - header overwrite whatever the placeholder holds;
  - stripping of hop-by-hop headers.
- **Response scrubbing.** The tests cover:
  - whole bodies under each content encoding;
  - the sliding window at every split offset of the secret;
  - WebSocket frames and header values.
  - The unscrubbed base64 and hexadecimal reflections are asserted as unscrubbed, so the documented limit is pinned rather than implied.
- **File delivery.** The tests cover the atomic directory swap, values written without a trailing newline, the `binding.json` schema, and the `netrc` projection.
- **Launch configuration.** The tests cover the launch variables, the fully qualified `NO_PROXY` list, and CA bundle assembly from image roots, deployer CA, and per-pod CA.
- **SDK accessors.** In each language, the tests cover descriptor parsing, the bounded retry for a session entry that is not yet written, and `env()` and `credential(name)`, which re-read on every call.

### Tier 2: component

- **Pod spec (envtest).** The tests cover:
  - `netagent` as a native sidecar with its own UID and a fixed resource request outside the pool's resource class;
  - the projected token volume, with the two deployment-specific audiences (§10.3), mounted into `netagent` only;
  - `/run/netagent`, read-write for `netagent` and read-only for the runtime;
  - the launch variables in the supervisor environment and in the embedded container environment;
  - the rejection of reserved variable names in a Runtime `env` on `proxied` pools;
  - the labels for `proxied` and `gitAccess`.
- **Admission.** The tests cover:
  - the `egressProfile` write restriction for a tenant admin on a shared pool;
  - overlay validation against the `tenantOverride` flags;
  - label immutability with the new profile value;
  - the rejection of the removed `provider-direct` value.
- **`lenny-egress-authz`.** The tests cover:
  - the pod informer, its projection, and binding resolution from the pod labels, the pool record, the tenant pin, and the session store;
  - fail-closed behaviour before the informer syncs, and the stale-cache bound during API server loss;
  - token validation, including the rejection of a deleted pod's token;
  - cache invalidation on a binding change and on a binding definition edit.
- **`lenny-git-mirror` in one container.** The tests cover:
  - store separation per authorization boundary: an object known by SHA in one tenant's store is not served from another tenant's;
  - `fsckObjects` rejection of malformed packs;
  - serialized fetches per repository;
  - per-store ownership: a fetch subprocess can write its own store and no other, a public-store `upload-pack` cannot write, a tenant-store `upload-pack` cannot write where ACLs exist, a second fetch under another UID can write the first fetch's packs, and the parent re-chowns after each fetch;
  - the ambient-capability start: the parent is non-root with exactly three capabilities and its `fsGroup`, and the fetch child has none;
  - UID retirement: a process a fetch leaves behind is killed at fetch exit, before the re-chown and before the fetch is reported complete, a UID is never reused within the parent's lifetime, and a survivor cannot write the store afterwards or connect to a later fetch's forwarder;
  - the re-chown walk ignores a planted symlink and changes owners only;
  - the prefetch request, including refusal for `direct`-routed repositories;
  - purge.
- **`netagent` in one container.** The tests cover the CA kept across a restart, the readiness endpoint, and failing closed when the decision service is unreachable.

### Tier 3: contract

- I3 in its gRPC and HTTP/2 forms, between Envoy and `lenny-egress-authz`. The check carries the authority, `Proxy-Authorization`, and the peer address. The response carries the route header and the dynamic metadata, and removes `Proxy-Authorization` on chained routes.
- I7: the binding subscription, the proxy Service to dial, the base-destination list, local decision reports, and error codes.
- I8: credential requests on the `lenny-token-service` audience token, the Token Service's own verification of that token with the peer-address check from its own pod informer, its binding re-read, and its I3 scope call carrying the pod UID and peer address without the token. A request relayed through any identity other than the pod's or the mirror's client certificate is refused, and the mirror identity obtains read tokens only, never `stored` credentials.
- I5: git smart HTTP through `netagent` and the mirror, protocol v2, and the `ERR` packet that git displays.
- The network descriptor and the credential directory, as a versioned contract that the SDKs consume.
- The OIDC discovery document and JWKS of the Token Service, and the claims of the federation JWT.
- The `metadata` formats, including the AWS container credentials response and its token-file authentication.

### Tier 4: integration

- **Git end to end on the compose stack.** A local git server and the mirror sit behind `netagent`. The tests cover:
  - clone and fetch, including shallow and partial clones;
  - push with a token minted from a fake GitHub App API;
  - a push outside the write scope refused with no mint call made;
  - the read-after-push refresh;
  - LFS batch and transfer, submodules, and redirects.
- **Client compatibility matrix for `inject`.** Real client tools in containers run against `netagent` and a fake upstream with a test CA. The tools are curl, Python requests, httpx over HTTP/1.1 and HTTP/2, aiohttp, Node fetch, Go, Java HttpClient, a rustls-based client, `gh`, `glab`, npm, pip, Maven, and Gradle. For each tool the matrix covers:
  - injection on a bound host and passthrough on an unbound host;
  - placeholder handling;
  - the plain-HTTP upgrade where the tool supports it;
  - scrubbing of reflected secrets;
  - SSE and WebSocket streaming.

  This matrix replaces the round 3 mitmproxy evidence with evidence about `netagent` itself, and it records the latency and memory figures that round 3 could not give.
- **`federation` delivery.** Each supported cloud SDK runs against a fake provider token endpoint and a fake service, exchanges the Lenny JWT, signs with the result, and re-reads a rotated token. The Azure case runs on a tenant-owned pool and through constructor arguments.
- **`metadata` delivery.** Each supported cloud SDK runs against the `netagent` plugin and a fake service, for each shipped format.
- **Minting.** The Token Service runs against a fake GitHub API. The tests cover the `repositories` and `permissions` parameters, cache hits, refresh before expiry, rate accounting, and error mapping.
- **Session create.** The tests cover validation of `egress.allowedHosts`, `writableRepositories`, and credential bindings, including their rejection on concurrent-session pools, and the synchronous `ls-refs` resolution with its two error codes.
- **LFS upload authorization.** An LFS batch with `operation: upload` takes the write path and is refused outside the writable set.
- **Bind race.** The first pod-side request after a bind commit succeeds through the read-through, under an injected notification delay.
- **Platform traffic under the launch variables.** Checkpoint uploads, the gateway link, and the LLM proxy path reach their endpoints directly in both pod models.
- **Agent git configuration.** `git config --global` succeeds in the pod, the image's global and system configs apply, and Lenny's rules follow them.
- **Resume with `gitClone`.** A resumed session with a `gitClone` source materializes through the provisional binding.
- **Union write scope.** On a concurrent-session pod, a slot pushes to a repository another slot declared, and the union shrinks when that session ends.
- **`ls-refs` under tenant routes.** Resolution goes through a private git endpoint and an egress group, with the v0 fallback, and a hostile host hits the byte and time caps. The mirror case, including a repository the mirror has never fetched, lands with proposal 5.
- **`ls-refs` on the egress path.** Calls alternating between tenants produce one decision per call. A stopped replica and a frozen replica each cost one retry with no failed call. Proxy and decision service outages return the transient error within their bounds, and a denial returns `egress_denied`. A `.git` redirect is followed once and every other redirect is refused. The v0 path stops at its match and hits its cap on an oversized advertisement.
- **`ls-refs` through the mirror.** The answer tracks the upstream ahead of the store. Each listed cause answers from the store within its age limit and reports `resolvedFromStore` on the session, and past the limit it returns the transient error. A host 401, 403, 404, or 429 and a CONNECT 403 or 502 never fall back. A decision service outage returns the transient error, and identical calls coalesce. A pinned SHA the store lacks schedules a fetch, under the negative cache, the per-tenant rate, and the minimum interval. A host 401 or a refused mint clears confirmations, a later 503 does not serve from the store, and a confirmed commit the store lacks returns the transient error. Confirmations survive a mirror restart.
- **Private destinations.** At the default level a pod and the resolver reach one through a raw tunnel, with fetch and push from stock git, a raw socket from any pod is dropped, and an ungranted tenant is refused. Validators refuse private destinations for a level 2 proxy that declares no pinned dialing. At level 1 only labelled pods of granted tenants have a path, `inject` and LFS work on the private host, a Helm revocation removes the path, and the gateway and the mirror refuse a decision address that differs from their registration read.
- **Label visibility.** A request that reaches a component before its informer sees the tenant label is retried, succeeds once the label appears, and fails closed if it never does. A service-mode pod behaves the same through its pin record and never runs tenant traffic under the warm-up policy.
- **Service-mode pin.** Two gateway replicas racing to pin one pod for different tenants produce one winner, the loser reroutes, and a same-tenant race adopts. A store write failure aborts the request, a failed label stamp is retried, an IP reused for a new pod carries no old pin, a gateway rollout keeps the tenant on its pinned pods, and records of deleted pods are removed. The first request never runs under a cached warm-up decision.
- **Pool names.** A deleted pool's name is refused for a new pool, deletion cascades to its overlays, grants, and level 1 identities, and an unlabelled pod is refused on a tenant-owned pool.
- **Level 1 on Squid.** A reference Squid and sync job hold per-identity lists, generation overlap and retirement, and edit propagation. The resolver and the mirror work through it, `netagent` refuses a CONNECT the decision denies before it dials, a fronted tunnel is closed, and the validators refuse chaining, egress groups, and private destinations through the proxy. Each pod state in the identity table receives its stated identity, and a private git endpoint works for pods, the resolver, and the mirror.
- **Binding push fan-out.** With several decision-service replicas, every replica serves the new binding within one round trip, and a replica that missed the push repairs itself within the re-read bound.
- **Canonical repository paths.** The shared canonicalizer refuses traversal, encoding, parameter, userinfo, and port tricks identically in the resolver, at create, and in `netagent`.
- **Materialization without the gateway clone.** A session with `gitClone` sources materializes through the pod path, and the resolved commit is reported back and persisted.

### Tier 5: end to end on Kind

- **The full chart with `proxied` pools.** Pods reach only `lenny-egress`, `lenny-egress-authz` on I7, the Token Service on I8, the git endpoints, the resolver, and the base destinations, each on its port, and a direct connection fails. The `proxied` resolver answers cluster names and returns NXDOMAIN for every other name.
- **Warm-up egress before claim.** The tests cover the warm fill from the mirror, the policy change at claim, and the closing of tunnels when a session ends.
- **Pod models.** Each row of the policy table under "Pod models" is tested: pod-warm, SDK-warm, service mode, recycle, concurrent sessions, tenant-owned pools, and the embedded model.
- **`restricted` pools with `gitAccess`.**
- **Scope of Kind.** kindnet does not filter traffic to node-local endpoints and fails open without its userspace program. Tier 5 asserts only what kindnet enforces, and the node-local cases move to tier 6.

### Tier 6: end to end on cloud

- `proxied` NetworkPolicy enforcement on Calico (iptables and eBPF dataplanes), Cilium, GKE Dataplane V2, and AWS VPC CNI in strict mode, including node-local endpoints.
- An egress group's NAT address as seen by an external echo service.
- GitHub App minting against a test organization. GitHub enforces repository scoping and the `workflows` permission, and a real `gh pr create` succeeds through `inject`.
- `netagent` in gVisor and Kata pools.

### Tier 7: load

- **7a (local).** The tests cover:
  - decision latency and the decision cache in `lenny-egress-authz` at the CONNECT rates the round 3 Envoy results reached;
  - the mint cache with many sessions on one GitHub App installation.
- **7b (Kind).** The tests cover:
  - Envoy tunnel concurrency above the default limit of 1,024 per cluster;
  - CONNECT setup latency at the target rates;
  - ext_authz timeout behaviour;
  - many intercepted streams through one `netagent`;
  - concurrent warm fills from the mirror.

### Tier 8: chaos

- **The decision service.** When it is down, slow, or partitioned, CONNECTs fail closed and recover within the documented time.
- **A `netagent` crash.** A crash during a tunnel and during a push leaves the runtime running, egress fails closed during the restart, and the CA is unchanged afterwards.
- **Mirror and Token Service loss.** The tests cover the loss of a mirror pod during a fetch, and an unavailable Token Service during a push, at an `always` binding's bind time, at a federation rewrite, and at a `metadata` refresh. Each fails closed, cached credentials serve until expiry, and no token is used past its expiry.
- **Decision replica loss.** `netagent` resubscribes within the grace period and keeps its binding; a rollout of the decision service closes no tunnel.
- **API server loss.** With the API server unreachable, decisions and mints continue on the informer cache for existing pods until their tokens expire, new pods fail closed, the alert fires, and the Token Service stays ready for the gateway lease path.
- **Fetch retry window.** A first fetch longer than the window fails the session at finalize with `git_fetch_pending` before the finalize watchdog fires, with several `gitClone` sources in one plan; a resume with a pending fetch fails after the 30 s wait before the `resuming` watchdog; and the next session is served from the store.
- **Envoy and DNS.** The tests cover an Envoy rollout with live tunnels, and DNS rebinding against a controllable resolver.

### Tier 9: security

- **Secrets out of agent reach.** The agent UID cannot read the service account tokens, upstream tokens, the CA private key, or `inject` secrets, through the filesystem, procfs, the environment, or command lines. A control case with `shareProcessNamespace` set confirms the webhook rejects the pod.
- **No secrets in the environment.** No credential appears in any environment variable or argument of the runtime or of `netagent`. This extends the existing credential-leakage tests.
- **SSRF.** The tests cover every non-public address range, reached through hostnames, literals, IPv4-mapped and NAT64 forms, and rebinding.
- **Tenant isolation.** A pod of one tenant cannot reach another tenant's private services, upstream proxies, git endpoints, egress groups, mirror stores, or credential bindings, including by object SHA, and including through a hostname the tenant controls that resolves to another tenant's private address.
- **Mirror poisoning.** A repository filled through one tenant's route or credential is never served from the shared public store, and a public store stops serving after its upstream turns private.
- **Host mismatch under `inject`.** A request whose `Host` differs from the CONNECT authority on a bound host carries no injected header.
- **Federation isolation.** A token issued for one tenant fails verification against another tenant's issuer, and fails at every Lenny endpoint.
- **Quotas.** One pod at its tunnel and rate quotas does not degrade another tenant's CONNECT latency or success rate, and a lost access-log stream expires its counted tunnels within the heartbeat bound rather than refusing the tenant indefinitely.
- **Egress groups.** A pod of an ungranted tenant is dropped by the group's ingress policy and refused by the decision; a pod of a granted tenant is refused at the default Envoy; a forged `x-lenny-egress-group` header from a pod never reaches the decision.
- **Binding feed.** A forged push changes no decision, a lost push is repaired within the re-read bound, and a lower generation is refused.
- **Header hygiene.** Agent-supplied route, identity, and `Proxy-Authorization` headers never leave `netagent`, and a copied service account token is refused from any address but the pod's.
- **Chained upstreams.** The pod's `Proxy-Authorization` never reaches a chained upstream, and the upstream's 407 and 403 replies are normalized.
- **The server-name check.** A mismatched name, a missing name, and an IP-literal authority are refused. The Encrypted Client Hello case is asserted as a documented limit.
- **Token misuse.** A deleted pod's token is refused. Minting a pod-bound token requires the RBAC verb that no Lenny component holds. A credential request on I8 that arrives from any identity other than the pod's or the mirror's is refused, including one relayed by the decision service, and the mirror identity cannot obtain a `stored` credential or a write token.
- **Port scoping.** A pod cannot reach Envoy's admin interface, the metrics listeners, the I3 or access log ports, or any mirror port other than git.
- **Provisional binding.** A resuming pod obtains read tokens for its `gitClone` sources and nothing else, and a resume that fails leaves the released pod with no session grant.
- **Two gates.** A decision service that answers allow for a host outside the binding's stored `hosts`, or for a repository outside the stored writable set, or that asserts another tenant for the pod, obtains no mint, and `netagent` injects nothing for a host absent from the I8 response.
- **Cache bounds.** A credential cached for one session is gone from `netagent` before the next session on the same pod, and a rotated stored secret reaches the pod within its cache lifetime.
- **Mirror store reads.** The mirror derives the requesting pod's tenant from its own `pkg/binding` instance and refuses a store the decision allowed for another tenant.
- **Mirror subprocess boundary.** A stand-in for a compromised fetch subprocess cannot read the mirror's identity material, another tenant's store, or a concurrent fetch's forwarder socket.
- **Routing rule.** A repository under a `mint-coarse` or `stored` pool goes `direct` from `netagent`, resolves upstream in the gateway, and is refused by the mirror, whatever the tenant's git route says.
- **I8 isolation.** Pods at their I8 quotas do not raise the gateway's lease-path latency or trip its Token Service breaker.
- **Owner stamp.** A pool created without an owner cannot gain one, and a recycled pod keeps its tenant identity at level 1, and the controller's creation-time owner label passes the webhook, and a gateway re-stamp with another tenant is refused.
- **Placeholders and scrubbing.** A secret is never attached to an unbound host, a placeholder sent to an unbound host carries no secret, and reflected secrets are scrubbed.
- **Stored secrets and git hosts.** A stored-secret `inject` binding on a host a VCS pool covers is refused at create, and a `git push` through the egress proxy to such a host carries no credential.
- **Level 1 limits.** The probe fails against a proxy with no address rule, one that ignores authentication, one that refuses everything, one without TLS, and one whose sync job has stopped, and it passes against the reference configuration. Preflight refuses a proxy without TLS or authentication before install, and warm pods do not fill until the post-install probe passes and its ConfigMap shows it. `lenny-ops` obtains probe credentials with its rendered rows. Hosts carrying a newline, proxy syntax, or a numeric final label are refused on write and on generation, and the contract suite's renderer cases cover them. An identity's credential is absent from the agent UID's reach and from the environment, tenants hold separate identities, a warm pod of a shared pool holds only the warm identity, and a bound pod's credential is flushed on recycle. The chart refuses level 1 without the acknowledgement. A test records each residual in the limits table, so the documentation cannot drift from behaviour.
- **Tenant admin limits.** A tenant admin cannot change `egressProfile` on a shared pool, and an overlay cannot extend a setting without the platform flag.
- **Tree validation.** Components at the adapter or `netagent` UID never follow workspace symlinks out of the tree, and the fetch size caps hold.

### Tier 10: conformance

- The runtime adapter conformance battery gains a network and credentials suite. It runs for a runtime with no SDK and for one built on each SDK, in the sidecar and the embedded models. Each runtime must:
  - reach a bound API through `inject`;
  - read a `file` binding;
  - clone and push through the git endpoint;
  - pass the launch variables to a spawned shell command.
- The SDK accessors behave identically across languages against shared descriptor fixtures.
- The level 1 proxy contract ships as a suite a deployer runs against a candidate proxy.

### Tier 11: documentation

- The examples on the new documentation pages validate against their schemas. These include Helm values, admin API payloads, credential bindings, and the network descriptor.
- The Level 1 proxies page carries the limits table, and the pages that describe a weakened guarantee link to it.
- Every new alert resolves to a runbook. The new alerts cover decision failures, mint rate, mirror errors, `netagent` restarts, and tunnel saturation.

## Decisions

Recorded owner decisions:

- Level 1 replacement is included (2026-11-02). The owner reversed the deferral, and the documentation must state its limits and why it is not the recommended approach.
- Decisions 1 to 11, 13 to 30, and 32 are adopted as recommended (2026-11-02), along with the four corrections listed under revision 15 in "Review record". Decision 31 and decisions 33 to 36 remain open.
- Local endpoints are adopted, and they run in the separate `netagent` container.
- The decision service runs separately from the gateway.
- Pull requests and host APIs go through standard HTTP on the egress route, with no platform tool. An in-pod API token is acceptable as a tenant opt-in.
- Policy for concurrent-slot pods is per pool and tenant.
- Envoy is the default proxy. The owner made this conditional on hands-on testing, and the third validation round passed it with the configuration changes listed under "Central egress proxy".
- Stored-token push sits behind a gate plus a per-pool tenant opt-in, with the risks stated under "Credentials". Revision 6 made the gate a per-tenant platform grant (decision 19), which supersedes the deployment-wide gate this decision first recorded.
- Tenant admins can register upstream proxies and git endpoints on public addresses, following the connector precedent. Private-address services stay under platform registration.
- `gitAccess` on `restricted` pools is adopted, so `restricted` pools keep warm stores without general egress.

Decisions raised in revision 3:

1. **Remove `provider-direct` now.** Recommendation: adopt, with `deliveryMode: direct` paired with `proxied` and an LLM provider host set (see "Consolidating `provider-direct`").
2. **Remove the gateway clone path.** Recommendation: adopt. The gateway runs no git, clones run in the pod through `netagent`, and F-14.1.37 closes by deletion (see "Credentials", **The gateway runs no git.**).
3. **Remove category-keyed defaults from §26.2.** Recommendation: adopt (see "Runtime categories in §26.2").
4. **`inject` as the default credential delivery, including TLS termination for bound hosts.** Recommendation: adopt, with `file` as the gated opt-in. Round 3 validated both transports with mitmproxy standing in for `netagent`.
5. **Egress groups for corporate gateway identity.** Recommendation: adopt, because no surveyed gateway documents header identity on an undecrypted CONNECT.
6. **Tenant pool overlays with `tenantOverride` defaulting to `narrow`.** Recommendation: adopt (see "Tenant pool overlays").
7. **SSH remotes through `sshForms` data with a git `ERR` fallback.** Recommendation: adopt, with no per-provider code in `netagent`.
8. **SDK scope.** Recommendation: one read-only network accessor per SDK (endpoints, launch variables, bindings, and `file` values read on each call), and nothing else. Every mechanism keeps working for runtimes without an SDK.
9. **Cloud and platform credentials.** Recommendation: adopt `federation` as the generic mechanism, with the Token Service as OIDC issuer and provider templates as data, and adopt `metadata` as a provider plugin for tenants that cannot configure federation trust, with AWS shipped first. Both sit behind the `agentReadableDelivery` grant with `file`.
10. **No tool-specific templates shipped by Lenny.** Recommendation: adopt. Lenny ships the convention variables, the git config, the CA bundle, the descriptor, and the generic credential directory with a `netrc` projection. Tool files (Maven `settings.xml`, `gh` `hosts.yml`) come from the runtime image or a deployer template.
11. **Constant placeholder.** Recommendation: adopt `lenny-injected` as a constant fixed at pod creation, because `netagent` overwrites the header whatever the placeholder holds.

Decisions raised by the revision 4 review:

12. **Level 1 replacement.** The review recommended deferring it. The owner decided to include it, with its limits and the reasons it is not recommended stated in the documentation (see "Level 1 replacement").
13. **Private destinations pinned by the decision, with no filter exemptions.** Recommendation: adopt. It closes the cross-tenant path through tenant-controlled DNS, and it removes the need for SSRF exemptions.
14. **Per-tenant federation issuers.** Recommendation: adopt, because AWS and Azure trust conditions cannot express a tenant claim, and a per-tenant issuer scopes trust by construction.
15. **Egress groups as additional `lenny-egress` Deployments with pod pinning.** Recommendation: adopt, in place of a second proxy tier.
16. **Ref resolution stays in the gateway, through a Go `ls-refs` client.** Recommendation: adopt. It keeps the §14 contract synchronous and trusts no SHA from the pod.

Decisions raised by the revision 5 review:

17. **Binding feed as a gateway push plus bounded re-reads, with a provisional binding on resume.** Recommendation: adopt. Postgres notifications are unavailable through the §12 pooler, and the provisional binding is the smaller change to §7.3.
18. **Egress-group grants as Helm values with chart-rendered ingress policies.** Recommendation: adopt, so no Lenny component writes NetworkPolicies.
19. **One gate per risk class, granted per tenant.** Recommendation: adopt, in place of one deployment-wide gate.
20. **A tunnel lifetime menu rendered by the chart.** Recommendation: adopt, so pools choose a cap without xDS.

Decisions raised by the revision 6 review:

21. **`netagent` requests credentials from the Token Service directly (I8), and the Token Service consults the decision service for scope.** Recommendation: adopt, because relaying the pod's token through the decision service made that service a bearer-token relay.
22. **The controller stamps the owner tenant on tenant-owned pool pods at creation.** Recommendation: adopt, with the matching label-webhook rule.
23. **One shared repository-path canonicalizer** for the resolver, session create, and `netagent`. Recommendation: adopt.

Decisions raised by the revision 7 review:

24. **Two independent gates for every mint, with the Token Service authoritative for binding definitions and the decision service for policy and liveness.** Recommendation: adopt, with one shared `pkg/binding` package.
25. **I8 on an isolated, rate-limited TLS listener of the Token Service.** Recommendation: adopt, so pod load cannot reach the gateway's lease path.
26. **`ownerTenantId` set only at pool creation and immutable afterwards.** Recommendation: adopt.

Decisions raised by the revision 8 review:

27. **A Kubernetes-backed pod view in the Token Service.** Recommendation: adopt. Two independent gates need two independent identity sources, and the Pod label is the only authoritative tenant pin, so the Token Service runs its own informer over the agent namespaces.
28. **The mirror serves `mint-scoped` providers only.** Recommendation: adopt; repositories on `mint-coarse` and `stored` providers route `direct` from the pod under one rule that `netagent`, the resolver, and the mirror share.

Decisions raised by the revision 9 review:

29. **Per-fetch UIDs and per-fetch forwarder sockets in the mirror.** Recommendation: adopt, so a compromised git subprocess is bounded to its own fetch.
30. **A fetch retry window below the session watchdogs, with admin prefetch for large repositories.** Recommendation: adopt, with 180 s as the default.

Decisions raised by the revision 10 review:

31. **A capability exception for the mirror container** (a root entrypoint that raises `CAP_SETUID`, `CAP_SETGID`, and `CAP_CHOWN` as ambient capabilities and drops to the parent UID) so fetches run under per-fetch UIDs. Recommendation: adopt, as the one stated exception to the drop-all and non-root rules, with the `baseline` Pod Security prerequisite. User namespaces reduce the risk of the exception without removing it (see **Capability posture**). Open.
32. **Per-store ownership in the mirror**, so a compromised fetch can write one store only. Recommendation: adopt.

Decisions raised by revision 15:

33. **Level 1 enforcement in two layers**, with a decision call from `netagent` on I7 for each CONNECT and the proxy's list behind it. Recommendation: adopt, because a proxy list alone cannot apply session host lists or binding state, and the call keeps per-session audit.
34. **Level 1 as a deployment-wide choice with an acknowledgement**, per-tenant identities, and validators that refuse the features it cannot serve. Recommendation: adopt, because a silently ignored setting would present a tenant with a guarantee it does not have.
35. **No pod `ipBlock` for private destinations at the default level or level 2**, with pods and the resolver reaching them through a raw tunnel to the pinned address, a level 2 proxy required to dial pinned addresses, and Helm-rendered tenant-scoped policies at level 1. Recommendation: adopt, because a deployment-wide `ipBlock` on pods lets any tenant's agent open a raw socket to another tenant's private service. This replaces the direct resolver path revision 16 proposed.
36. **The mirror answers from its store only for listed causes and within 6 hours of the upstream's last confirmation**, and the session reports it. Recommendation: adopt. A shorter limit returns errors sooner during a host outage, and a longer one keeps sessions running on commits that may be retracted.

## Review record

Revision 4 went through two adversarial reviews on 2026-11-02, one for security and one for design substance, plus a fact-check against the specification and the code. Revision 5 records how each finding above Low or Minor was resolved. Revision 5 then went through a second round of both reviews, and revision 6 records those resolutions in the second table. Revision 6 went through a third round, recorded in the third table, revision 7 a fourth, recorded in the fourth, revision 8 a fifth, recorded in the fifth, revision 9 a sixth, recorded in the sixth, revision 10 a seventh, recorded in the seventh, revision 11 an eighth, recorded in the eighth, revision 12 a ninth, recorded in the ninth, and revision 13 a tenth, confirming round, recorded in the tenth. Findings the reviews rated Low or Minor were folded into the sections they concern without a row here.

| Finding | Resolution in revision 5 |
| :-- | :-- |
| Tenant-controlled DNS could reach another tenant's SSRF-exempted private address through Envoy's resolver (High) | Private destinations are pinned by the decision and never resolved; the address filter has no exemptions; tenant-registered public services are resolved and pinned at registration and re-checked ("Egress decision service", **Private destinations**). |
| One deployment-wide federation issuer made P4 depend on each tenant's trust policy, and AWS cannot condition on a tenant claim (High) | One issuer URL per tenant, a stable subject, a dedicated key set, and source-address binding where the provider supports it ("Credential delivery to runtimes", **`federation`**). |
| `inject` attached the secret by CONNECT authority, so a request with another `Host` on a shared edge carried it to an attacker (High) | The attach check is on the decrypted `Host`, upstream SNI is pinned, and upstream certificates never validate against the per-pod CA. |
| A tenant route could fill the shared public mirror store (High) | Store identity includes route and credential; only anonymous fetches through the platform default route share a store; refresh is bounded ("Git mirror"). |
| The per-pod CA was an unconstrained root and `netagent` wrote the whole bundle (Medium) | Name constraints to bound hosts; the controller assembles the bundle and `netagent` appends only its CA; the admission webhooks cover `netagent`'s volumes ("TLS interception", **Bundle**; "The `netagent` container"). |
| The Token Service trusted the decision service's assertion (Medium) | A second projected token with audience `lenny-token-service` is forwarded on I7 and verified by the Token Service, which re-derives the binding. |
| One pod could fail every tenant closed through shared Envoy limits (Medium) | P7 and per-pod and per-tenant quotas in the decision service; I3 admitted from Lenny proxies only; I7 on its own rate-limited port. |
| Verbatim tenant files and templates bypassed the `file` gate (Medium) | Verbatim files need the same gate; templates reference no secrets. |
| Write scope was undefined on concurrent-session pools (Medium) | The union over the pod's sessions, stated under "Local git endpoint". |
| Level 1 static credentials were a cross-tenant shared secret (Medium) | Level 1 deferred (open decision 12). |
| Service account token replay from another source (Medium) | Peer-address check on I3, I5, and I7; `expirationSeconds` 600. |
| Agent-supplied headers crossed hops (Medium) | `netagent` forwards `Host` and its own `Proxy-Authorization` only; the decision overwrites the route header. |
| Launch variables captured the adapter's platform traffic in the embedded model, and `NO_PROXY` could not name presigned URLs (Major) | The adapter disables proxying on its platform transports; `NO_PROXY` is derived from the rendered endpoints ("Runtime SDKs", **Embedded runtimes**; "Injected configuration"). |
| The gateway-to-decision-service binding feed was unspecified, including the bind-then-clone race and §7.3 fencing (Major) | Postgres notifications, read-through on a miss, a stated bound, and the generation rule ("The `netagent` container", **Binding**). |
| `GIT_CONFIG_GLOBAL` on a read-only file broke `git config --global` and hid the image's config (Major) | Lenny's rules move to `GIT_CONFIG_SYSTEM`. |
| The "identity before claim" rationale overstated the adapter's position (Moderate) | The comparison table and the recommendation are corrected; two audiences are stated. |
| Egress groups were a second proxy tier with no contract (Moderate) | Groups are additional `lenny-egress` Deployments, and bound pods dial them directly. |
| Public mirror store classification and the public-to-private transition were undefined (Moderate) | Defined under "Git mirror". |
| LFS uploads bypassed write detection (Moderate) | `netagent` reads the batch `operation`. |
| Ref resolution in the pod changed the §14 contract and cost a claimed pod (Moderate) | Resolution stays in the gateway through a Go `ls-refs` client (open decision 16). |
| A deployment-wide tunnel cap let a dead `netagent`'s tunnel outlive a recycle (Moderate) | The cap is per route and selected per pool by the decision. |
| The `proxied` policy omitted `lenny-egress-authz` (Moderate) | Added, with the resolver. |
| `netagent` decisions were invisible centrally (Moderate) | Reported over I7 with decision identifiers. |
| Proposal 1 broke `gitClone` and proposal 2 depended on an unlanded draft (Moderate) | Proposal 1 keeps the stored `token`; pool ownership moves into proposal 2. |
| Policy edits did not invalidate the decision cache (Moderate) | A policy version in the cache key. |
| Tenant client config files needed pointer variables the design had removed (Moderate) | Fixed paths with constant pointer variables in the default profile. |

Second round, on revision 5:

| Finding | Resolution in revision 6 |
| :-- | :-- |
| Egress-group admission was asserted: no per-group policy, no way for the decision to know which Envoy it serves, and a path to leave from another tenant's address (High) | Grants are Helm values; the chart renders the group's ingress policy on the tenant label; each group's Envoy adds a fixed group header and the decision refuses mismatches in both directions ("Defaults, replacement, and augmentation"). |
| Postgres notifications can be spoofed by any database client and lost on reconnect, and the pooler the spec mandates does not carry them (High, Major) | The feed is a gateway gRPC push on the gateway's mTLS identity, treated as a hint; the decision re-reads the row on every push, on every miss, and at a bounded age; generations are monotonic ("The `netagent` container", **Binding**). |
| The gateway's `ls-refs` resolver ran with no policy, followed a client-supplied URL, and attached credentials on wildcard host matches (High) | The decision applies the creating tenant's policy and routes to the gateway's request; the credential attaches within a repository prefix only; no redirects, `ref-prefix`, byte and time caps, strict parsing, and per-tenant concurrency ("Credentials", **The gateway runs no git**). |
| A resumed pod has no published binding while it materializes, so a resumed session with `gitClone` could not clone (Major) | A provisional binding written before `FinalizeWorkspace`, as a §7.3 change in proposal 2. |
| The controller cannot derive `NO_PROXY`, because it never receives the LLM proxy or OTLP hosts (Major) | The chart passes both to the controller as inputs; the adapter bypasses the proxy for presigned hosts; `netagent` logs a CONNECT to a base destination as a configuration error ("Injected configuration"). |
| Concurrent-tunnel quotas had no close signal (Medium, Moderate) | Rates centrally, per-pod concurrency in `netagent`, per-tenant concurrency from Envoy's gRPC access log stream, and a chart check that quotas fit Envoy's limits. |
| The per-route tunnel cap was static configuration while pools are created at runtime (Moderate) | A chart-rendered menu of routes that pools choose from. |
| Private-destination pinning rested on the forwarded-CONNECT form the spike tested, not raw tunnelling (Medium) | Both forms are stated; raw tunnelling is listed as owed; tenant routes name only held objects; the registration check covers the full exclusion list and the cluster's own addresses. |
| The `inject` authority check was ambiguous for absolute-form HTTP/1.1 requests (Medium) | One derived authority with its port; a disagreeing `Host` is refused. |
| Per-tenant issuers keyed by name, with unstated key lifecycle and reachability (Medium, Moderate) | Issuers keyed by tenant identifier; per-tenant keys destroyed with the tenant; rotation overlap; reserved `/oidc/` path; the public-ingress requirement stated. |
| One gate covered four risk classes deployment-wide (Medium) | Three gates, each a per-tenant platform grant plus a tenant opt-in. |
| The mirror parses hostile packs for every tenant in one process (Medium) | Sandboxed git subprocesses and optional sharding. |
| Egress-group grant changes did not re-point bound pods (Moderate) | The bounded binding re-read re-points `netagent`; open tunnels run to the pool cap, stated as residual. |
| `ls-refs` against a mirrored host lost the outage benefit and lacked a v0 fallback (Moderate) | Resolution against the mirror when the route is the mirror; v0 fallback. |
| Proposal 1 depended on proposal 2 through the resolver, and bundle assembly sat in the wrong proposal (Moderate) | The resolver moves to proposal 3; bundle assembly moves to proposal 2. |
| Tests and documentation still carried level 1 and lacked the new mechanisms (Moderate) | Updated. |

Third round, on revision 6:

| Finding | Resolution in revision 7 |
| :-- | :-- |
| The decision service relayed the pod's Token Service token, so a compromised decision service held a valid token for every active pod (High) | `netagent` requests credentials from the Token Service directly on I8, and the Token Service asks the decision service for scope the way Envoy does ("Egress decision service", **Credential requests**). |
| Pods could reach Envoy's unauthenticated admin interface and other non-token ports (High if unstated) | Port-scoped pod-side rules, Envoy admin on loopback, metrics on a scrape-only listener, and a tier 9 test (**Port scoping**). |
| Credentials attached by repository prefix without a canonical path form (Medium) | One shared canonicalizer for the resolver, session create, and `netagent`. |
| The provisional resume binding was a full binding with no stated retirement (Medium, Medium) | Read-scoped to `gitClone` sources, fenced on the generation, retired on every exit from `resuming` in the pod-release write. |
| Tenant-owned pool pods carried no tenant label, so a group-granted tenant's warm pods had no egress (Major) | The controller stamps the owner at creation, with a new label-webhook rule. |
| The binding push reached one decision-service replica (Major) | Fan-out to every replica through a headless Service, non-blocking with bounded retries, and the re-read as backstop. |
| `ls-refs` against the mirror failed for a repository it had never fetched, and the mirror had no gateway identity (Major) | The mirror forwards a bounded `ls-refs` upstream on a miss and schedules the fetch; it accepts the gateway's mTLS identity carrying the tenant. |
| Per-tenant concurrency from the access log stream drifted upward (Major) | Heartbeats from the periodic entries, expiry after three missed intervals, per-instance reset, an approximate quota with an alert, and the per-pod limit as the exact bound. |
| The LLM proxy host is a gateway flag no template renders, and a public hairpin fails on `proxied` pools (Medium) | One Helm value for both consumers, constrained to an in-cluster name, with a preflight check. |
| Gate wording, SSRF-exemption wording, the egress-group documentation row, and the gateway's `ls-refs` identity were inconsistent | Corrected throughout. |

Fourth round, on revision 7:

| Finding | Resolution in revision 8 |
| :-- | :-- |
| `netagent` learned a binding's hosts from the decision service, so a compromised decision service could point injection at an attacker's host (High) | The I8 response carries the binding's hosts and attach template from the Token Service's own store read, and `netagent` gates injection on that set; the Token Service enforces the writable set and allowlist itself, with the decision as a second gate ("Egress decision service", **Credential requests**). |
| I8 exposed the shared credential plane to every pod with no per-pod limit, no isolation from the gateway path, and no caching (High, Major) | An isolated, rate-limited TLS listener; `netagent` caches to expiry; credential calls join P7 (**The I8 listener**). |
| The Token Service's new footprint (pod watch, binding reads, ingress from agent namespaces, RBAC) was unstated (Major) | The decision service keeps the only pod watch and answers liveness on I3; binding resolution is one shared package; the ingress, egress, and RBAC changes are listed under "Specification sections affected". |
| The mirror had no stated identity or credential path for upstream fetches (Medium) | The mirror fetches on its own audience token with the store's tenant as policy context and requests credentials on I8 on its mTLS identity. |
| Token Service unavailability was covered for push only (Medium) | Fail-closed behaviour stated for every delivery mode, with cached credentials serving to expiry. |
| I7 resubscription on replica loss was unstated (Medium) | Resubscribe to any replica with the last generation, a 30-second tunnel grace. |
| A pool ownership change stranded labelled pods (Medium) | `ownerTenantId` is immutable once set. |
| I7 and I8 references, the diagram, the `proxied` peers, and the canonicalizer's proposal were inconsistent | Corrected. |

Fifth round, on revision 8:

| Finding | Resolution in revision 9 |
| :-- | :-- |
| The Token Service had no independent source of pod identity or tenant, so the second gate depended on the decision service (High, Major) | `pkg/binding` is Kubernetes-backed in both components, each with its own informer; tenant identity never comes from I3 ("Egress decision service", **Credential requests**). |
| The Token Service's I3 call could carry the pod's token (High) | The call carries the pod UID, the peer address, and the scope only. |
| The credential cache outlived the binding on recycled, service-mode, and concurrent pods (High) | The cache is keyed on binding generation and session and flushed on every binding change; stored secrets get a 15-minute cache lifetime. |
| I7 resubscription was exempt from the rate limit and read through the store (Major) | Matching-generation resubscriptions are served from cache, with their own low limit. |
| The mirror's tenant assertion on I2 and its I8 identity were unstated, and a compromised mirror could obtain every `stored` token (Medium) | An `x-lenny-tenant` header honoured only from service identities; read tokens only on I8; the parent attaches credentials on a loopback forwarder; `stored` repositories route `direct`. |
| Mirror store reads were single-gated on I3 (Medium) | The mirror resolves the caller's tenant itself and refuses a store outside it. |
| The `want`-line wait failed large first fetches (Medium) | `netagent` retries the mirror to the pool's fetch timeout, and materialization retries within its own. |
| Token Service loss was unstated for `ls-refs`, materialization, and warm fills (Medium) | Stated, each failing closed. |
| Stored secrets had no cache expiry (Medium) | A 15-minute cache lifetime assigned by the Token Service. |
| Level 1 statements survived its deferral; the architecture peer list and the Token Service store claim were stale | Corrected. |

Sixth round, on revision 9:

| Finding | Resolution in revision 10 |
| :-- | :-- |
| The mirror's sandbox did not bound a compromised git subprocess to its tenant: shared UID, readable identity material, a shared forwarder (High) | Per-fetch UIDs, identity material readable by the parent only, per-tenant store groups, and a per-fetch forwarder socket with a peer-credential check ("Git mirror"). |
| The `netagent` retry window equalled the finalize watchdog and exceeded the resume watchdog (Major) | A pool fetch retry window below both, 180 s by default, with admin prefetch and warm fill for large repositories, and new scope-table rows. |
| The `stored`-provider `direct` rule had one decision point and no counterpart in `netagent` or the resolver (Major) | One rule keyed on the pool's provider level, applied by `netagent`, the resolver, and the mirror; `mint-coarse` joins it; P2 and the mirror table are reworded. |
| The informers covered the agent namespaces only, while the service identities they check run in `lenny-system`; the mirror's own instance lacked RBAC (Medium) | Informers cover `lenny-system` too; the mirror carries the same RBAC and egress. |
| `pkg/binding` startup, outage, and scale behaviour were unstated (Medium) | Fail closed before sync, stale-cache bound on API server loss, a pod projection, and Token Service readiness kept independent. |
| The cache key missed binding definition edits (Medium) | The definition version is part of the key, and edits are pushed as binding changes. |
| The egress-group ingress policy omitted the mirror (Medium) | Admitted. |
| I3 and I8 rows and the I3 caller list were stale | Corrected. |

Seventh round, on revision 10:

| Finding | Resolution in revision 11 |
| :-- | :-- |
| Public stores shared one group, so a compromised fetch of a hostile public repository could rewrite every public store (High) | Per-store GIDs for public and tenant stores; a fetch holds only its store's group; public-store `upload-pack` runs with no group ("Git mirror", **Per-store ownership**). |
| Per-fetch UIDs needed capabilities every `lenny-system` workload drops, so the sandbox could not be built (High, Major) | A stated capability exception for the mirror container with a custom seccomp profile, §13.1 and §17 rows, a preflight allowlist, and user namespaces as the later replacement (**Capability posture**). |
| The retry-window check covered the finalize bound only, ignored the setup bound and the resume watchdog, and applied per source (Major) | One window per materialization, validated against both watchdogs less the setup bound. |
| The window-expired fetch reused a plan-bind error code (Medium) | It surfaces at finalize as `SESSION_CREATION_FAILED` with reason `git_fetch_pending`. |
| The Token Service released mirror read tokens on pool match alone (Medium) | The tenant's git route must also name the mirror for that host. |
| The forwarder pinned the credential but not the destination (Medium) | It pins the upstream host and repository path. |
| Projected tokens cannot renew without the API server, so all egress fails closed about 600 s into an outage (Medium) | Stated as a residual with an alert. |
| Namespace wording, the audience list, the mirror egress list, and the missing prefetch API were inconsistent | Corrected, and the prefetch resource is added to the scope table, §15, the documentation plan, and the tests. |

Eighth round, on revision 11:

| Finding | Resolution in revision 12 |
| :-- | :-- |
| Without `fsGroup` the mirror's I8 client key was either unreadable by the parent or readable by every fetch UID (High) | `fsGroup` stays with `fsGroupChangePolicy: OnRootMismatch`; the key is group-readable at 0440 by the parent only, because fetch UIDs hold only their store's group ("Git mirror", **Per-store ownership**). |
| Kubernetes never grants added capabilities to a non-root entrypoint, so the sandbox could not switch UIDs (Major) | A root entrypoint raises three ambient capabilities and drops to the parent UID; the exception covers the non-root rule as well, with a `baseline` Pod Security prerequisite and a preflight check that now covers StatefulSets (**Capability posture**). |
| The custom seccomp profile was unnecessary and had no shipping path (Major) | Removed; RuntimeDefault permits the calls. |
| The retry window ignored the resume rebuild's setup commands and the Runtime's setup bound (Major) | The window applies to initial materialization only, validated against the finalize bound less the Runtime's setup bound; the resume path waits 30 s at most. |
| Files written by a fetch UID were not writable by a later fetch, and a reused UID kept owner bits (Medium) | `core.sharedRepository=group` and a re-chown to the store owner after each fetch. |
| A tenant's own agent could poison its tenant store through `upload-pack` (Medium) | A per-store reader group through a POSIX default ACL, with the residual stated where ACLs are absent. |
| The `lenny-system` Pod Security level was unstated for the exception (Medium) | Stated as a prerequisite with a preflight row. |
| Prefetch had no storage bound; §6.2, the proposal assignment, and the Git access page were incomplete | Corrected. |

Ninth round, on revision 12:

| Finding | Resolution in revision 13 |
| :-- | :-- |
| A descendant left by a compromised fetch could pass the forwarder's peer check once its UID was reused for another tenant (High) | Fetch UIDs are never reused within the parent's lifetime, and every process under a UID is killed before the UID retires ("Git mirror", **Per-store ownership**). |
| The ambient set is cleared on a root-to-non-root switch, so the stated capability order carried nothing (Medium, Medium) | The order is keep-caps, switch with groups kept, restore, then raise, as `setpriv` does (**Capability posture**). |
| The post-fetch re-chown walked a tree the fetch controlled with `CAP_CHOWN` (Medium) | An fd-relative `lchown` walk that changes owners only, under the store's GID. |
| The validator's outcome on a Runtime update was unstated (Medium) | A Runtime update that breaks a referencing pool's window is refused, with the derived-runtime maximum and the §6.2 default stated. |
| Stale watchdog wording in the scope table and tier 8, §7.3 missing from the affected sections, and the Pod Security label gap | Corrected. |

Both ninth-round reviews judged the design ready to split into proposals once these corrections were made.

Tenth round, confirming, on revision 13:

| Finding | Resolution in revision 14 |
| :-- | :-- |
| The UID sweep was not tied to fetch exit, and the parent holds no `CAP_KILL` (High as written) | The sweep runs at fetch exit, before the re-chown and completion, through a helper that switches to the fetch UID and signals its own UID, with the parent as child subreaper ("Git mirror", **Per-store ownership**). |
| A stored-secret `inject` binding on a git host bypassed the `storedTokenPush` grant and path enforcement (Medium to High) | Such bindings are refused at create, and P2 names the stored-API-secret exception ("Credential bindings"). |
| The ambient raise needs the capability in the inheritable set (precision) | Stated in the capability order. |
| The §28 label range said I1 to I7 | Corrected to I8. |

Both tenth-round reviews found no Major or High issue beyond these and judged the design sound enough to proceed to proposals.

Revision 15 applied the owner's decisions and these corrections, which answering the owner's questions surfaced:

| Finding | Resolution in revision 15 |
| :-- | :-- |
| The doc said user namespaces remove the mirror's capability exception, and they relax only the non-root rules | Corrected, with their node requirements and ID range (**Capability posture**). |
| A mirror holding a repository answered `ls-refs` from its store, so `main` could resolve to an old commit | The mirror asks the upstream first and uses the store only when the upstream is unreachable. |
| The label the gateway stamps at bind could reach an informer after the pod's first request | A retryable not-yet-visible state in `pkg/binding`. |
| `ownerTenantId` left `unset → owner` open on a pool with warm pods | The owner can be set only at pool creation. |
| Round 6: connection reuse skipped the second tenant's decision, GitLab's suffixless path redirects, v0 advertisements exceed 1 MiB, and Bitbucket Cloud does speak v2 | One tunnel per call, a single `.git` redirect, a 32 MiB streaming v0 cap, and the Bitbucket statement corrected. |

Eleventh round, on revision 15:

| Finding | Resolution in revision 16 |
| :-- | :-- |
| The probe never tested authentication, so an agent could use a proxy that ignores Basic with no credential and bypass `netagent` (High) | The probe requires 407s, a positive control, and a separation check, and a source-address proxy does not meet the contract. |
| The shared credential could cross the network in cleartext (Major) | The contract requires a TLS proxy listener. |
| The 25-hour bound and P6 depended on the sync job, with nothing detecting its failure (Major) | Canary checks, and both statements made conditional. |
| Tenant-controlled host strings reached deployer proxy configuration, and an open ceiling had no representation (Major) | A strict host grammar on write and generation, opaque identity names, `allowAll` with `denyHosts`, and a reference renderer. |
| Private git endpoints failed at level 1 while the text called them unaffected (Major) | A direct path for the resolver at every level and for the mirror at level 1. |
| Identity release per pod state was undefined, and warm-up additions were missing from the lists (Major) | A state table derived by the Token Service, and the lists corrected. |
| The mirror's store fallback contradicted its I3 gate during a decision service outage (Major) | The fallback covers upstream and `lenny-egress` outages only. |
| The fallback was unbounded in age, invisible on the session, and triggerable across tenants by volume (Major) | A 6-hour limit, `resolvedFromStore` on the session, coalescing, and 429 excluded. |
| Silent Envoy assumptions, an incomplete limits table, and sync lag mapped to permanent errors (Medium) | I3, I8, and network rows corrected; tunnel lifetime and warm rows added; a `netagent` server-name check and tunnel reports added; publish before release, and a proxy refusal after an allow is transient. |
| Failure classification gaps, including a chained gateway's 502 reading as a host fault, and "adds no outage" overstated (Medium) | An explicit list of fallback causes, CONNECT 429 and 502 mapped, and the dependency's scope restated with its exceptions. A `want` miss schedules a fetch. |
| Service mode escaped the not-yet-visible state, and pool delete and recreate was unguarded (Medium) | A durable pin record, a refusal while old pods remain, and a cascade on deletion. |
| The user namespace statement omitted OCI runtime floors and `idsPerPod` (minor) | Added. |

Twelfth round, on revision 16:

| Finding | Resolution in revision 17 |
| :-- | :-- |
| Service-mode pins had no arbiter across gateway replicas, so one tenant's request could reach a pod pinned to another (High; the code defect exists today) | A conditional insert on pod UID decides the tenant, the loser reroutes, a write failure aborts, and a reconciler retries the label. Proposal 2 closes it. |
| Pod `ipBlock` entries crossed tenants, so any tenant's agent could open a raw socket to another tenant's private service (Major; this also affected the default level) | Pods hold no such entry at the default level or level 2, which use Envoy's raw tunnel, now validated, and level 1 uses tenant-scoped policies. Revision 16's direct resolver path is withdrawn at those levels. |
| Level 1 could not install or probe as written, because preflight runs before any gateway exists (Major) | Preflight checks TLS and authentication only, and the full probe runs after install and gates pool fill, with the `lenny-ops` row and credential path added. |
| Pool recreation could not work, because pool names are a primary key and deletion is soft (Medium) | A new owner means a new pool name, and unlabelled pods are refused on tenant-owned pools. |
| Store fallback state was incomplete, and two statements contradicted the age limit (Medium) | Confirmations are defined and persisted, negative answers clear them, an unavailable mint is listed separately from a refused one, and the statements are corrected. |
| An agent could trigger upstream fetches by looping on random SHAs (Medium) | A negative cache, a per-tenant rate, and a minimum interval. |
| The identity table gave a pinned recycled pod the warm identity, against §5.2 (Medium) | The warm row is limited to pods with no pin. |
| The level 1 mirror `ipBlock` falsified the sandbox's network statement, and the host grammar admitted literals (Medium) | The residual is stated, and a numeric final label is refused. |

Thirteenth round, on revision 17:

| Finding | Resolution in revision 18 |
| :-- | :-- |
| A non-Envoy level 2 proxy had no way to reach a pinned address (Major) | Pinned dialing joins the level 2 contract, and validators refuse private destinations for a proxy that declares it lacks it. |
| The pin record did not cover the IP-keyed router or a gateway rollout (Major) | The router keys on pod UID, reads records on a miss, and adopts a same-tenant conflict. |
| A tenant identifier could be an invalid label value, which fails the rendered NetworkPolicy (Major) | The §10.2 pattern narrows. |
| Level 1 grants had two sources, and level 1 covered git only (Medium) | Grants are Helm values at level 1, and `netagent` dials granted private destinations on both endpoints. |
| Network rows and the probe result's location were missing (Medium) | The binding push port, the Token Service row, and a ConfigMap the controller reads. |
| The first pin raced the decision cache (Medium) | Record, push, and label precede the first request, and unlabelled service-mode pods are not cached. |
| The record's lifecycle, scope, and Postgres dependency were unstated (Medium) | Stated, with its outage behaviour. |
| Mirror confirmations were too coarse, a refused mint cleared nothing, and a negative entry could fail a new pinned commit (Medium) | (ref, SHA) pairs, clearing on a refused mint, and a 60-second entry a fetch clears. |
| A label-selected `proxied` rule was a raw path to deployer git services, and untested push had left the owed list (Medium) | The rule covers the mirror only, and push is listed as owed. |

## Validation record

First spike, which was research only:

- Client proxy support: credentials in proxy URLs failed for Java, headless Chromium, and Yarn berry, and for Node unless a flag is set.
- Corporate gateways: Zscaler enrolls each proxy user individually, and the other vendors identify workloads by source address and identity headers.
- GitHub App tokens can be limited to named repositories and permissions, for one hour.
- No other host combines one-repository scoping with short lifetimes, apart from CodeCommit.
- Artifactory and Nexus do not serve git fetch.
- `insteadOf` rewrites pushes.
- Protocol v2 serves any object that exists by SHA.
- CNI behaviour for ClusterIP policy differs between implementations.
- Projected service account tokens are pod-bound and file-delivered.
- Lenny facts: §5.1 forbids secrets in env, the supervisor renders env as command-line arguments, `lenny/vcs_token` returns stored tokens without scope checks, and F-14.1.37 breaks the gateway clone.

Second spike, which ran hands-on tests and further research:

- Envoy 1.40.2 and 1.39.6: CONNECT, ext_authz on CONNECT with 403, 407, and fail-closed 503, and decision metadata in logs all passed. SSRF filtering passed for hostnames and literals once the mapped ranges were added. `max_stream_duration` passed on CONNECT, while `max_connection_duration` failed. Chaining passed through forwarded CONNECT with an `ORIGINAL_DST` cluster, while `http_11_proxy` failed. Throughput reached about 2.7 GB/s.
- Squid 7.7: authentication, the external ACL helper, SSRF filtering, per-tenant parents, and the server-name check passed, with workarounds. Ending active tunnels failed.
- Smokescreen-based Go proxy: every requirement passed, including kill by lease. The test found two pitfalls.
- git 2.49 and git-lfs 3.6: the generic rewrite, `pushurl` behaviour, submodules, `go get`, the streaming prototype, partial and shallow clones, chunked push, redirect rewriting, LFS URL derivation, and git's tree protections all passed. Go's `net/http/cgi` failed for streaming output and for chunked pushes.
- Research:
  - GitHub token caching guidance and rate limits.
  - Calico and GKE post-DNAT policy matching.
  - Corporate gateway identity models.
  - ext_authz as the decision protocol, and AuthZEN's status.
  - EDL as an allowlist format.
  - CA trust variables by ecosystem.
  - Clients sending hostnames inside CONNECT.
  - Token file formats, and `gh` requiring HTTPS.
- Lenny internals:
  - The container and UID layout.
  - The embedded model.
  - The resource class applied to every container.
  - The ordering of warm-up, claim, and credential delivery.
  - Service mode receiving no lease.
  - The archive gate checks.
  - Tenant-admin permissions.
  - The current egress settings.

Third round, which ran hands-on tests and further research:

- **Envoy 1.40.2 and 1.39.6:**
  - The baseline passed again: CONNECT, gRPC ext_authz with allow, 403, 407, and fail-closed 503, the SSRF filter on 21 non-public targets, `max_stream_duration`, and chaining through ext_authz metadata.
  - ext_authz in HTTP mode passed over HTTP/2 only.
  - IPv6 passed.
  - DNS rebinding never reached a private address.
  - Load passed after raising the circuit breakers: 1,000 concurrent tunnels and 2,000 new CONNECTs per second with decisions.
  - Slow and failed decision services failed closed.
  - Upstream 407 and 403 replies were normalized with `custom_response`.
  - The server-name check through an internal listener passed, with the ECH caveat.
  - Two findings became requirements: the pod's `Proxy-Authorization` leaks to chained upstreams unless the decision removes it, and the default circuit breaker caps each cluster at 1,024 tunnels.
- **Kubernetes 1.35 on kind:**
  - Token isolation to `netagent` held, including under a shared process namespace, which §13.1 forbids and which served as a control.
  - Offline validation rejected a deleted pod's token about 70 ms after the watch event, against about 6 s for TokenReview.
  - Rotation happened at 80 to 88 percent of the lifetime.
  - Native sidecar ordering and restart isolation held.
  - The NXDOMAIN resolver for `proxied` pods worked with curl, Python, pip, Node, and Go.
  - kindnet enforced NetworkPolicy through a ClusterIP but not toward node-local endpoints.
- **Client tools:**
  - Maven 3.9 and 4.0 need `settings.xml` through `MAVEN_ARGS`.
  - Gradle 8 and 9 honour `JAVA_TOOL_OPTIONS`.
  - `gh`, `glab`, npm, and pip re-read their credential files on every invocation.
  - `glab` requires mode 0600 and supports plain-HTTP API calls.
  - `gh` refuses to run without a token.
- **Research:**
  - Secure web gateways and identity on undecrypted CONNECT.
  - GitHub minting limits.
  - Calico eBPF and Cilium post-NAT policy, from source.
  - Squid `external_acl_type` status.
  - Prior art for credential injection.
  - Envoy CONNECT maturity and advisories.
  - SSH and HTTPS URL forms per git host, checked with `git ls-remote`.
  - Exposure of environment variables.
- **Credential injection**, run with mitmproxy 12.3.1 as the stand-in for `netagent`, after a first attempt to write a custom interceptor stopped on a safety classifier:
  - mitmproxy intercepted only the bound hosts. Other hosts passed through as opaque tunnels, keeping their public certificate, and a placeholder sent to them arrived unchanged.
  - The header was overwritten on every request to a bound host, with or without a placeholder. A placeholder in the query was not replaced.
  - The plain-HTTP upgrade worked with no CA on the client.
  - These clients authenticated with placeholders only, while the upstream received the real secret:
    - curl
    - Python requests, httpx, and aiohttp
    - Node 24 fetch
    - Go
    - Java 21 HttpClient, over HTTP/2
    - `gh` 2.103.3, with `GH_ENTERPRISE_TOKEN`
    - npm, through `.npmrc`
    - pip, through a Basic placeholder in the index URL
  - Response scrubbing behaved as described under "Credential delivery to runtimes".
  - The CA kept its fingerprint across a restart.
  - Interception added about 2.7 ms at p50 per new connection.
  - curl reads only lowercase `http_proxy` for `http://` URLs, so the client profile keeps both cases.
- **Credential formats:**
  - The AWS container credentials endpoint worked with boto3 1.43, AWS CLI v1 and v2, and the AWS SDKs for Go v2, JavaScript v3, and Java v2. Each client fetched from a loopback endpoint and signed with the credentials. Each re-read a changed token file, never signed with an expired credential, and rejected a non-loopback plain-HTTP URI. A trailing newline in the token file broke every client.
  - `.netrc` support was uneven:
    - requests, pip, and Go read it by default and honour `NETRC`. Go uses it only over HTTPS.
    - git and GNU wget read `$HOME/.netrc` only.
    - curl needs `--netrc`.
    - httpx needs `NetRCAuth`, which demands mode 0600.
    - BusyBox wget, npm, Node fetch, and Java ignore it.
- **Envoy resilience, soak, and drain** (round 5): keepalive, outlier detection, active health checks, and one retry gave zero failed CONNECTs under endpoint kill and freeze; 200 long streams ran 20 minutes with flat memory; drain behaviour and the retry per-try timeout pitfall were characterized.
- **Production CNIs** (round 5, two-node kind): Calico 3.33 iptables, Calico 3.33 eBPF, and Cilium 1.20 with kube-proxy replacement enforced the `proxied` policy identically, allowing the eight permitted paths and dropping all thirteen denied destinations, including node-local ones.
- **Federation and metadata formats** (round 4, hands-on against fake provider endpoints):
  - AWS web identity from a config file alone: boto3, and the Go v2, JavaScript v3, and Java v2 SDKs assumed the role, signed, and re-read a changed token on refresh. Endpoints can also come from the file.
  - GCP external account: google-auth for Python, Node, and Go exchanged the token, with impersonation, and re-read it. Python and Node look up the project at Cloud Resource Manager during initialization.
  - Azure workload identity: azure-identity for Python, Node, and Go exchanged the token. The authority must be HTTPS, Node and Go need instance discovery disabled against a non-standard authority, and the tenant and client identifiers come from the environment or constructor arguments only. The token file is cached for 5 to 10 minutes.
  - GCE metadata emulation: Python, Node, and Go worked, with Python needing `GCE_METADATA_IP` as well.
  - Azure App Service managed identity emulation: Python, Node, and Go worked with one request each.
- **SDK review** (read-only):
  - The Go, Python, and TypeScript runtime SDKs contain no environment, subprocess, proxy, CA, or git handling.
  - The supervisor builds the launch environment from an empty one.
  - Embedded binaries exit on any controller flag they do not define.
  - Several gaps between §15.7 and the SDKs are listed under "Runtime SDKs".

Sixth round (2026-11-02), on the egress path for ref resolution and on level 1:

- **`ls-refs` through Envoy 1.40.2.** A Go client returned the same commit as `git ls-remote` from GitHub (branches and peeled annotated tags), GitLab, Bitbucket Cloud, Gitea, Forgejo, kernel.org, and sourcehut, with an absent ref and an absent repository classified correctly. No test used credentials, and the Azure DevOps sample repository answered 401, so private repositories and Azure DevOps remain owed.
- **Failures,** at 25 calls per second with one retry: one decision endpoint killed, 0 of 375 failed; one Envoy stopped, 0 of 300; one Envoy frozen, 0 of 300; a graceful drain, 0 of 375. With no retry, one stopped Envoy failed 88 of 300, so the retry is required. With every decision endpoint down, calls failed closed in 0.5 s (1.0 s with the retry); with both Envoys down in 2.0 s; and with both frozen in 3.0 s.
- **Hostile hosts.** An unbounded advertisement stopped at the byte cap in 16 ms, slow and hanging hosts stopped at 10 s, and redirects, malformed pkt-lines, and oversized pkt-lines were refused.
- **Raw tunnel to a pinned address.** Envoy terminated the CONNECT and tunnelled raw bytes to an `ORIGINAL_DST` cluster that read the address from the decision's dynamic metadata, for a name with no DNS record. The granted tenant reached the origin and another tenant got a 403. The byte cap held inside the tunnel, stock git 2.49 ran `ls-remote` and a shallow clone of 75 MiB through a pinned address, and with the decision service down the call failed closed. Push through the tunnel was not run.
- **Level 1 on stock Squid 7.7,** configured from a rendered policy document: per-identity lists held in both directions, the resolver worked through it, and the results under "Level 1 replacement" for credentials, SSRF, the server-name check, revocation, and rotation were observed there.

The spike files are under the session scratchpad directories `spike-lsrefs`, `spike-level1`, `spike-envoy`, `spike-envoy-r3`, `spike-proxy-alts`, `spike-git`, `spike-k8s`, `spike-tools`, `spike-inject`, `spike-formats`, `spike-federation`, `spike-cni`, `spike-envoy-r4`, and `research-r3`. They are throwaway and not part of the repository.

## Validation still owed

- **`inject` delivery in Go:** the same matrix against the Go `netagent` once it exists, plus httpx over HTTP/2, a rustls client, and memory under many concurrent intercepted streams. The round 3 numbers come from mitmproxy, a Python process, and do not represent the production component.
- **Corporate gateways:** a hands-on test against at least one gateway in a deployer's environment, covering source-address identity through an egress group, and whether that gateway reads `X-Authenticated-User` on CONNECT. Public documentation does not settle the second point.
- **GitHub:** whether installation token mints count against the 2,000-per-hour limit, measured against a test App or confirmed by GitHub support.
- **Protocol v2 `ls-refs` in Go** with credentials on private repositories, against Azure DevOps and AWS CodeCommit, and against the mirror. Round 6 covered public repositories on the other hosts.
- **Push through the raw tunnel,** which round 6 did not run; it ran fetch and clone only.
- **Level 1** against a commercial proxy, an EDL consumer, and the Go `netagent`, which replaces the curl and Go clients round 6 used. The TLS proxy listener and the ClientHello check have not been run, and round 6 ran the authentication, address, and rotation checks by hand rather than as the probe.
- **Envoy gRPC access log service** as the tunnel-close feed: delivery under load and on Envoy restart.
- **Name-constrained per-pod CA** acceptance by the client matrix, since some TLS stacks handle name constraints on a root differently.
- **The mirror's ambient-capability start** in a container with `allowPrivilegeEscalation: false`, RuntimeDefault seccomp, and `fsGroup` with `OnRootMismatch`: a root entrypoint raising three ambient capabilities, switching to the parent UID, forking fetch children under other UIDs with a single store group, re-chowning after the fetch, and a POSIX default ACL reader group on the volume types the chart supports.
