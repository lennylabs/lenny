---
code: CONV
type: conventions
summary: >-
  How the migrated specification under spec-next/ is written: identifiers and their anchors, link
  rules, page status values, reviewer comments, and the registry of page codes this spike uses.
---

# Conventions

This page is the minimal conventions page of the migration spike. The page types, the placement procedure, the owners of enumerable facts, and the gloss rule are those of `TAXONOMY.md` version 2 in `designs/evidence/spec-structure-design/spike/`. This page states only what the spike had to fix to write pages that a tool can check.

## Identifiers and anchors {#identifiers}

- A page code is two to nine upper-case letters with no hyphen, registered in the table below before use.
- A requirement is written `<a id="claim-r4"></a>**CLAIM-R4. Title.** Text.` The anchor is the lower-cased identifier. Numbers are never reused, and a retired requirement keeps its number with a successor pointer.
- An owned term carries an anchor equal to its slug, placed in the term's table cell: `<a id="reserved-hold"></a>Reserved hold`. A term anchor is unique across the whole specification. When two capabilities would own the same word, each qualifies it (`pod-phase` rather than `phase`).
- A definition block is preceded by an anchor `<kind>-<slug>`, where the slug is the defined name lower-cased with every run of characters other than letters and digits replaced by `-`: `setting-gateway-claimholdttlseconds`, `metric-lenny-pod-claim-duration-seconds`, `label-lenny-dev-tenant-id`, `error-warm-pool-exhausted`, `crd-podclaim`.
- An interface operation has the identifier `<interface folder>#<resource>.<verb>` and the anchor `<resource>-<verb>` lower-cased: `kubernetes#podclaim.updateStatus` is anchored `podclaim-updatestatus`.
- Code cites an identifier and never a path: `// spec: CLAIM-R4`, `// spec: kubernetes#podclaim.create`, `// spec: setting-claimorphantimeout` for a definition.

## Links {#links}

- A link into the specification is an ordinary relative Markdown link. It ends at a page or at an identifier anchor registered on that page.
- A heading anchor (`{#overview}`) is a local anchor. A link from another page never targets one.
- `tools/specref.py check` enforces both rules, and `tools/specref.py fix` rewrites paths after a page moves.

## Page status {#status}

The front matter field `status` is absent on a page written in full. `status: partial` marks a page of which the spike wrote only the parts the pod claimer needs. `status: placeholder` marks a page outside the spike that exists so links resolve. A link into a placeholder targets the page and carries no fragment, because the placeholder registers no identifier.

## Reviewer comments {#reviewer-comments}

Three HTML comments annotate the migrated text and are dropped by the renderer:

- `<!-- src: ... -->` names the current section and bold label, or the design proposal, a paragraph came from.
- `<!-- judgment: ... -->` records a placement or a reading that needed semantic judgment rather than a mechanical move.
- `<!-- unresolved: ... -->` records a fact the sources do not settle.

## Code registry {#code-registry}

| Code | Page | Status in the spike |
| :-- | :-- | :-- |
| CONV | `conventions.md` | written |
| CLAIM | `capabilities/runtimes-and-pools/pod-claims.md` | written |
| POOL | `capabilities/runtimes-and-pools/warm-pools.md` | written |
| APOD | `capabilities/runtimes-and-pools/agent-pod-lifecycle.md` | written |
| SLOTOPS | `capabilities/sessions/slot-assignment-and-failure.md` | written |
| SLOT | `capabilities/sessions/session-isolation.md` | partial |
| GWCLAIM | `components/gateway/pod-claimer.md` | written |
| KPCLAIM | `interfaces/kubernetes/podclaim.md` | written |
| KAPOD | `interfaces/kubernetes/agentpod.md` | written |
| KPSET | `interfaces/kubernetes/podset.md` | written |
| KLABEL | `interfaces/kubernetes/labels-and-annotations.md` | partial |
| ISO | `capabilities/runtimes-and-pools/isolation-profiles.md` | placeholder |
| PLACE | `capabilities/runtimes-and-pools/placement.md` | placeholder |
| RCLASS | `capabilities/runtimes-and-pools/resource-classes.md` | placeholder |
| RUNTIME | `capabilities/runtimes-and-pools/runtime-registry.md` | placeholder |
| REV | `capabilities/runtimes-and-pools/runtime-revisions.md` | placeholder |
| DERIVED | `capabilities/runtimes-and-pools/tenant-derived-runtimes.md` | placeholder |
| CKPT | `capabilities/sessions/checkpoints/checkpoint-capture.md` | placeholder |
| SESS | `capabilities/sessions/session-lifecycle/session-states.md` | placeholder |
| SCRUB | `capabilities/sessions/pod-reuse-and-scrub.md` | placeholder |
| RESUME | `capabilities/sessions/retry-and-resume.md` | placeholder |
| FORENSIC | `capabilities/security-and-compliance/quarantine-and-evidence.md` | placeholder |
| WEBHOOK | `components/admission-webhooks.md` | placeholder |
| POD | `components/agent-pod/index.md` | placeholder |
| REDIS | `components/external/redis.md` | placeholder |
| PSC | `components/pool-scaling-controller.md` | placeholder |
| WPC | `components/warm-pool-controller.md` | placeholder |
| GW | `components/gateway/index.md` | placeholder |
| ERR | `cross-cutting/error-model.md` | placeholder |
| PKI | `cross-cutting/mtls-and-pki.md` | placeholder |
| OBSCONV | `cross-cutting/observability-conventions.md` | placeholder |
| APOOLS | `interfaces/admin-api/pools.md` | placeholder |
| CAPISES | `interfaces/client-api/sessions.md` | placeholder |
| CHGWPOD | `interfaces/channels/gateway-to-pod.md` | placeholder |
| K8S | `interfaces/kubernetes/index.md` | placeholder |
| ADAPTER | `components/agent-pod/adapter.md` | named as a metric emitter; not created |
| SEND | `capabilities/sessions/session-lifecycle/session-end.md` | named in front matter; not created |
| SCRUBPROC | `capabilities/sessions/scrub-procedure.md` | named in the ledger; not created |
| TIMEOUT | `capabilities/sessions/session-lifecycle/timeouts.md` | named in the ledger; not created |
| SUSP | `capabilities/sessions/session-lifecycle/suspension-and-dormancy.md` | named in the ledger; not created |
| SCREATE | `capabilities/sessions/session-lifecycle/session-creation.md` | named in the ledger; not created |
| KADMIT | `interfaces/kubernetes/admission.md` | named in the ledger; not created |
| KUBE | Kubernetes as an external dependency (MAP.md) | named in the ledger; not created |
| ARCH, NETISO, DCFG, PREFLT, INSTALL, OBS, STORPG, STORREDIS, QUOTA | as in MAP.md | named in the ledger; not created |
