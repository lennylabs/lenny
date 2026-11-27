---
id: TOKX-I26
status: open
kind: spec-defect
severity: low
requirements: []
---

# Self-rotation of a token ends at the lineage depth cap, which bounds how long a client can keep one token chain

## Specification

§13.3 **Lineage of derived tokens.** and the token exchange depth limit (`auth.tokenLineageMaxDepth`).

## Evidence

- Each self-rotation records the replaced token as its parent, and exchanges are refused with `lineage_depth_exceeded` once the depth reaches `auth.tokenLineageMaxDepth`, default 32 (`cmd/lenny-token-service/flags.go:272`).
- A client rotating before each 900-second expiry therefore loses its rotation chain after about 32 rotations, roughly 5 to 8 hours, and must mint again, which starts a new idempotency record scope under change 0178 decision 8.
- Found by the adversarial review of a per-subject idempotency key with a conflict error, on 2026-11-26.

## Gap

The depth cap, meant to bound delegation, also bounds plain self-rotation, which adds no authority.

## Suggested resolution

Decide whether a self-rotation counts toward the lineage depth, and state the result in §13.3 and in the retry rule of §11.5.
