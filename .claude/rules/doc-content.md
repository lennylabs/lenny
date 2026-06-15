# Documentation content and structure

Project-wide rules for what Lenny's reader-facing documentation says and how each page is organized. They apply to every `.md` file under `docs/` and to any agent or workflow that writes or edits documentation. They complement `doc-style.md`, which governs prose mechanics, and `doc-diagram-style.md`, which governs diagrams. Where `doc-style.md` says how a sentence should read, this file says what a page should contain and how it should be arranged.

## Top-level principle

A documentation page is correct, complete for its scope, written for its reader, and ordered, within the page and across the documentation, as a progression the reader can follow. Every technical claim traces to the spec. Each page states what it covers and for whom, leads with the reader's primary path, presents alternatives as alternatives, and pitches its level of detail to its purpose. A page that contradicts itself, omits part of its subject, reads as a disconnected list of facts, or asserts behavior the spec does not define is a defect, regardless of how polished each sentence is.

## Write for the reader

Documentation serves the reader's task and mental model, rather than mirroring the system's internal structure. Organize a page around what the reader is trying to do or understand, and use the reader's vocabulary, introducing a platform term at the point the reader first needs it.

- Every paragraph must be readable by the page's intended reader without unstated context. A paragraph that only a maintainer, or someone who has already read the spec or the source, can follow is a defect on a reader-facing page.
- Define a term before relying on it. Do not reference a concept, field, or component the page has not yet introduced or linked.
- Keep one idea to a paragraph and lead with the point. The reader gets the takeaway from the first sentence and the support from the rest.

## Give the page a throughline

A page reads as a connected progression, where each section follows from the one before it and a reader can move from the top to the bottom without backtracking. Order the material so that concepts are established before they are used and each section advances the reader toward the page's goal.

- Open with the context a reader needs: what the page covers, who it is for, and where it fits. State the why before the how when the why is not obvious.
- A page should not read as a set of independent facts that could be reordered without loss. When sections have no relationship to each other, connect them or split them into separate pages.
- Carry the reader across section boundaries. A new section states how it relates to what came before when the connection is not self-evident.

## Order the documentation as a whole

The throughline applies across the whole documentation as well as within a single page. The set of pages has a structure a reader can follow, and each page sits where a reader would look for it.

- Order a section's pages as a reading path, from orientation to detail. A section landing page states what the section covers, who it is for, and the order to read its pages, and it links to each.
- Place a page in the section that matches its reader's task. A page that serves a different audience than its section belongs in that other section, with a link from where a reader might first look.
- Introduce each concept once, in the page that owns it, and link to that page from everywhere else that refers to it. Re-explaining the same concept across several pages produces duplicates that drift apart.
- Every page is reachable from its section index and links onward to the next step. A page that nothing links to, or that leads nowhere, is a gap in the path.
- Set the navigation order so the rendered sidebar matches the intended reading order.

## Match technical depth to the page

Calibrate the level of detail to the page's purpose and its reader. The depth that suits an overview is wrong for a reference, and the reverse.

- A conceptual or getting-started page stays at the level of concepts and outcomes, and links to the reference for exhaustive detail. Do not drop wire-level frames, full field tables, or internal mechanics into a page whose reader wants to understand what something is and why it exists.
- A reference page is exhaustive and precise about the surface it documents. Do not leave it vague or rely on the reader to infer the fields it omits.
- A guide or tutorial carries enough detail for the reader to complete the task, and no more. When a step needs deep background, summarize it and link to the explanation rather than inlining the whole thing.
- When a page must serve both a conceptual reader and a detail-seeking reader, separate the depth: a short conceptual lead, then the detail in clearly marked sections the conceptual reader can skip, or a link to a reference page.

## Verify against the spec before asserting behavior

The spec under `spec/` is the source of truth for platform behavior (see `spec-driven-development.md`).

- Before documenting how something works (a protocol, a default, a capability, an error path), confirm it against the relevant spec section. Do not infer behavior from the name of a flag, the structure of an example, or what would be convenient to write.
- When the spec and an existing doc disagree, the spec is right and the doc is the defect. When the spec is silent on something a reader needs, that is a spec gap. Raise it through the proposal pipeline rather than inventing an answer in the documentation.
- Reader-facing documentation must stand on its own for a reader who does not have the spec. Do not cite spec section numbers (`§4.7`) in published prose; section numbers are internal and shift. State the behavior and link to the relevant documentation page. The `// spec:` citations defined in `code-best-practices.md` belong in code rather than in published documentation.

## State the recommended path and mark alternatives

- When the platform supports more than one way to do something, name the recommended or standard path and lead with it. Present each alternative after it, labeled with who it is for and when to choose it.
- A guide page leads with the path its primary reader takes. Capabilities aimed at a different audience are summarized and linked, rather than interleaved with the main path. For example, an author guide leads with the supported authoring model and links the advanced or first-party-only variant instead of giving both equal weight.

## Comparison tables

- A table that compares options (modes, levels, commands, backends) states, for each option, what it is or runs, what it gives the reader, and its limitations or when to use it. A table that lists only names with one-line descriptions does not let a reader choose.
- Do not organize a comparison around an incidental axis (the host operating system, for example) when the real axis is a platform concept such as an integration level or a deployment mode. Put an incidental caveat in the row it affects, rather than in the structure of the table.

## Keep a page internally consistent

- Tables and prose on the same page must agree. When one row states that a capability is absent, another row must not assume it is present. Re-read every table against the surrounding prose before finishing.
- Reading orders, numbered steps, and cross-references resolve to real, current locations. When a page moves, update every inbound link and every reading-order entry that points to it.

## Cover the page's whole subject

- A page that introduces a category covers its full scope, rather than the common case alone. When a category has more than one member (the runtime types, the credential sources, the isolation profiles), name each member, even when the page then focuses on the common one.
- Do not describe the default case as though it were the only case. State the default, then note where the other cases are documented.

## Do not anchor prose to transient output

- Do not quote a log line, a startup banner, a console message, or other runtime output as the authoritative statement of a behavior. Quoted strings drift when the code changes, and a reader who sees different text assumes the docs are wrong. State the behavior in prose. When an exact string matters to the reader, mark it as illustrative and keep the surrounding sentence true on its own.

## Counts and enumerations

Avoid stating a count of things the platform provides (`the nine built-in runtimes`, `the four reference wrappers`). Counts go stale when the set changes, and the list beside the count already carries the information. Remove the count and name the set. This repeats a `doc-style.md` rule because stale counts surface in review as a content defect, beyond their stylistic cost.

## Place reference material in Reference

A deep protocol, wire-format, or schema reference belongs in the Reference section, where readers look things up. A guide page explains how and when to use that material and links to the reference, rather than embedding the full specification inline. When a guide page grows into an exhaustive field-by-field reference, move the reference content to `docs/reference/` and leave the guide pointing to it.

## Where these rules apply

- All `.md` files under `docs/`.
- Chat responses that propose documentation content.

## How to apply when editing

1. Identify the page's audience and its primary path. Confirm the page leads with that path, presents other audiences as secondary, and sits in the documentation section that matches its reader's task.
2. Read the page top to bottom as the intended reader. Confirm each paragraph is understandable without unstated context, the sections progress without backtracking, and the depth matches the page's purpose.
3. For every technical claim, find the spec section that backs it. If none exists, stop and raise a proposal before documenting the behavior.
4. Check each comparison table: every option states what it is, what it gives the reader, and its limitations. Confirm the table's organizing axis is a platform concept.
5. Re-read every table against the page's prose for contradictions, and confirm that every link and reading-order entry resolves, that the page is reachable from its section index and links onward, and that its navigation order reflects the reading order.
6. Confirm the page covers its whole subject, names the recommended path, quotes no transient output as fact, and states no stale count.
7. Apply `doc-style.md` to the prose and `doc-diagram-style.md` to any diagram.

## Escape hatches

- API reference pages generated from a schema follow the generator's structure and field order.
- ADRs and other historical records describe a decision at a point in time and are not held to the current-behavior rule; they record what was decided and when.
- Release notes and changelogs reference specific versions and are exempt from the no-transient-output rule for the strings they quote.

## Maintenance

When a review surfaces a recurring content or structure defect this file does not cover, add a specific, actionable rule. Keep each rule actionable and tied to a defect a reviewer can catch. Do not restate `doc-style.md` except where a stylistic tic is also a content defect.
