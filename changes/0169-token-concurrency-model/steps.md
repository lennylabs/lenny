# Steps: 0169 A concurrency model of the token rules

## Validation owed

Each configuration of the model runs with the pinned `tla2tools.jar` at the bounds open decision 5 settles, and `change.md` records, per configuration, the TLC release, the jar's SHA-256, the bounds, the number of distinct states, the run time, and the result, under a "Model results" section that the steps add. The model of `Main` reproduces TOKX-I13, TOKX-I20, and TOKX-I22 before any other result of 0169.S1 is reported. The code lane of 0168.S1 waits on the results of 0169.S2 and on the owner's answers to the counterexamples it reports.

## Steps

### 0169.S1 Concurrency model of the token rules on main
- status: Planned
- lanes: [non-specification]
- depends-on: [0165.S2]

Adds the TLA+ model under `tests/formal/tokenconcurrency/` (the module, the configuration files, the runner `run.sh`, and a README that maps each process to its code and each property to its rule) and the `formal/` entry of `tests/README.md`, with the variant `Main`: the rules on `main` after change 0167 and step 0165.S2, as "Current state" in `change.md` lists them. It reads the code of each process before writing it, including the revocation cache in `pkg/credential/revocation` and `pkg/auth/authmw`, and records each difference between the code and the text, as 0165 recorded its gaps G1 to G7. It runs the properties C1 to C9 under `Main`, C8 over the nested transactions of rule 1 (c) of **Platform-admin impersonation.**, which `Main` already has, and records the results, and writes each counterexample as a scenario with alice, bob, and carol in acme and globex. A counterexample that an open issue already records (TOKX-I13, TOKX-I20, and TOKX-I22) is recorded against that issue, and any other becomes an open decision of this change; no rule is fixed here. The step depends on 0165.S2, whose **Token rule properties.** the properties refine and whose decision 14 this change amends; 0165.S2 depends on 0167.S1.

### 0169.S2 The locking design of 0168 as a variant
- status: Planned
- lanes: [non-specification]
- depends-on: [0169.S1]

Adds the variant `TF`, which replaces the recursive-revocation request, the impersonation start, the full revoke, and step (1a) with the design of change 0168 (its decisions 1, 2, 5, and 6 and its sections TF-1 to TF-5), and, under open decision 7, the variant `I13Lock`. It runs C1 to C10 under `TF`, and the properties decision 7 names under `I13Lock`, and compares each result with the result under `Main`. Each counterexample is confirmed against 0168's `change.md` and reported to the owner as an open decision of this change before the code lane of 0168.S1 starts; the fix goes into 0168's design, and the model's `TF` processes change with it.

The code lane of 0168.S1 depends on this step, and its specification lane does not (decision 2). The edge belongs in the `depends-on` field of 0168.S1 as `0169.S2 (part)`, with its description naming the code lane as the waiting part. This change edits no record of 0168, whose records are on branch `records/0168-token-fixes`, so the edge is added there.
