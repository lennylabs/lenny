# Channel naming

Project-wide rules for naming the communication channels between the gateway replicas, the agent pod, the adapter container, the runtime container, and the control plane. They apply to every identifier that names a link, a channel, or a register, and to every carrier that spells one: specification prose, documentation, schemas, Go source, proto definitions, flags, environment variables, manifest keys, metric label values, and test names.

Specification §28.1 is the normative statement of this law and §28.3 carries the registers the identifiers are drawn from. This file states the same rules where an agent writing a name meets them, so a conforming name is written the first time rather than corrected by a lint afterwards. It also writes out the phrases §28.1 N3 bans, which the specification describes rather than quotes; the list is under "Reserved spellings" below.

## Top-level principle

Each conversation on this surface has one canonical identifier, chosen for the conversation it carries, and every carrier spells that identifier in the form the carrier's own convention fixes. An identifier encodes nothing the register in §28.3 already records, and it is chosen once and used everywhere.

## The naming law

**N1.** A channel's canonical identifier is a mnemonic for the conversation it carries, chosen so that no two channels on the same boundary share a stem. The endpoint pair, the plane, the dial direction, the authority direction, and the transport are register columns in §28.3, so an identifier is not required to encode any of them and is never read as the authoritative statement of one.

**N2.** Identifiers are mnemonic, uppercase, and hyphenated, under one of the three class prefixes §28.2 states: `LNK-` for a transport connection between two participants, `CH-` for a typed conversation carried on one connection, and `REG-` for shared state mediating two participants with no live connection. Positional identifiers are not used, because a channel added between two others must not renumber its neighbours.

**N3.** Two words are reserved and may not stand as a bare noun phrase naming a conversation on this surface: the word the platform uses for a resource's phase transitions, and the word it uses for a command plane. The prohibition covers the space-separated spelling and the hyphenated compound spelling, and a matcher joins two consecutive comment lines before it applies either spelling, so a phrase wrapped across a comment boundary is one site. The prohibition's domain is `spec/`, `docs/`, `schemas/`, a Go doc comment in a tracked Go file, and a tracked root-level markdown document the exclusion list leaves in scope. Outside that domain are the historical audit records, the two root planning documents, the build and queue records, the `proposals/` directory, and every `testdata/` directory, each of which records a finding, a plan, or a fixture as it was written rather than the current contract. Either word may appear inside a canonical identifier, as `LNK-GWCONTROL` does. A markdown anchor identifier is outside the prohibition in both spellings, because a kramdown attribute value and the fragment of an intra-repo link are addressable link targets rather than prose, and an anchor that has to change moves through the anchor-redirect map so that a redirect exists for every inbound link. An identifier stem may not reuse a term the specification already binds to an unrelated mechanism.

**N4.** Each channel uses one identifier everywhere: the Go package or file name stem, the proto RPC name stem, the metric label value, and the test name fragment for a test scoped to one channel. A gate or a test spanning channels is named for the invariant it enforces and carries no channel identifier. The metric half of N4 is deferred. The remediation step that adds the adapter metrics endpoint and its catalog entries is the step that discharges it, because the adapter process emits those metrics inside the agent pod and they sit outside the default scrape target set until a deployer wires an adapter scrape target. The deferral carries a claim-register row with status `ABSENT` naming that step, per §28.4.

**N5.** A link identifier and the channel identifiers it carries share no stem, so a search for one never returns the other.

**N6.** A register is named for the store and the key rather than for a verb.

**N7.** A flag, environment variable, or manifest key naming a channel carries that channel's identifier in the form its carrier already fixes: a flag uses lowercase kebab, an environment variable uses upper snake, and a manifest key uses the camelCase convention the §4.7 adapter manifest field set establishes.

**N8.** A specification citation names a heading rather than a line. Citing a specification line number is retired and may not be written, in any spelling. The prohibition is on the line number rather than on one form of words, so a spelling a matcher does not yet recognize is a gap in the matcher rather than a permitted citation. A section that gives up content carries a permanent successor pointer naming the heading that now owns the content and the identifiers that moved. The citation resolver and the line-citation ratchet are the gates that hold this rule. The prohibition on line numbers in a `// spec:` citation, stated in `code-best-practices.md`, is the code-side half of N8.

## Reserved spellings

This file sits outside every part of the domain N3 names, so it writes the banned phrases out. The specification describes the two reserved words instead, because §28 sits inside the prohibition's own domain, and the naming lint holds the same spellings as a regular-expression literal in `scripts/specshift/name`. Both are checked against the phrases below.

| Reserved word | Space-separated spelling | Hyphenated compound spelling |
|:--|:--|:--|
| The word for a resource's phase transitions | `lifecycle channel` | `lifecycle-channel` |
| The word for a command plane | `control channel` | `control-channel` |

The plural is banned in both spellings, giving `lifecycle channels`, `lifecycle-channels`, `control channels`, and `control-channels`. The matcher is case-insensitive, so a capitalized or an uppercase spelling is the same site.

None of these phrases is a name. Each one names a conversation without saying which conversation, which is what N1 and N3 together forbid. The correction is the canonical identifier of the mechanism the sentence denotes, taken from the registers in §28.3. Widening the matcher, suppressing the site, or registering an exception is not a correction.

## Where these rules apply

- Every identifier under `spec/28_communication-channels.md`, and every reference to one from another specification section.
- Documentation under `docs/`, the schemas under `schemas/`, and the root-level markdown documents the exclusion list leaves in scope.
- Go package names, file name stems, symbol names, and doc comments under `pkg/`, `cmd/`, `sdks/`, and `tests/`.
- Proto RPC and message names, JSONL frame names, metric label values, flags, environment variables, and manifest keys that name a channel.
- Chat responses that propose a name for a link, a channel, or a register.

## How to apply when naming

1. Decide which of the three classes the thing belongs to, and read the register for that class in §28.3. An identifier that already exists is reused rather than renamed.
2. Choose a stem that is a mnemonic for the conversation, under N1, N2, and N5, and check it against the reserved spellings above and against the terms the specification already binds.
3. Write the identifier in each carrier in the form N4 and N7 fix for that carrier.
4. Add the register row in §28.3 for a new link, channel, or register, and the claim-register row in §28.4 for any part of the contract that does not yet hold in code.
5. Cite the specification by heading, under N8. Do not write a specification line number in a citation, a comment, or prose.

## Escape hatches

- A retired identifier spelling stands in the naming-table row that retires it, and nowhere else.
- A record that documents a finding, a plan, or a fixture as it was written keeps the words it was written with. The audit records, the two root planning documents, the build and queue records, the `proposals/` directory, and every `testdata/` directory are outside the reserved-phrase prohibition for that reason.
- A vendor's own flag or a third-party term quoted verbatim keeps its upstream spelling.

## Maintenance

When a naming defect surfaces that these rules do not cover, state the rule in §28.1 through the proposal pipeline first, then restate it here. §28.1 is the normative text and this file follows it. When a reserved word is added or removed, update the table above in the same change as the naming lint's matcher, so the specimens and the matcher stay one statement of the prohibition.
