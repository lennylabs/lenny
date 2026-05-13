# Documentation style

Project-wide style rules for Podium documentation, the README, the spec, and any other prose intended for readers. These rules apply to all `.md` files in the repository and to chat responses about doc content.

## Top-level principle

Documentation must be factual and dry. State what is true, what something does, and what to do. Use complete declarative sentences with explicit subject-verb-object structure.

## Minimal style policy

- Write in a neutral technical style.
- Use complete declarative sentences.
- Avoid rhetorical emphasis, dramatic phrasing, and marketing cadence.
- Include conjunctions in lists using standard written English (`a, b, and c`; `x, y, or z`).
- Prefer explicit behavioral descriptions over conversational summaries.

## Strong anti-AI-style policy

Do not use any of the following.

### Em-dash overuse

Em-dashes are acceptable for genuine asides. They are not acceptable as decorative beats every other sentence, as soft pauses for rhythm, or to set off a contrast that does not need it. If a sentence reads naturally with a comma, period, parenthesis, or colon, use one of those instead.

### "X not Y" / "X, not Y" rhythm constructions

**Minimize this pattern. Avoid it altogether wherever possible.**

Examples to avoid:

- `Not X — it's Y.`
- `Not just X, but Y.`
- `Less X, more Y.`
- `X — not Y.`
- `X, not Y.`
- Section headings shaped as `## Why hints, not requirements` or `## X vs Y`.
- Punchline-style closures: `The intermediate steps were comfort, not correctness.`

The rhythm of "X, not Y" reads as AI emphasis even when both halves carry information. Treat it as the highest-priority pattern to remove.

Rewriting strategies:

- Drop the negation when one half is filler. `There is one canonical implementation per concern, not three.` → `There is a single canonical implementation per concern.`
- Replace with `rather than` when both halves convey real contrast and the alternative is obvious. `forward compatibility, not present-day distribution` → `forward compatibility rather than present-day distribution`.
- Split into two complete sentences when both halves are load-bearing. `These are advisory hints, not enforceable requirements.` → `These fields are advisory. The host decides whether and how to honor them.`
- For headings, name the noun directly. `## Why hints, not requirements` → `## Advisory framing`.

The pattern is sometimes acceptable — for instance, when contrasting two literal values in a configuration error message, or when negating an expected reading the reader is likely to make. When it shows up in your prose, ask whether the rewrite is feasible before keeping it.

### Conversational intros and second-person framing

Examples to avoid: "You're here to write artifacts.", "We'll grow it into…", "By the end you'll have…", "Let's start with…", "If you're new, do X first." Documentation states what the page covers, not what the reader is doing or feeling. Use imperative mood for instructions and declarative mood for facts. Skip "welcome" framing entirely.

### Sentence fragments for emphasis

Examples to avoid: "Just a folder.", "No daemon. No port. No auth.", "Done.", "That's it.", "End of story." These read as dramatic punctuation rather than information. Convert each fragment to a complete sentence, or fold it into the surrounding sentence.

### Rhetorical closures

Examples to avoid: "The agent reads it; nothing more.", "Nothing else needed.", "And that's all there is to it." Stop the paragraph after the substantive content. The closure adds rhythm, not information.

### Conversational punchlines

Statements that exist to land an emotional beat rather than convey information. If a sentence does not state a fact, describe a behavior, or give an instruction, remove it.

### Omitted conjunctions in lists

Standard written English uses `and` or `or` before the last item: "a, b, and c". Lists like "Style guides, glossaries, API references, large knowledge bases" or "no daemon, no port, no auth" are fragment-style emphasis and should be reworded.

### Marketing or sales language

Examples to avoid: comprehensive, robust, seamless, powerful, leverage, deliver, unlock, elegant, world-class, best-in-class, industry-leading, game-changing.

Also avoid "real X" and "true X" used as emphasis (e.g., "a real artifact", "a real catalog"). When the contrast is between a placeholder and a working example, name the contrast explicitly.

### Hyperbole

Examples to avoid: biggest, existential, load-bearing, the only X, the cleanest, the most important. Drop the superlative; state what is true.

### Hedging

Examples to avoid: could help, may benefit, might consider, could become beneficial. Either it does or it does not; pick one.

### Defensive flourishes

Examples to avoid: "not a slogan", "this is what makes Podium special", "as we'll see", "rest assured". State the thing.

### Corporate possessives

Examples to avoid: "for an organization's library", "what Podium gives an organization", "the organization's catalog". Use functional framing.

### Formulaic AI tics

Examples to avoid: earns its place, earns the complexity, value compounds, first-class citizen used reflexively, deliberately scoped, intentionally thin, let's dive in, let's explore, navigate the complexities, in today's world.

### Tricolons and parallel triples

Three-element parallel constructions used for rhythm rather than meaning: "Same artifacts, same author flow, same shared library."; "Fast. Reliable. Scalable." filler lists. Convert to a single complete sentence with a conjunction.

### Stock connectors

Examples to avoid: Importantly, Notably, It's worth noting, In essence, Simply put, At its core, In other words, To put it simply.

### Headline definite articles

"Podium is **the** X for Y" overclaims. Use "**a** X for Y" or just "X for Y" unless the definite article is genuinely accurate.

### Explicit counts of capabilities, types, SPIs

Examples to avoid: "the seven first-class types", "17 SPIs", "the four meta-tools", "three deployment shapes", "five steps". Counts go stale when the underlying surface changes, and they rarely add information when the list itself is right there.

Reword to avoid the count: "the first-class types", "the SPIs", "the meta-tools", "the deployment shapes". Section headings that name a count (`## The four meta-tools`) should be renamed to the noun form (`## Meta-tools`).

### Intensifiers and emphasis adverbs

Examples to avoid: very, really, just, simply, truly, actually. Use only when technically necessary.

### "Shape" as a generic noun

Do not use "shape" as a generic noun for data structures, schemas, formats, configurations, APIs, or document organization.

Examples to avoid: "the deployment shape", "the consumer shape", "the registry shape", "the wire shape", "the response shape", "harness-native shape", "delivery shape", "MCP shape".

Replace with the specific technical term: deployment **mode** / **topology**, consumer **path** / **integration**, **format**, **layout**, **structure**, **schema**, **convention**. Pick the term that matches what the reader will actually encounter.

### Vague causal verbs

Avoid "shape," "drive," "enable," "facilitate," and "support" when a more precise verb is available. Describe the specific mechanism or effect directly.

Examples to avoid:

- "The directory layout drives the domain hierarchy." → "The directory layout defines the domain hierarchy."
- "Cross-type dependency edges drive impact analysis." → "Cross-type dependency edges feed impact analysis."
- "A health-state machine drives the transition." → "A health-state machine governs the transition."
- "Reordering via `podium layer reorder` is supported." → "Use `podium layer reorder` to change order."
- "Nesting is intentionally not supported." → "Nesting is intentionally absent." (or "Profiles cannot nest.")
- "What each shape supports is in §13.11." → "What each mode covers is in §13.11."

When you have to use one of these verbs, the action and its target should both be specific enough that the reader can reconstruct the mechanism.

## Prefer

- Explicit subject-verb-object constructions.
- Concrete operational language.
- Precise terminology.
- Grammatically complete prose.
- Concrete numbers, paths, and code over adjectives.
- Imperative mood for instructions ("Run `podium init`").
- Declarative mood for facts ("The registry stores manifests in Postgres.").

## Example identifiers

Use placeholder names from the cryptography convention when an example needs a human user. The first user is `alice`, the second is `bob`, then `carol`, `dave`, and so on. This applies to user IDs (`alice@acme.com`), filesystem paths (`/Users/alice/...`), layer identifiers (`alice-personal`), Git repositories (`git@github.com:alice/...`), and any other example that names a specific person.

Do not use the names of real people, including the project author, maintainers, or contributors. Real names create the impression that the example reflects a specific configuration rather than a generic illustration, and they age poorly as team membership changes.

For example tenants and organizations, use `acme` (`acme.com`, `Acme Corp`) unless the example needs to distinguish between multiple tenants, in which case extend with `acme`, `globex`, `initech`.

## Where these rules apply

- Documentation site (`docs/`).
- The repo README (`README.md`).
- The technical specification (`spec/`).
- Inline code comments that constitute prose (more than a one-line tag).
- Chat responses about doc content.

## How to apply when editing

1. Read each paragraph for fragments. Convert any sentence that lacks a subject or a verb into a complete sentence, or fold it into the adjacent sentence.
2. Read each list. If the last item is preceded by a comma instead of `and`/`or`, fix the list.
3. Search for em-dashes. Replace with comma, period, parenthesis, or colon unless the em-dash sets off a genuine aside.
4. Search for "real X", "true X", "just X", "very X". Reword.
5. Search for explicit counts. Reword to remove the count when the count is incidental.
6. Search for closures like "That's it.", "Done.", "Nothing more." Remove.
7. Search for "We'll", "You'll", "Let's", "By the end". Reword in declarative voice.

## Escape hatches

- Code blocks, ASCII diagrams, and other visual elements are not subject to these rules. A diagram label like `No daemon. No port. No auth.` inside an ASCII art box is a visual marker, not prose.
- Direct quotations from external sources are preserved verbatim.
- Quoted error messages, logs, and command output are preserved verbatim.

## Maintenance

When new failure modes surface in review, add them to the relevant section above. Do not let the file grow into a thesaurus; keep each rule actionable and specific.
