# Design principles

Project-wide principles the owner has set for what Lenny contains and how the specification, the changes, and the documentation describe it. They apply to every change, every specification edit, and every documentation page, and to any agent that proposes a design. They complement `spec-driven-development.md`, which governs how a design reaches the specification, and `code-best-practices.md`, which governs the code.

## Top-level principle

Lenny's core stays small and generic. A design adds a protocol-neutral hook in Lenny before it adds machinery for a particular protocol, vendor, or kind of runtime, and it documents everything an operator or a runtime author needs.

## Keep the core small

- When a feature integrates an external protocol or service, prefer leaving it outside Lenny's core: in an optional first-party repository, in an operator-run service reached through egress, or in a runtime. Lenny then adds only small, protocol-neutral hooks. The owner made the client API REST-only and moved the MCP, OpenAI, and A2A client adapters to their own repositories on 2026-11-14, and removed connectors on 2026-11-15.
- Before proposing a Lenny-side mechanism for an external protocol, check whether egress, a runtime, or an operator service already covers it, and present that option beside the Lenny-side one.
- Lenny carries no provider-specific or vendor-specific code paths, such as per-cloud authentication, per-vendor rehosting, or vendor channel enums. Wire formats Lenny must parse are allowed; vendor quirks belong to an operator-run gateway (owner's answer of 2026-11-19: "I don't want provider specific code in Lenny. Deployers can use LLM gateways for this.").
- Lenny does not enforce policy by inspecting network traffic.

## Runtime-agnostic specification

- The specification defines no categories of runtime, such as coding, research, or chat, and keys no default or rule on one. A platform default applies to every runtime alike. A shipped reference runtime may describe its own defaults.
- Describe the isolation profiles factually. Choosing a profile is the operator's decision, so no text calls runc (the `standard` profile) development-only, test-only, or unfit for production. State what runc shares (the host kernel), what it does not protect against (a kernel exploit), and the controls that bound the risk (node separation and user namespaces).

## No compatibility machinery

Lenny has no deployments outside the project. A change adds no dual mode, legacy flag, or migration shim to preserve compatibility for an external user; it changes the interface and every caller. `code-best-practices.md` ("Configuration and compatibility") states the same rule for code.

## Document everything

Each change lists, under its documentation section, every detail an operator, a deployer, or a runtime author needs from it: settings and their defaults, node configuration, limits, failure modes, and contracts (owner's answer of 2026-11-13: "we must document all the relevant details of this design (and all others)"). A review treats a missing item as a defect.

## Where these principles apply

- Every change under `changes/`, every edit of `spec/`, and the documentation under `docs/`.
- Any proposal an agent makes to the owner.

## Maintenance

When the owner sets a lasting principle about what Lenny contains or how it is described, add it here with the date and the owner's words where they matter.
