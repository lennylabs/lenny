### FLR-001 Billing Stream MAXLEN Formula Uses Incorrect RTO Window [High]
**Files:** `17_deployment-topology.md` (line 1027), `12_storage-architecture.md` (line 151)

The Tier 3 billing Redis stream MAXLEN derivation formula uses an incorrect RTO window duration. The specification states that Postgres RTO is `< 30s` (12_storage-architecture.md:151), but the billing stream MAXLEN calculation in 17_deployment-topology.md uses a 60-second window: `600 × 60 × 2 = 72,000`. This is mathematically inconsistent.

With the stated 30s RTO and 2x safety factor, the correct formula should be:
- `600 events/s × 30s × 2 = 36,000` (not 72,000)

The current 72,000 value absorbs a 60-second Postgres outage window, which is 2x the stated RTO. This represents either:
1. An undocumented RTO increase (the actual RTO tolerance is 60s, not 30s), or
2. An over-provisioning error in the formula derivation

**Recommendation:** Clarify the actual Postgres failover RTO target for Tier 3. If `< 30s` is the true target, correct the billing stream formula to `600 × 30 × 2 = 36,000`. If operational experience has shown that 60s RTO is necessary, update Section 12.3 to state `RTO < 60s` for Tier 3 and add a note explaining the extended window versus lower tiers.
