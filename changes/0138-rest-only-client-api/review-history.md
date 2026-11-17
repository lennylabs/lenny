# Review history: 0138-rest-only-client-api REST-only client API

- 2026-11-13: revision 1, written from the owner's answers to items 23 to 26, the spike of the same day, and an inventory of the specification, the designs, the documentation, and the tree. Not yet reviewed.
- 2026-11-14: two security reviews of the authorization rule of "RO-4" and O1. The first found the audience check a union and adapter tokens carrying full REST authority; the second, run at the owner's request ("run another security review"), reviewed per-adapter operation sets and found the enforcement, capability, and role gaps that "RO-4" now closes.
- 2026-11-14: revision 2, applying the owner's answers to O1 to O10 and both reviews' fixes.
- 2026-11-14: revision 3, applying the owner's answers to O11 to O13. Not yet reviewed.
- 2026-11-15: revision 4, applying the owner's answers of that day on `poolAttach` and the interrupt codes. Not yet reviewed.
- 2026-11-15: revision 5, applying the owner's decision of that day to remove connectors. Not yet reviewed.

# Revision notes

- Revision 1, 2026-11-13.
  - Records the owner's decisions D1 to D4.
  - Keeps the spike's R1 to R14 and adds R15 to R18 for the provider adapters, which the spike did not cover: conversation input on REST, the conditional send, the client reference, and close after turn. R12 becomes required, because the provider adapters need it. R7 becomes a stability rule under O6.
  - Finds, against the live RFC 9728 text, that a shared resource identifier equal to the origin fails a strict client that follows the challenge's metadata URL, and recommends the adapter's endpoint URL as the shared identifier (O1).
  - Finds that the Open Responses specification of `2026-04-24` defines no retrieval route, so `GET /v1/responses/{id}` is an OpenAI extension the adapter keeps.
  - Open decisions O1 to O10.
- Revision 2, 2026-11-14.
  - Records the owner's answers to O1 to O10 as settled decisions, with the owner's words, and the security fixes as S1 ("Ok on the adapter security fixes").
  - O1: the adapter resource identifier is a capability of every adapter, configured through the operator's adapter registry `auth.adapters[]`, from which the gateway computes identifiers, including external origins with their own identifiers.
  - Replaces revision 1's rule in "RO-4" with the combined rule set of both reviews: the registry, OpenAPI eligibility and capability tagging, audience classification, narrowing by attested peer or `Lenny-Resource` with `binding: attested` as the first-party default, per-registration evaluation with a role ceiling, leaf enforcement bound at route registration, minting refusals for token exchange under every grant, both playground mints, impersonation, and `lenny-ops`, the code `OPERATION_NOT_PERMITTED_FOR_RESOURCE`, stream close at expiry and revocation, webhook rules, the idempotency key scope, rate limits and stream caps, caching, audit, adapter obligations, the mutual TLS adapter hop, resource mappings before client ID aliases, the push relay's derived secret and sealed subscriber data, the limit on sender-constrained tokens, and same-origin browser rules. Rewords ROR-2 and ROR-4, amends ROR-3, and adds ROR-13.
  - O2: `callbackUrl` creates a `terminated` subscription under R6's rules.
  - O3: removes the close-after-turn option and the concept of a session kept for a provider surface; R18 is withdrawn. Provider adapters create ordinary sessions, may shorten the idle timeout, and route follow-ups by conversation fingerprint or response ID, waking `dormant` sessions; Chat Completions continues sessions the same way. R16 stays, with its pod condition replaced by the waiting states and its restore condition narrowed (O12).
  - O4: adds R19, the turn interrupt, with a suspension fallback for runtimes without `interrupt` (O11); `tasks/cancel` and `CancelTask` retract a pending message and otherwise call R19.
  - O8: nothing is deleted or moved; adapter designs carry banners, added by the cross-design pass.
  - O9: R13 ships in the first release on a separate download origin; R10 and R11 stay deferred, with the use cases that would warrant them.
  - O10: the A2A adapter's specification states and implements `input-required`.
  - Adds O11 to O13 as choices for owner review.
  - Updates the reads-against list to the current revisions and adds the runtime protocol design.
- Revision 3, 2026-11-14, and changes these rules.
  - O11 settled as (b), in the owner's words "I wouldn't change Lenny's behavior for this. The adapter can fall back": R19 sends `interrupt_request` only when the session's effective capabilities include `interrupt`, answers `409 INTERRUPT_UNSUPPORTED` when they do not and `504 INTERRUPT_TIMEOUT` when the runtime does not acknowledge within the deadline, and suspends nothing. The MCP adapter's `tasks/cancel`, the A2A adapter's `CancelTask`, and the playground's Stop button fall back to `POST /v1/sessions/{id}/suspend`; `lenny session interrupt` reports the code. The "Turn interrupt" term, O4's row, R19, "RO-7", "RO-8", "RO-9", "Limits", "Alternatives rejected", the §15.1 and §29.4 rows, the documentation, the tier 1 test, and the instructions to the MCP and lifecycle designs follow.
  - O12 ("O12: ok") and O13 ("O13: ok") settled as recommended. The three entries move to "Decisions", and "Open decisions" records that none is open.
  - Choices this revision makes: the codes `INTERRUPT_UNSUPPORTED` at `409` and `INTERRUPT_TIMEOUT` at `504`, which the owner confirmed on 2026-11-15 (revision 4).
  - The status line's reads-against list names the current revision of each cited design.
  - In the same revision, RO-4's `credential-use` class names a plan's `userCredential` binding entries in place of `credentialPolicy`, which `0133-llm-upstreams-and-cost-budgets` LU-4 removes. The post-chain check for a final pool that attaches a `userCredential` binding through `poolAttach`, which this revision added for the owner's review, is withdrawn by revision 4 (S2).
- Revision 4, 2026-11-15, and changes this rule.
  - **S2, `poolAttach` through an adapter.** The owner answered "(b). In this scenario, a user would nevertheless need to be authenticated even if using MCP, right? If that's the case I see no problem with (b)". A narrowed request is authenticated as the user whatever adapter carries it, and the operator configured the VCS pool to act as that user, so a `poolAttach` binding attaches through any adapter. Revision 3's post-chain check that required `credential-use` for it leaves "RO-4", and the class covers a plan's `onRequest` `userCredential` binding entries alone. The instruction to `0151-user-credential-delivery` and the tier 4 test follow.
  - **The interrupt codes.** The owner kept `409 INTERRUPT_UNSUPPORTED` and `504 INTERRUPT_TIMEOUT` ("3: ok"). The O11 row records it, and "Open decisions" no longer marks them for review.
  - The status line's reads-against list names the current revision of each cited design.
- Revision 5, 2026-11-15, the connector removal of [`0124-connector-removal`](../0124-connector-removal/change.md) revision 2, and changes these rules.
  - The owner decided on 2026-11-15: "Yes, let's drop connectors." Lenny offers no connectors of any kind.
  - The "What stays" table of "Scope" replaces the gateway's connector client with the harness's own MCP to operator services through egress, and names platform tools alone on tool frames.
  - `LINKS_REQUIRED` names account links alone, in `details.userLinks`, under UC-3. The UC-3 and CR label rows are new, and the CA-4 and CA-5 row records that both are withdrawn.
  - R4's provenance loses whether a connector raised an elicitation, and an operator service's elicitation reaches R4 as its runtime's question under B6. R12 drops the exemption of connector prompts and answers a forwarded operator-service question `decline` under `decline`. "RO-8" drops connector prompts.
  - "RO-4" drops connector-link management from the ineligible operations, and drops the `memory-write` capability class, whose memory routes CA-10 removed with built-in memory.
  - ROR-13 and its tier 9 test drop connectors from the places a client bearer must not reach.
  - The instructions to the MCP protocol upgrade and connector authorization designs keep their words as the record and note that MP-6, CA-4, and CA-5 are withdrawn. The A2A adapter of "RO-9" is unrelated and unchanged.
  - The status line's reads-against list cites the MCP protocol upgrade design at revision 9, the message delivery design at revision 23, the runtime protocol design at revision 11, the connector authorization design at revision 18, the security oversight design at revision 57, and the connector removal design at revision 2.
- Revision 6, 2026-11-15, follows [`0124-connector-removal`](../0124-connector-removal/change.md) revision 4, which records the owner's answer of that day to its O11, "go with (b)", with one prompt per user. When an account link stops working during a session, the gateway raises a link prompt under [`0151-user-credential-delivery`, "UC-8: Link prompts"](../0151-user-credential-delivery/change.md#uc-8-link-prompts). R4's provenance gains the link prompt's `initiator_type: platform`, `purpose: account_link`, and link provider, and states that one entry appears in each root's list it is shown on and resolves on all of them. R12 states that a link prompt stays pending under `inputPolicy: decline`. The status line reads against the removal design's revision 4, the connector authorization design's revision 20, and `0151-user-credential-delivery` revision 37.

## Move into changes (2026-11-16)

Change 0113 moved this design from `designs/rest-only-client-api-design.md` into this folder. Its review history moved into this file, its proposal plan became "Steps", its evidence moved into `evidence/`, its status became `Draft` pending the owner's final cross-review, and every path to a design or its evidence, in links and in prose, now names its new location. Sentences that named `designs/` as the current home of designs now name `changes/`; dated statements of what was read under `designs/` keep their words.
