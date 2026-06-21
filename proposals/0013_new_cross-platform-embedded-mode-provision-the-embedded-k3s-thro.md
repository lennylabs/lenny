# Proposal: Cross-platform Embedded Mode — provision the embedded k3s through Docker on macOS and Windows

- **Status:** Implemented (2026-06-20) on branch `impl/0013-cross-platform-embedded-mode`. Applied to spec (2026-06-19); verified, converged after 4 adversarial review rounds (3 findings fixed). Code landed across 10 build steps plus verify-phase fixes (23 commits, 62 files, +7451/-839); `pkg/embedded/...` cross-compiles for Linux and Windows, the unit and component tiers are green, and the whole-change design review is clean. The tier-4 integration smoke (`TestEmbeddedModeSmoke`) does not complete on this host because the seeded reference runtimes are placeholder-pinned images that are not pullable — the F-17.4.12 fixture limitation, an environment dependency rather than a regression. Open decision (§8): the Docker-backed launcher rolls its own `docker run rancher/k3s` to keep the prerequisite at exactly Docker, per the proposal's recommendation.
- **Date:** 2026-06-19.
- **Scope:** Extends §17.4 Embedded Mode to run a real embedded k3s on macOS and Windows by provisioning it as a container under Docker Desktop's Linux VM, with Docker accepted as a stated prerequisite on those hosts. The Linux path keeps the in-process managed-child-process launcher unchanged. The mandate restores Embedded Mode's "same platform code path as production" value on non-Linux hosts, which the current `runtime.GOOS == "linux"` gate (`pkg/embedded/k3s/k3s.go:41-43`) withholds: on a non-Linux host `pkg/embedded/stack/stack.go:205,223-226,267` skips the embedded cluster, the CRD install, and the production controllers, and the host falls back to a controller-simulator (Source/Compose Mode) that does not exercise the Kubernetes code path. This proposal stages spec edits to §17.4, §17.9.6, §15.4.3, §15.4.5, and §24, plus local-fidelity disclosures. It stages no code. The OS branch is confined to the substrate-provisioning layer (the `pkg/embedded/k3s` launcher selection and the `SupportedPlatform()`/`k3sEnabled` gate in `pkg/embedded/stack`); the gateway, controllers, CRDs, storage interfaces, and the §4.7 placement/adapter/mTLS path stay byte-identical across operating systems, so the no-mode-dependent-business-logic-split invariant holds.

This document stages the proposed spec changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off. The code blast radius is recorded in §4 (Non-goals) and §6 for the implementation step; no code is staged here.

## 1. Problem

§17.4 documents Embedded Mode (`lenny up`) as the primary path for deployers and operators evaluating Lenny on a workstation and for "anyone who wants a functioning deployment on their laptop" (`spec/17_deployment-topology.md:144,154`). Its defining value over Source Mode and Compose Mode is the "Same platform code path as production" promise: Embedded Mode runs the production gateway, controllers, CRDs, and storage interfaces against a real embedded Kubernetes with "no mode-dependent code splits in business logic" (`spec/17_deployment-topology.md:168,211,280`; `spec/15_external-api-surface.md:2360`). That value is unreachable on macOS and Windows.

### 1.1 The Linux-only gate withholds the real-Kubernetes path on macOS and Windows

`SupportedPlatform()` gates on `runtime.GOOS == "linux"` (`pkg/embedded/k3s/k3s.go:41-43`) because k3s links a Linux container runtime and runs as a managed child process (`k3s.go:3-19`). On a non-Linux host the stack skips the embedded cluster, the CRD install, and the production controllers entirely (`pkg/embedded/stack/stack.go:205,223-226,267`); the host prints "embedded Kubernetes is unavailable on this host (k3s requires Linux)" and "session placement is unavailable" (`stack.go:213-214,224-225`). The only cross-platform fallbacks reach a controller-simulator over a Docker container (Compose Mode, `spec/17_deployment-topology.md:222`) or a controller-sim with no Kubernetes at all (Source Mode, `spec/17_deployment-topology.md:197-202`), and neither exercises the real Kubernetes code path.

The project owner has decided Embedded Mode MUST work on macOS and Windows by running real k3s inside Docker Desktop's Linux VM, with Docker accepted as a stated prerequisite on those hosts. The spec does not currently mandate macOS/Windows parity. Adding that mandate is what this proposal does.

### 1.2 The spec text contradicts the intended per-OS substrate in several places

Several spec sites assume the full real-Kubernetes stack runs on every host, or assert zero external dependencies, or carry stale "in-process" wording that is already inaccurate against the current Linux launcher:

- The §17.4 components table k3s row reads "Downloaded on first `lenny up` into `~/.lenny/k3s/` and started in-process" (`spec/17_deployment-topology.md:160`). The current Linux implementation runs k3s as a managed child process rather than in-process; the package doc-comment itself states "In-process embedding of k3s is not feasible" (`k3s.go:10`). The row also describes no macOS/Windows substrate.
- The §17.4 prerequisite prose asserts "No Kubernetes cluster, no Postgres operator, no cert-manager, no OIDC provider required beforehand" with no Docker qualifier (`spec/17_deployment-topology.md:154`), and §17.9.6 says Embedded Mode "requires zero external cloud or cluster dependencies" (`spec/17_deployment-topology.md:1534`).
- The "Same platform code path as production" paragraph asserts "Only the driver selection differs" (`spec/17_deployment-topology.md:168`). Under this proposal a second axis of variation exists (host OS / substrate launcher), so the word "Only" becomes inaccurate.
- §24's thin-client exception list states `lenny up` runs "the gateway, controllers, embedded Postgres/Redis, and k3s in-process" (`spec/24_lenny-ctl-command-reference.md:14`), and the §24.19 `lenny up` row says "Start the embedded k3s / Postgres / Redis / KMS / OIDC / gateway / controllers / reference runtimes stack" (`spec/24_lenny-ctl-command-reference.md:260`). Both assume the full stack runs on every host, which the Linux-only gate does not deliver; line 14 also carries the stale "in-process" framing.

### 1.3 The runtime-author abstract-socket restriction is over-broad under a Docker-backed substrate

§15.4.3 states abstract Unix sockets are a Linux kernel feature not supported on macOS, that Standard- and Full-level runtime development therefore requires a Linux environment, and recommends Compose Mode for macOS developers (`spec/15_external-api-surface.md:2074`). The §17.4 macOS note repeats the same restriction (`spec/17_deployment-topology.md:345`). Under the Docker-backed substrate, the Embedded-Mode runtime-author flow runs the agent as a real in-cluster pod (`spec/17_deployment-topology.md:283-318`: `docker build`, `lenny image import` into the embedded containerd store, registered runtime, pool warms a pod), so the intra-pod abstract sockets run on a Linux kernel inside the Docker VM even when the host is macOS or Windows. The two restriction sites are over-broad for Embedded Mode once the substrate runs everywhere, and a §15.4.5 cross-platform note added without reconciling them would create a self-contradiction.

### 1.4 Local fidelity has limits that hold on any local substrate, including Linux

A local single-node cluster (Docker-backed or Linux) cannot reproduce the full production isolation and network surface, and §17.4 carries no disclosure of this. The `sandboxed` (gVisor) profile degrades to runc because the embedded k3s installs no gVisor RuntimeClass / `runsc` containerd-shim; gVisor is a userspace kernel needing no nested virtualization, and dev mode falls back to runc (`spec/05_runtime-registry-and-pool-model.md:681,703-704`). The `microvm` (Kata) profile degrades because it requires hardware virtualization a Docker-Desktop VM or single-node substrate cannot nest (`spec/05_runtime-registry-and-pool-model.md:682`). Separately, the §13 default-deny NetworkPolicy egress isolation (`spec/13_security-model.md:11,33`) is not exercised locally because the embedded k3s runs `--disable-network-policy` (`k3s.go:214`). Conflating gVisor and Kata under a single "nested virtualization" cause would itself be a spec defect.

## 2. Decisions

- **Extend the spec to mandate cross-platform Embedded Mode, then implement.** This is the settled project-owner direction. The spec edits are the proposal's deliverable; the code blast radius is recorded for the implementation step and not staged here.
- **Provision the embedded k3s per host OS.** Keep the in-process managed-child-process launcher on Linux (`k3s.go:161-184`) so the zero-dependency Linux experience does not regress. Add a Docker-backed launcher on macOS and Windows that runs the same pinned k3s version (`Version = v1.31.4+k3s1`, `k3s.go:37`) with the same cluster-disabling flags `serverArgs()` already builds (`--disable traefik`, `--disable servicelb`, `--disable-cloud-controller`, `--disable-network-policy`, `--flannel-backend host-gw`, `k3s.go:211-215`), so the macOS/Windows cluster is the same k3s distribution as the Linux embedded cluster.
- **Confine the OS branch to the substrate-provisioning layer.** The branch lives in the `pkg/embedded/k3s` launcher selection plus the `SupportedPlatform()`/`k3sEnabled` gate in `pkg/embedded/stack`. The gateway, the production controller binary, the binary-embedded CRD install, the storage interfaces, and the §4.7 placement/adapter/mTLS path are byte-identical across operating systems, so the no-mode-dependent-business-logic-split invariant holds. This is infrastructure substrate provisioning rather than a business-logic branch.
- **Shell out to the `docker` CLI to run the k3s container.** This matches the established codebase pattern in `pkg/embedded/localcli/image.go` (`exec.Command("docker", "save", ...)` at `:110`, `exec.LookPath("docker")` at `:301`) and keeps the added prerequisite at exactly Docker. The `github.com/moby/moby/client` dependency is `// indirect` at `go.mod:179` and imported nowhere in `pkg/` or `cmd/` (it arrives transitively through testcontainers in `tests/testinfra`), so it is not free reuse and does not justify a Go Docker-API path.
- **Reject kind (vanilla Kubernetes) for the embedded substrate.** It would introduce a second distribution diverging from the Linux in-process k3s by OS. The apparent reuse from the tier-5 kind harness does not transfer: Embedded Mode does not run the Helm chart. It applies binary-embedded CRDs directly (`pkg/embedded/.../crdinstall.go`, "needs no checkout of the Helm chart") and runs the gateway and controllers as host child processes, with only agent Sandbox pods running in-cluster. kind stays canonical for chart e2e; the embedded substrate stays k3s.
- **Treat the host/Docker networking wiring as the main implementation risk and specify it.** The current `serverArgs()` hard-codes `--bind-address 127.0.0.1` (`k3s.go:210`) and `--rootless` conditionally on the host euid (`k3s.go:217-219`); both require substrate-specific adjustment for a containerized API server. The Docker-backed launcher must publish a host port and rewrite the generated kubeconfig server URL (consumed by host-process controllers via KUBECONFIG), the agent pods must reach the host gateway via `host.docker.internal` (absent everywhere in the repo today), and the §4.7 gateway↔adapter gRPC+mTLS path (`spec/04_system-components.md:636`) must traverse the host/Docker boundary because the gateway and controllers run as host processes while agent pods run in-cluster.
- **Disclose the local-fidelity gaps with their correct per-mechanism causes.** gVisor degrades because the embedded cluster installs no gVisor RuntimeClass/`runsc` shim (gVisor is a userspace kernel; dev mode falls back to runc per `spec/05_runtime-registry-and-pool-model.md:703-704`); Kata degrades because it needs hardware virtualization a Docker-Desktop VM or single-node substrate cannot nest (`spec/05_runtime-registry-and-pool-model.md:682`); the §13 default-deny NetworkPolicy egress isolation is not exercised because the embedded k3s runs `--disable-network-policy`. These gaps hold on any local substrate including Linux. The disclosure keeps the gVisor and Kata causes distinct.
- **Treat the Docker prerequisite as a new mandate rather than an existing assumption.** The spec names Docker Desktop only conditionally for one sub-path of `lenny image import` (no `--file`, `spec/24_lenny-ctl-command-reference.md:280`) and provides a Docker-free `--file` path for when the host daemon is unavailable (`spec/24_lenny-ctl-command-reference.md:275`); Embedded Mode currently documents zero pre-existing dependencies (`spec/17_deployment-topology.md:154,1534`) and Source Mode states "No Postgres, Redis, MinIO, Kubernetes, or Docker required" (`spec/17_deployment-topology.md:204`). A Docker requirement for the embedded substrate on macOS/Windows is staged here as a new mandate.

## 3. Proposed changes

### 3.1 Spec change: `spec/17_deployment-topology.md` §17.4 components-table k3s row (line 160)

Anchor on the Kubernetes row of the "Embedded components" table in §17.4, currently at line 160. The current Notes cell reads:

```
Downloaded on first `lenny up` into `~/.lenny/k3s/` and started in-process
```

The wording is stale against the current Linux implementation, which runs k3s as a managed child process (`k3s.go:7-8,161-184`), and it describes no macOS/Windows substrate. Replace the Notes cell so it states both launchers and removes "in-process". The Embedded-option cell keeps its "single-node, rootless where supported" qualifier; note that rootless applies to the Linux child-process launcher. Replacement Notes cell:

```
On Linux, the pinned k3s binary (`v1.31.4+k3s1`) is downloaded on first `lenny up` into `~/.lenny/k3s/` and supervised as a managed child process. On macOS and Windows, the same pinned k3s version runs as a container under Docker Desktop's Linux VM with the identical cluster-disabling flag set, so the embedded cluster is the same k3s distribution on every host. Rootless mode applies to the Linux child-process launcher.
```

Notes for the applier:

- Do not write "in-process" anywhere in the row.
- Do not alter the Embedded-option cell's `[k3s](https://k3s.io) (single-node, rootless where supported)` text.
- Leave every other row of the table unchanged.

### 3.2 Spec change: `spec/17_deployment-topology.md` §17.4 prerequisite prose (line 154)

Anchor on the prerequisite sentence in §17.4, currently at line 154. The current text reads:

```
A single statically-linked binary — `lenny` — embeds every dependency needed to run a complete Lenny installation on one host. No Kubernetes cluster, no Postgres operator, no cert-manager, no OIDC provider required beforehand. Intended audience: operators evaluating Lenny, developers building **against** Lenny (workload authors and runtime authors), and anyone who wants a functioning deployment on their laptop.
```

The opening sentence claims the binary "embeds every dependency needed to run a complete Lenny installation on one host", and the next sentence claims "No Kubernetes cluster, ... required beforehand" with no Docker qualifier. k3s needs a Linux kernel; on macOS and Windows the embedded substrate now runs inside Docker Desktop's VM, so Docker becomes a stated prerequisite on those hosts. Preserving the opening sentence verbatim while adding a Docker prerequisite makes the same paragraph assert both that the binary embeds every dependency and that Docker Desktop is a required, non-embedded dependency on macOS and Windows. Qualify the opening sentence so the embedding claim is scoped to the Lenny components the binary carries, then name the Linux kernel that Docker Desktop supplies as the one non-embedded prerequisite on macOS and Windows. Replace the first two sentences ("A single statically-linked binary ... required beforehand.") with:

```
A single statically-linked binary — `lenny` — embeds every Lenny component needed to run a complete installation on one host. On Linux no external dependency is required beforehand: no Kubernetes cluster, no Postgres operator, no cert-manager, no OIDC provider. On macOS and Windows the embedded k3s needs a Linux kernel that the binary cannot embed, so Docker Desktop is a prerequisite and supplies that kernel through its Linux VM.
```

Notes for the applier:

- Do not weaken the production-warning or local-only framing elsewhere in §17.4.
- Leave the "Intended audience:" sentence that follows the replaced text unchanged.

### 3.3 Spec change: `spec/17_deployment-topology.md` §17.9.6 zero-dependency claim (line 1534)

Anchor on the §17.9.6 sentence, currently at line 1534. The relevant clause reads:

```
This mode requires zero external cloud or cluster dependencies and is the primary path for laptop-scale evaluation of Lenny.
```

Scope the zero-dependency claim to Linux and name Docker Desktop as the macOS/Windows substrate prerequisite, cross-referencing the §17.4 prerequisite prose. Replace that clause with:

```
On Linux this mode requires zero external cloud or cluster dependencies; on macOS and Windows it requires Docker Desktop, which supplies the Linux VM the embedded k3s runs in (see [§17.4](#174-local-development-mode-lenny-dev) Embedded Mode). This mode is the primary path for laptop-scale evaluation of Lenny.
```

Notes for the applier:

- Leave the rest of the §17.9.6 paragraph (the backend enumeration and the Source/Compose Mode sentence) unchanged.
- Confirm the §17.4 anchor slug `#174-local-development-mode-lenny-dev` against the heading `### 17.4 Local Development Mode (`lenny-dev`)` before applying.

### 3.4 Spec change: `spec/17_deployment-topology.md` §17.4 platform-code-path invariant (line 168)

Anchor on the "Same platform code path as production" paragraph in §17.4, currently at line 168. The current text reads:

```
**Same platform code path as production.** Embedded Mode uses the production gateway, controllers, CRDs, and storage interfaces. Only the driver selection differs: `mode=embedded` is signaled by a platform flag that the storage, KMS, and identity interfaces consume to pick their embedded backends. There are no mode-dependent code splits in business logic.
```

Once the per-OS substrate launcher exists, the word "Only" is inaccurate, because a second axis of variation (host OS / substrate launcher) now exists alongside the `mode=embedded` driver-selection axis. The business-logic claim is not contradicted: an OS branch is not mode-dependent, and the substrate launcher is not business logic. Make the minimal repair: change "Only the driver selection differs" to "Within a host, the driver selection differs", preserve "There are no mode-dependent code splits in business logic" unchanged, and append one sentence locating the per-OS substrate provisioning below the named business-logic packages. Replacement paragraph:

```
**Same platform code path as production.** Embedded Mode uses the production gateway, controllers, CRDs, and storage interfaces. Within a host, the driver selection differs: `mode=embedded` is signaled by a platform flag that the storage, KMS, and identity interfaces consume to pick their embedded backends. There are no mode-dependent code splits in business logic. The embedded Kubernetes substrate is provisioned per host operating system (a managed k3s child process on Linux, a Docker-backed k3s container on macOS and Windows), and that provisioning is confined to the substrate layer below the gateway, controllers, CRDs, and storage interfaces, which stay identical across operating systems.
```

Notes for the applier:

- Do not add a second paragraph; one appended sentence suffices.
- "substrate" is established spec vocabulary (`spec/18_build-sequence.md`); do not introduce a parallel term.

### 3.5 Spec change: `spec/15_external-api-surface.md` §15.4.3 abstract-socket platform note (line 2074)

Anchor on the "Platform compatibility note" inside the §15.4.3 Transport bullet, currently at line 2074. The current text reads:

```
**Platform compatibility note:** Abstract Unix sockets (names beginning with `@`) are a Linux kernel feature and are **not supported on macOS**. Standard- and Full-level runtime development therefore requires a Linux environment. The recommended approach for macOS developers is to use `docker compose up` ([Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev) Compose Mode), which runs the adapter inside a Linux container. `make run` ([Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev) Source Mode) supports macOS for Basic-level runtimes only, since Basic level uses the stdin/stdout binary protocol exclusively and does not open any Unix sockets.
```

Under the Docker-backed substrate, Embedded Mode runs the adapter and runtime binary inside an in-cluster Linux pod (under the Docker VM on macOS and Windows), so the abstract-socket requirement is satisfied there. The host-side restriction holds only for Source Mode (`make run`, adapter on the host) and Compose Mode (adapter in a separate container). Replace the note with:

```
**Platform compatibility note:** Abstract Unix sockets (names beginning with `@`) are a Linux kernel feature. They are available wherever the adapter and runtime binary run inside an in-cluster Linux pod. Under Embedded Mode (`lenny up`) the adapter and runtime binary run in an in-cluster pod — on macOS and Windows that pod runs under Docker Desktop's Linux VM (see [Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev) Embedded Mode) — so Standard- and Full-level runtime authors on macOS and Windows can develop against Embedded Mode. Source Mode (`make run`) runs the adapter on the host, where abstract sockets are **not supported on macOS**; `make run` therefore supports macOS for Basic-level runtimes only, since Basic level uses the stdin/stdout binary protocol exclusively and does not open any Unix sockets. macOS developers who use Source Mode for Standard- or Full-level work should use `docker compose up` ([Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev) Compose Mode) instead, which runs the adapter inside a Linux container.
```

Notes for the applier:

- This edit must land together with §3.6, §3.8, and §3.9; the §15.4.5 and §24 cross-platform notes are non-contradictory only after this site and §3.6 are reconciled.
- Leave the surrounding Transport bullet text (the `@lenny-platform-mcp` examples and the "no stdio transport" sentence) unchanged.

### 3.6 Spec change: `spec/17_deployment-topology.md` §17.4 macOS note (line 345)

Anchor on the "macOS note" blockquote in §17.4, currently at line 345. The current text reads:

```
> **macOS note:** `make run` (Source Mode) supports macOS for Basic-level runtimes (stdin/stdout binary protocol only). Standard- and Full-level runtimes require abstract Unix sockets (`@` prefix names), which are **Linux-only** — macOS does not support abstract sockets. If you are developing a Standard- or Full-level runtime on macOS, use `docker compose up` (Compose Mode) instead, which runs the adapter inside a Linux container. See [Section 15.4.3](15_external-api-surface.md#1543-runtime-integration-levels) for level definitions.
```

The note is over-broad once Embedded Mode runs the adapter in an in-cluster Linux pod on macOS and Windows. Scope the restriction to Source Mode and add the Embedded-Mode path. Replace the note with:

```
> **macOS and Windows note:** The abstract Unix sockets that Standard- and Full-level runtimes use (`@` prefix names) are a Linux kernel feature and are not available to a host-side adapter on macOS or Windows. `make run` (Source Mode) runs the adapter on the host, so it supports macOS for Basic-level runtimes only (stdin/stdout binary protocol). Embedded Mode (`lenny up`) runs the adapter inside an in-cluster pod — under Docker Desktop's Linux VM on macOS and Windows — so abstract sockets are available there and Standard- and Full-level runtime authors on macOS and Windows can use Embedded Mode. A macOS or Windows developer who prefers Source Mode for Standard- or Full-level work should use `docker compose up` (Compose Mode) instead, which runs the adapter inside a Linux container. See [Section 15.4.3](15_external-api-surface.md#1543-runtime-integration-levels) for level definitions.
```

Notes for the applier:

- This edit and §3.5 reconcile the two over-broad restriction sites; both must land for §3.8 and §3.9 to be non-contradictory.
- Confirm the §15.4.3 anchor slug `#1543-runtime-integration-levels` before applying.

### 3.7 Spec change: `spec/24_lenny-ctl-command-reference.md` thin-client exception list (line 14) and §24.19 `lenny up` row (line 260)

Anchor on item 3 of the thin-client exception list, currently at line 14. The current text reads:

```
3. **`lenny up` / `lenny down` / `lenny status`** ([§24.19](#2419-local-stack)) manage the Embedded Mode single-binary stack ([§17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)) and run the gateway, controllers, embedded Postgres/Redis, and k3s in-process. These are local-only commands; they do not call any remote API.
```

The "in-process" wording is stale for k3s and the controllers (the controllers run as host child processes per `stack.go:267-274`; k3s runs as a managed child process per `k3s.go:7-8`), and it assumes the full stack runs on every host. The "in-process" characterization is accurate for the `miniredis`-backed Redis (`spec/17_deployment-topology.md:162`). Reword so k3s is named as a managed child process on Linux and a Docker-backed container on macOS/Windows, the controllers as host child processes, and the in-process Redis is preserved. Replacement:

```
3. **`lenny up` / `lenny down` / `lenny status`** ([§24.19](#2419-local-stack)) manage the Embedded Mode single-binary stack ([§17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)): they run the gateway and controllers as host child processes, the embedded Postgres as a child process (PostgreSQL 16 binary bundle) and the embedded Redis in-process, and the embedded k3s as a managed child process on Linux or as a container under Docker Desktop's Linux VM on macOS and Windows. These are local-only commands; they do not call any remote API.
```

Anchor on the §24.19 `lenny up` row, currently at line 260. The current Description cell reads:

```
Start the embedded k3s / Postgres / Redis / KMS / OIDC / gateway / controllers / reference runtimes stack. Idempotent. Prints the local gateway URL and the non-suppressible "Embedded Mode — NOT for production use" banner.
```

This cell carries no present falsehood and becomes true once parity lands. Add a brief §17.4 Docker-Desktop cross-reference if it aids navigation; the rest of the row needs no change. Optional replacement Description cell:

```
Start the embedded k3s / Postgres / Redis / KMS / OIDC / gateway / controllers / reference runtimes stack (on macOS and Windows the embedded k3s runs under Docker Desktop; see [§17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)). Idempotent. Prints the local gateway URL and the non-suppressible "Embedded Mode — NOT for production use" banner.
```

Notes for the applier:

- The line-14 edit is required (it corrects a present falsehood). The line-260 cross-reference is optional polish; apply it only if the navigation aid is worth the added text.
- Do not blanket-remove "in-process": preserve the in-process characterization of the embedded Redis.

### 3.8 Spec change: `spec/15_external-api-surface.md` §15.4.5 Runtime Author Roadmap §17.4 item (line 2360)

Anchor on item 6 of the §15.4.5 Basic-level roadmap, currently at line 2360. The current text reads:

```
6. **[Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)** — Local Development Mode (`lenny-dev`). Use `lenny up` (Embedded Mode, the primary path for runtime authors — exercises the real Kubernetes code path via the embedded stack and registers your runtime through the production admin API) or `make run` (Source Mode, for platform contributors who need to modify the gateway or controller source alongside their runtime).
```

This recommendation is correct on every host once §3.1 through §3.4 establish the embedded k3s runs everywhere, and once §3.5 and §3.6 reconcile the abstract-socket restriction. Add a one-line cross-platform note so a macOS or Windows reader is not left assuming the recommendation excludes them. Replace the item with:

```
6. **[Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)** — Local Development Mode (`lenny-dev`). Use `lenny up` (Embedded Mode, the primary path for runtime authors — exercises the real Kubernetes code path via the embedded stack and registers your runtime through the production admin API; on macOS and Windows the embedded k3s runs under Docker Desktop's Linux VM) or `make run` (Source Mode, for platform contributors who need to modify the gateway or controller source alongside their runtime).
```

Notes for the applier:

- Do not apply this edit before §3.5 and §3.6 land; without them the cross-platform note contradicts the §15.4.3 and §17.4 abstract-socket restrictions.

### 3.9 Spec change: `spec/15_external-api-surface.md` §15.4.5 Standard-level abstract-socket dependency note

The §15.4.5 Standard-level roadmap entry for §4.7 (`spec/15_external-api-surface.md:2364`) does not restate the abstract-socket restriction, so no edit to that entry is required. Confirm during application that no §15.4.5 Standard- or Full-level item asserts a host-side Linux-only requirement that §3.5 and §3.6 now scope to Source Mode; if one does, scope it the same way. This subsection is a verification step rather than a staged text replacement.

Notes for the applier:

- If §15.4.5 carries no such Standard/Full restriction, record that no edit was needed and move on. Do not invent a note.

### 3.10 Spec change: `spec/17_deployment-topology.md` §17.4 local-fidelity disclosure (new paragraph)

Add a local-fidelity disclosure paragraph to §17.4. §17.4 currently carries no disclosure that a local single-node cluster cannot honor the full production isolation and network surface, and these gaps hold on any local substrate including Linux. Anchor the new paragraph immediately after the "Same platform code path as production" paragraph (`spec/17_deployment-topology.md:168`, the §3.4 target) and before the "Reference runtimes pre-installed" paragraph (`spec/17_deployment-topology.md:170`). The disclosure states the correct per-mechanism cause for each gap and keeps the gVisor and Kata causes distinct. New paragraph:

```
**Local isolation fidelity.** The embedded single-node cluster cannot reproduce the full production isolation surface on any host. The `sandboxed` (gVisor) and `microvm` (Kata) isolation profiles ([§5.3](05_runtime-registry-and-pool-model.md#53-isolation-profiles)) degrade to `standard` (runc) locally: gVisor degrades because the embedded cluster installs no gVisor RuntimeClass or `runsc` containerd-shim, and Kata degrades because it requires hardware virtualization the local substrate cannot nest. This matches the dev-mode fallback to runc ([§5.3](05_runtime-registry-and-pool-model.md#53-isolation-profiles), dev mode fallback). The embedded k3s runs with NetworkPolicy enforcement disabled, so the default-deny egress isolation ([§13.2](13_security-model.md#132-network-isolation)) is not exercised locally, consistent with the dev-mode preflight skip of CNI NetworkPolicy support ([§17.6](#176-packaging-and-installation) preflight, dev mode). These gaps apply to all three local-dev modes and to both the Linux and Docker-backed substrates, so an evaluator should not mistake local behavior for the production isolation boundary.
```

Notes for the applier:

- Confirm the §5.3 anchor slug `#53-isolation-profiles`, the §13.2 anchor slug `#132-network-isolation`, and the §17.6 anchor slug `#176-packaging-and-installation` against their headings before applying. The isolation-profile table, the RuntimeClass/Degraded behavior, and the dev-mode runc fallback are defined in §5.3 Isolation Profiles (`spec/05_runtime-registry-and-pool-model.md:662,668-670,703`), so the disclosure links there rather than to §5.1 Runtime, matching the convention at `spec/05_runtime-registry-and-pool-model.md:175` and `spec/26_reference-runtime-catalog.md:38`.
- Do not conflate gVisor and Kata under a single "nested virtualization" cause; gVisor is a userspace kernel that needs no nested virtualization, so the disclosure keeps the two causes distinct.
- Place the paragraph between the §3.4 paragraph and the "Reference runtimes pre-installed" paragraph; do not displace either.

## 4. Non-goals

- **No code is staged by this proposal.** The §17.4/§17.9.6/§15/§24 spec edits land first via implement-proposal. The code blast radius (the Docker-backed k3s launcher in `pkg/embedded/k3s`, the `SupportedPlatform()`/`k3sEnabled` gate rework in `pkg/embedded/stack`, the kubeconfig-server-URL rewrite, the `host.docker.internal` egress wiring, the §4.7 mTLS host/Docker boundary, and removing the Linux-only skip in the embedded test infrastructure) is recorded for the implementation step in §6 and is not written here.
- **No change to the Linux in-process managed-child-process launcher.** The Linux path keeps downloading the bare k3s binary from GitHub releases (`k3s.go:45-54,176`) and supervising it as a child process; the zero-dependency Linux experience does not regress and Docker is not required on Linux.
- **No adoption of kind or k3d as the embedded substrate by default.** The substrate stays k3s on every host; kind remains canonical only for the tier-5 Helm-chart e2e harness, which Embedded Mode does not use. Whether the Docker-backed launcher shells out to a `k3d` CLI or runs `docker run rancher/k3s` directly is an open question for the reviewer (see §8).
- **No tier-dependent or dual-mode business-logic split.** The OS branch is confined to substrate provisioning; the gateway, controllers, CRDs, storage interfaces, and the §4.7 placement/adapter/mTLS path stay byte-identical across operating systems. v1 ships a single canonical implementation of each business-logic concern.
- **No new client-facing protocol, RPC, frame, or endpoint.** The change is substrate provisioning plus spec wording; no MCP or gRPC surface is added.
- **No promise that local isolation matches production.** The gVisor and Kata isolation profiles and the §13 NetworkPolicy egress boundary are explicitly disclosed (§3.10) as not reproduced locally on any substrate, rather than silently degraded.
- **No reconciliation of the §24.19.1 `lenny image import` prerequisite (C6, dropped).** A first draft proposed adding prose to §24.19.1 (`spec/24_lenny-ctl-command-reference.md:280`) to "reconcile" the conditional host-daemon Docker prerequisite with the new substrate Docker prerequisite. It was dropped because there is no inconsistency to reconcile: line 280 is a conditional prerequisite for the host-daemon image-source sub-path of `lenny image import` (`spec/24_lenny-ctl-command-reference.md:270`), and it composes without contradiction with a mode-level substrate prerequisite. If Embedded Mode is running on macOS/Windows it already requires Docker Desktop, so the host-daemon import path's prerequisite is automatically satisfied. The image-import code confirms the two Docker uses are separate: the host-daemon path runs `docker save` against the host daemon (`image.go:103-110`) and pipes into the embedded containerd via a host-local `ctr` binary and host filesystem socket (`image.go:262-263`, `121-122`). Adding a reconciliation note would only re-explain the substrate prerequisite at the import site, working against the doc-content rule to introduce each concept once and link from elsewhere. Separately, when k3s runs in a Docker container the host paths the import code reaches (`~/.lenny/k3s/data/agent/containerd/containerd.sock` at `image.go:263`, the host `ctr` binary at `image.go:262`) do not exist, so the import mechanism itself needs real code rework on macOS/Windows. That rework is deferred to the implementation step and is recorded in §6; a consistency note asserting "adds no prerequisite" would be inaccurate, which is a second reason C6 is dropped.
- **No reconciliation of the pre-existing stale "in-process" wording beyond the §17.4:160 and §24:14 sites this change already touches.** Other sites that may carry stale "in-process" framing are out of scope.
- **No new Compose Mode or Source Mode capability.** Both modes remain controller-simulator paths and are unchanged.

## 5. Testing

- **Tier 0 (static):** confirm the edited spec renders and the added intra-spec anchors (`#174-local-development-mode-lenny-dev`, `#1543-runtime-integration-levels`) resolve to live headings. The spec lint and link-check stage flags a broken anchor.
- **Tier 1 (unit), at implementation:** the Docker-backed k3s launcher selection (`SupportedPlatform()` per OS, the launcher chosen in `pkg/embedded/k3s`) gets unit tests that assert the macOS/Windows path selects the Docker-backed launcher and the Linux path selects the child-process launcher, including the kubeconfig-server-URL rewrite. These are staged with the code at the implementation step rather than here.
- **Tier 2 (component) and tier 5 (e2e), at implementation:** `lenny up` on a Docker-backed substrate brings up the embedded cluster, installs CRDs, starts the controllers, and warms a pod, exercising the host/Docker networking wiring (published API port, `host.docker.internal` gateway egress, §4.7 gRPC+mTLS across the boundary). Run on a macOS or Windows host with Docker Desktop; where that host is unavailable in CI, implement the test and state the dependency.
- **Tier 11 (docs):** confirm the edited §17.4 components table, prerequisite prose, platform-code-path invariant, §17.9.6, §15.4.3, §15.4.5, and §24 entries agree with each other and with §05/§13 on the per-OS substrate, the Docker prerequisite, and the local-fidelity disclosures. The check confirms convergence.
- **No new tier-2-or-higher behavioral test is added by this proposal.** The proposal is spec-only; the behavioral tiers above are staged with the code at the implementation step.

## 6. Code blast radius (recorded for implement-proposal; not staged here)

- **`pkg/embedded/k3s`:** add a Docker-backed launcher (shell out to `docker`, matching `pkg/embedded/localcli/image.go`) selected on macOS and Windows; rework `SupportedPlatform()` so non-Linux hosts are supported when Docker is present; publish a host API port and rewrite the generated kubeconfig server URL; adjust the `--bind-address`/`--rootless` handling that is hard-coded for the host child process (`k3s.go:210,217-219`).
- **`pkg/embedded/stack`:** rework the `k3sEnabled` gate and the non-Linux skip (`stack.go:205,223-226,267`) so the CRD install and controllers run on every host where the substrate comes up; wire the controllers' KUBECONFIG to the rewritten kubeconfig; wire `host.docker.internal` so in-cluster agent pods reach the host gateway, and carry the §4.7 gateway↔adapter gRPC+mTLS path across the host/Docker boundary.
- **`pkg/embedded/localcli/image.go`:** rework the image-import path that reaches the embedded containerd via host filesystem paths (`image.go:262-263`, `121-122`) so it works when k3s runs in a Docker container rather than as a host child process.
- **Embedded test infrastructure (`tests/testinfra`):** remove the Linux-only skip guarding the embedded-stack tests so the Docker-backed path runs on macOS/Windows CI.

## 7. Resolved in adversarial review

### Pass 1 (2026-06-19, automated)

- Corrected the §3.10 local-fidelity disclosure (proposal §3.10) so the gVisor/Kata isolation-profile degradation and the dev-mode runc fallback link to §5.3 Isolation Profiles (`#53-isolation-profiles`) instead of §5.1 Runtime (`#51-runtime`). The profile table, the RuntimeClass/Degraded behavior, and the dev-mode runc fallback are defined in §5.3 (`spec/05_runtime-registry-and-pool-model.md:662,668-670,703`); §5.1 only references the `isolationProfile` field in passing and forwards to §5.3. The `#51-runtime` slug resolved to a live but wrong heading, so the applier-verification note and the tier-0 link check would not have caught it. Updated the applier note at proposal §3.10 to confirm the `#53-isolation-profiles` slug and cite the §5.3 source lines and the convention at `spec/05_runtime-registry-and-pool-model.md:175` and `spec/26_reference-runtime-catalog.md:38`.
- Resolved the §17.4 prerequisite-paragraph contradiction (proposal §3.2). The earlier edit inserted a Docker-Desktop prerequisite while preserving the opening sentence "embeds every dependency needed to run a complete Lenny installation on one host" verbatim (`spec/17_deployment-topology.md:154`), so the same paragraph asserted both that the binary embeds every dependency and that Docker Desktop is a required non-embedded dependency on macOS and Windows. Changed §3.2 to replace the first two sentences: the embedding claim is now scoped to the Lenny components the binary carries, and the Linux kernel that Docker Desktop's VM supplies is named as the one non-embedded prerequisite on macOS and Windows. Updated the applier note to drop the "keep the first sentence unchanged" instruction and the §9 files-touched entry to match.

### Pass 2 (2026-06-19, automated)

- Corrected the §3.7 staged replacement for §24 thin-client item 3 (proposal §3.7, replacement text) so the embedded Postgres is no longer characterized as in-process. The earlier replacement read "the embedded Postgres and Redis in-process", which grouped Postgres with Redis under "in-process". The embedded Postgres runs as a separate child process (`pkg/embedded/postgres/postgres.go:3-6` "starts it as a child process", `:48` "Instance is a running embedded Postgres process"; the §17.4 components table corroborates with a binary bundle at `~/.lenny/postgres/` at `spec/17_deployment-topology.md:161`, while only Redis is marked "In-process" at `spec/17_deployment-topology.md:162`). Applying the edit verbatim would have replaced one stale falsehood with a new one. Reworded the replacement to "the embedded Postgres as a child process (PostgreSQL 16 binary bundle) and the embedded Redis in-process", which scopes "in-process" to the `miniredis`-backed Redis alone and matches the proposal's own analysis at §3.7 (only Redis is in-process) and its applier note (preserve the in-process characterization of the embedded Redis).

## 8. Open decisions for review

- **Roll-our-own `docker run rancher/k3s` versus shelling out to a `k3d` CLI.** Rolling our own keeps the prerequisite at exactly Docker (the proposal's stated direction) and is recommended. `k3d` is mature k3s-in-Docker tooling that handles the host/Docker networking wiring this proposal flags as the main implementation risk (published API port, `host.docker.internal` egress, kubeconfig rewrite), but it is an additional user-installed prerequisite beyond Docker. The reviewer should confirm whether the extra `k3d` prerequisite is acceptable in exchange for its maturity, or whether the launcher should roll its own `docker run`.

## 9. Files touched on application

- `spec/17_deployment-topology.md`: §17.4 components-table k3s row (line 160) reworded to state the per-OS launcher and drop "in-process"; §17.4 prerequisite prose (line 154) reworded so the binary's embedding claim is scoped to the Lenny components it carries and Docker Desktop is named as the one non-embedded prerequisite supplying the Linux kernel on macOS and Windows; §17.4 platform-code-path invariant (line 168) repaired so "Only the driver selection differs" no longer asserts a single axis, with one appended sentence locating the per-OS substrate provisioning; §17.4 macOS note (line 345) scoped to Source Mode with the Embedded-Mode in-cluster-pod path added; §17.9.6 zero-dependency claim (line 1534) scoped to Linux with Docker Desktop named for macOS/Windows.
- `spec/15_external-api-surface.md`: §15.4.3 abstract-socket platform note (line 2074) scoped to host-side Source/Compose Mode with the Embedded-Mode in-cluster-pod path added; §15.4.5 §17.4 roadmap item (line 2360) given a one-line cross-platform note; §15.4.5 Standard/Full items verified for any further over-broad restriction (no edit expected).
- `spec/24_lenny-ctl-command-reference.md`: thin-client exception item 3 (line 14) reworded to name k3s and controllers as child processes per OS while preserving the in-process Redis; §24.19 `lenny up` row (line 260) optionally given a §17.4 Docker-Desktop cross-reference.
- No code, schema, proto, chart, or docs file is touched by this proposal. The code blast radius in §6 and the behavioral tiers in §5 are implemented at the implementation step.
