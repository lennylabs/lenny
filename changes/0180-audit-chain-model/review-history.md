# Review history: 0180 A formal model of the audit chain and redaction receipts

- 2026-11-26, revision 1. Created as `Draft` at the owner's request of 2026-11-26, as the formal verification FV-3 that "Formal verifications owed before M3" in the v1 program lists. It starts after step 0177.S2 lands.
- 2026-11-26. The owner settled open decisions O1 (TLA+, with the model kept under `tests/formal/auditchain/` for reruns) and O2 (outside the v1 program) in chat on 2026-11-26, and approved starting the change in the order 0179, 0180, 0175 after steps 0177.S3 and 0177.S4.
- 2026-11-27. Step 0180.S1 built the model under `tests/formal/auditchain/`, ran every configuration, and filled in the high-level design, the current state, the detailed design, and the model results. The counterexamples raised open decisions O3 to O7 and issues AUDIT-I9 to AUDIT-I14; AUDIT-I15 and AUDIT-I16 were found by reading the code; AUDIT-I6 and AUDIT-I7 gained the model's traces.
