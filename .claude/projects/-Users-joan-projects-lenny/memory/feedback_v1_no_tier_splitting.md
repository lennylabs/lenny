---
name: v1 single implementation
description: Lenny v1 has one implementation path — no tier-dependent alternate implementations or conditional architectures based on scale
type: feedback
---

No tier-dependent alternate implementations in v1. When capacity tiers (Tier 1/2/3) were introduced, some fixes added "use Postgres as state store at Tier 3" or "switch to Redis Cluster at Tier 3" — user clarified there's only one implementation in v1. Tiers define performance targets and sizing, not different code paths.

**Why:** v1 is a single codebase shipping one architecture. Tier-dependent implementation splits add complexity and testing burden that aren't justified yet.

**How to apply:** When fixing findings, don't propose "at Tier N, use implementation X instead of Y." Instead, design for the target tier (Tier 2) with the understanding that Tier 3 is reached via horizontal scaling of the same components, not architectural swaps.
