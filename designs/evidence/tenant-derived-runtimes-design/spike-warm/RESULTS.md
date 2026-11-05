# Warm clone spike (2026-11-05)

Owed by `tenant-derived-runtimes-design.md` "Not verified": the move with absolute alternates, fetch by SHA through `netagent` on each host, and whether negotiation offers the store's commit as a have.

Environment: Docker Desktop, arm64, git 2.49.1 (Alpine, OpenSSL) in `spike-tls-git`. No Go `netagent` exists. `proxy-main.go.txt` stands in for its git path: a CONNECT endpoint that terminates TLS for every host under a per-run CA, re-originates to the host against public roots over HTTP/1.1, injects no credential, and logs each request with its want and have counts (`raw/requests.jsonl`). Public repositories only, anonymous.

Per host, `t.sh fill` shallow-fetched the tip by SHA (depth 12), took A as up to ten commits back, and filled a bare store with A's full history by SHA as its only ref (`refs/lenny/warm`), plus a copy with the ref deleted. `t.sh session` mounted the store read-only, used a tmpfs `/tmp` and a separate `/workspace` volume, so each move crossed filesystems, and ran: A from the store with no fetch, a fetch of the tip over alternates, a baseline fetch with no alternates, a fetch over the ref-less copy, and a depth-1 fetch by SHA.

| Host | Protocol | Fill (A, non-tip, by SHA) | Delta pack KiB | Baseline KiB | Haves | Ref-less store KiB |
|:--|:--|:--|--:|--:|--:|--:|
| GitHub | v2 | ok | 52.6 | 329.1 | 16 | 329.1 |
| GitLab.com | v2 | ok | 26.8 | 7133.3 | 16 | 7135.8 |
| Bitbucket Cloud | v2 | ok | 45.1 | 3876.6 | 16 | 3876.0 |
| Codeberg (Forgejo) | v2 | ok | 59.3 | 12680.9 | 16 | 12684.0 |
| Gitea.com | v2 | ok | 482.2 | 8578.8 | 16 | 8595.2 |
| sourcehut | v2 | ok | 13.7 | 87.7 | 16 | 87.7 |
| kernel.org | v2 | ok | 56.1 | 1829.1 | 16 | 1829.2 |
| Azure DevOps | v0 only | ok | 11.5 | 12.1 | 1 | 12.1 |

- Every host served a fetch by SHA of a tip, of a non-tip ancestor, at depth 1, and at depth 12, through the terminating stand-in.
- Azure DevOps sent a v0 advertisement to a v2 request, and git fell back. Reachable SHAs are served under v0. The Azure repository is a 12 KiB wiki, so its delta shows only that the have was honoured.
- The first have was the store's commit A on every host. With the ref deleted, git sent no haves and every host resent the whole history, which confirms that the store needs its commit as a ref.
- A commit in the store was found with `cat-file -e` and checked out with no fetch.
- After a cross-filesystem move, every repository kept its absolute alternates path, `git status` was clean, `fsck --connectivity-only` passed, and a commit succeeded with the store read-only.

Not covered: a real `netagent`, HTTP/2 between git and `netagent`, a minted credential or private repository, a `required` pool, git builds on GnuTLS, large repositories and checkout time, and restore (capture copy and restore completion).
