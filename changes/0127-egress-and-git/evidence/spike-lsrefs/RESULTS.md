# ls-refs through the egress path (round 6, 2026-11-02)
Client: go/lsrefs (v2 ls-refs, v0 fallback, 1 MiB and 10 s caps, no redirects). Envoy 1.40.2 with the round 5 resilience config; stand-in decision service.
Raw output: t1.out (real hosts), t2.out (hostile, reuse, latency), t3.out and t4.out (failures; t4 is after the classification and connect-budget fix).
- SHA equal to `git ls-remote`: GitHub (branch, annotated tag peeled), kernel.org, Codeberg/Forgejo, Gitea, Bitbucket Cloud (speaks v2), sourcehut, GitLab with .git.
- GitLab without .git answers 301. Azure DevOps sample repo answered 401, so Azure DevOps is untested. No test used credentials.
- v0 advertisement exceeds 1 MiB on kubernetes/kubernetes and gitlab-runner. An early-sorted ref matches before the cap; a late tag does not.
- Envoy adds about 0.3 ms locally; on real hosts the difference is within host variance (280-390 ms p50). 4,271 calls/s at c=200, p99 130 ms, on a laptop in Docker.
- Tunnel reuse: keepalive=true, 20 alternating acme/globex calls -> 1 decision, acme only. keepalive=false -> 20 decisions, attributed correctly.
- Failures (25 calls/s, one retry, 1.5 s CONNECT budget): one decision endpoint killed 0/375 failed; one Envoy stopped 0/300 (worst 1.0 s); one Envoy frozen 0/300 (worst 1.5 s); graceful drain 0/375. Without retry, one Envoy stopped: 88/300 failed.
- All decision endpoints down: transient at 0.5 s (1.0 s with retry). Both Envoys down: transient at 2.0 s; frozen: 3.0 s. Recovery within seconds.
- Hostile: unbounded -> byte cap in 16 ms; slow and hanging -> time cap at 10 s; redirect refused; malformed and oversize pkt-lines refused.
- Go's dial timeout satisfies errors.Is(err, context.DeadlineExceeded), so a naive classifier reports a proxy failure as a host time cap. The CONNECT status decides.

# Raw tunnel to a decision-pinned private address (round 6). Raw output: raw/t.out
CONNECT terminated by Envoy with connect_config, ORIGINAL_DST cluster reading ext_authz dynamic metadata. The name had no DNS record. The granted tenant reached the origin, another tenant got 403, the byte cap held inside the tunnel, stock git 2.49 ran ls-remote and a shallow clone against github.com through a pinned address (75 MiB), and with the decision service down the call failed closed.
