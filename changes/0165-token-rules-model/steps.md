# Steps: 0165 A formal model of the token rules

## Validation owed

None. The step records the Alloy results it relies on in `change.md`, under "Model results".

## Steps

### 0165.S1 Token rules model and property list
- status: Building
- lanes: [non-specification, specification]

The non-specification lane adds the Alloy 6 model under `tests/formal/tokens/` (`tokens.als`, the runner `run.sh`, and a README), runs every command at the scopes "Model results" in `change.md` states, and records the results and the counterexamples in this change. It adds no specification text.

The specification lane states the property list of "Properties" in `change.md` in §13.3 (Credential Flow), as the invariants the rules of that section keep, with the owner's answers to the open decisions of this change. It waits until change 0164 (token exchange narrowing and identity rules), step 0164.S1, merges, because 0164.S1 edits the same paragraphs of §13.3 and two branches editing them in parallel would conflict. The specification lane lands on this step's branch, rebased onto `main` after 0164.S1 merges, before the step merges. The dependency is stated here rather than in the `depends-on` field, because 0164 is not yet on `main`.

### 0165.S2 Generated property tests against the Token Service
- status: Planned
- lanes: [code]
- depends-on: [0165.S1]

Go property tests generate random sequences of exchanges, rotations, child mints, impersonations, playground mints, full revokes, and revocation requests, run them against the Token Service with an in-memory issued-token store, and check the properties of "Properties" in `change.md` after each operation. The generator draws scope claims from the §25.1 grammar, including an absent claim, `tools:*`, domain wildcards, and a value the canonical form refuses. The tests run in the unit tier beside `pkg/tokenservice`, cite §13.3, and start after the owner has answered the open decisions of this change, so they encode the rules as decided. The step depends on 0165.S1 for the property list and the model's scenarios, which become fixed regression cases.
