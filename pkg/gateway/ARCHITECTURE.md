# `pkg/gateway` layout

This file maps each directory of `pkg/gateway` to the part of the gateway it holds and to the specification page that states that part. Change 0117 (gateway package layout) defines the layout. The gateway is one binary partitioned into seams, the session core, and parts that apply to the whole replica, as the gateway index page (`spec/components/gateway/index.md`) states.

## Parts and directories

| Directory | Part | Page |
| :-- | :-- | :-- |
| `seam` | The seam interface: the Go interfaces through which the seams and the session core reach each other, the route registration interface, and the values they carry. Under it, `seam/subsystem` and `seam/extractionthreshold` hold the seam model: the breaker, the limiter, the per-seam metrics, and the extraction-threshold reader. | `spec/interfaces/gateway-seams.md` (GWSEAM); the seam model on `spec/components/gateway/index.md` (GW) |
| `streamproxy` | The stream proxy seam: the session event bus, session attachment, and the event and log streams. | `spec/components/gateway/stream-proxy.md` (GWSTREAM) |
| `uploadhandler` | The upload handler seam: upload proxying, validation, and staging. | `spec/components/gateway/upload-handler.md` (GWUPLOAD) |
| `platformtools` | The platform tools seam: platform tool calls from pods, delegation orchestration and the delegation tree, the elicitation chain, input waits, and tool approvals. | `spec/components/gateway/platform-tools.md` (GWTOOLS) |
| `meteringproxy` | The metering proxy seam for LLM upstream calls. | `spec/components/gateway/metering-proxy.md` (GWLLM) |
| `core/coordination` | Session coordination: the coordination lease, fencing, and their stores. | `spec/components/gateway/session-coordination.md` (GWCOORD) |
| `core/podclaim` | The pod claimer. | `spec/components/gateway/pod-claimer.md` (GWCLAIM) |
| `core/checkpoint` | The checkpoint barrier, the checkpointer, and the eviction fallback. | `spec/components/gateway/checkpoint-barrier.md` (GWBARRIER) |
| `core/leaderjobs` | The gateway-singleton jobs of the session core that act on a confined package, such as the T4 KMS probe, which the replica's leader gate runs as one job. | `spec/components/gateway/index.md` (GW) |
| `core/session` | The session operations of the session core: create, start, bind, resume, suspend, terminate, and message delivery, with the pod session registry, the executor, and the adapter client. | The capability pages of the sessions and messaging areas |
| `api` | The API front: the request pipeline, middleware, route registration, service identity, and the admin handlers. `api/requestmetrics` is the pipeline's request-metrics stage. | `spec/components/gateway/api-front.md` (GWAPI) |
| `drain` | The drain of a replica: `prestop`, the preStop hook that checkpoints the replica's sessions, and `drainreadiness`, the drain-readiness and forced-drain audit endpoints the drain admission webhook calls. | `spec/components/gateway/drain.md` (GWDRAIN) |
| `scaling` | Scaling and capacity signals: `gcpause`, the GC pause gauge, and `capacityplanning`, the capacity-tier startup checks. | `spec/components/gateway/scaling-and-capacity.md` (GWSCALE) |
| `opsendpoints` | The ops endpoints: `health`, the platform health service, and `health/backends`, its backend checkers. | `spec/components/gateway/ops-endpoints.md` (GWOPS) |
| `replica` | The replica as a unit: `gatewaymetrics`, the gateway's metric set and registry; `gatewayleader`, leader election and the leader gate; `pdbwatcher`, the disruption budget watcher; `devmode`, the development-mode TLS guard rails; and `tlsprobe`, the startup TLS probe. | `spec/components/gateway/index.md` (GW) |
| `playground` | The web playground. | `spec/components/web-playground.md` (PLAY) |

Domain logic and stores do not belong to a gateway part. They live in concern directories under `pkg/`, such as `pkg/quota`, `pkg/audit`, `pkg/session`, and `pkg/tenancy`, where any component can import them.

## The import rule

- A package of a seam imports, from `pkg/gateway`, only `pkg/gateway/seam` and its subpackages and the packages of its own seam.
- A package of the session core imports no seam package and no API front package.
- A package of the API front imports no seam package and no session core package. Each part registers its own routes through the route registration interface of `pkg/gateway/seam`.
- `pkg/gateway/seam` imports no other package of `pkg/gateway`.
- No package outside `pkg/gateway` and `cmd/lenny-gateway` imports a package of `pkg/gateway`, except test packages under `tests/`.
- A package of another replica-level part, or of the web playground, imports no seam package.
- Only the session core, the metering proxy, and `cmd/lenny-gateway` import the confined packages: the credential packages, the token and key packages, the admission packages (the authentication middleware, quota, policy, and the tenant store), and the authorization predicates. The API front imports those its request pipeline and admin handlers read, and the web playground the JWT package, the issued-token store, and the tenant store, which its token mint reads. `tests/static/lint/gatewaylayout/confined.go` lists them and marks those of the API front and the playground. The rule governs direct imports.
- Every package of `pkg/gateway` sits in a directory of the table above.

The replica-level parts other than the API front may import the session core and the seam interface. `cmd/lenny-gateway` imports every part and wires each concrete type into the interfaces of `pkg/gateway/seam`.

The layout gate, `TestGatewayLayoutHoldsTheImportRule` in `tests/static/lint`, holds these rules over the non-test import graph. Its directory table is in `tests/static/lint/gatewaylayout/layout.go`. The wiring check, `TestGatewayWiringCrossesOnlyThroughTheSeam` in the same package, reads `cmd/lenny-gateway` with full type information and refuses a value of one part passed where another part expects an interface or a function not declared in `pkg/gateway/seam`, where the import rules refuse the receiving part importing the value's package. It attributes each value to the parts it reaches, following locals, the composition root's struct fields, adapters, closures, and the composition root's own functions and methods. It also reports a value attributed to a confined package that the composition root hands to a part GW-R2 does not allow. A crossing is judged and keyed by its receiving part, the package whose struct field or function parameter takes the value, and interfaces of the standard library or a dependency are not crossing targets. `tests/registers/gateway-wiring.yaml` lists the crossings each move step routes through the seam interface, and it only shrinks.

## Packages that have not moved yet

The steps of change 0117 move the packages into this layout one part at a time. Until a package's step runs, the package stays in its earlier directory, and two records track it:

- `tests/registers/gateway-layout.yaml` lists each package outside the directory of its part, with the part it belongs to and the step that moves it, or with the member change of the v1 program that removes it. It also lists each import that breaks the rule above, with the step that ends it. The layout gate refuses a violation the register does not carry and a row that no longer occurs. `TestGatewayLayoutRegisterOnlyShrinks` compares the register with its copy at the merge base: a branch deletes rows, and adds or rewrites one only where a move rewrote its paths or in the step that moves its package.
- `scripts/refactor/manifest` lists the old and new import path of every move, with its step. The move driver in `scripts/refactor` applies one step with `go run ./scripts/refactor -step <step>`. After a move runs, the manifest stays as the translation table for any text that still names an old path.

Each move step settles the names of the packages it moves, and a step that renames a package edits its manifest line before it runs the driver.
