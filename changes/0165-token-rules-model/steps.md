# Steps: 0165 A formal model of the token rules

## Validation owed

None. The step records the Alloy results it relies on in `change.md`, under "Model results".

## Steps

### 0165.S1 Token rules model and property list
- status: Building
- lanes: [non-specification, specification]

The non-specification lane adds the Alloy 6 model under `tests/formal/tokens/` (`tokens.als`, the runner `run.sh`, and a README), runs every command at the scopes "Model results" in `change.md` states, and records the results and the counterexamples in this change. It adds no specification text. The lane is complete: the owner accepted its results, including the base-scope results for the two large-scope checks that did not finish (decision 14).

The specification lane states the property list of "Properties" in `change.md` in §13.3 (Credential Flow), as the invariants the rules of that section keep, with the owner's answers in decisions 2 to 12. It waits until change 0167 lands, because 0167 carries the fixes of decisions 3 to 11 and edits the same paragraphs of §13.3 next, and two branches editing them in parallel would conflict. Change 0164 (token exchange narrowing and identity rules), which edited those paragraphs before, merged to `main` with 0164.S1. The specification lane lands on this step's branch, after `main` with 0167 is merged into it, before the step merges. The dependency is stated here rather than in the `depends-on` field, because 0167 is not yet on `main`.

### 0165.S2 Generated property tests against the Token Service
- status: Building
- lanes: [code]
- depends-on: [0165.S1]

Go property tests generate random sequences of exchanges, rotations, child mints, impersonations, playground mints, full revokes, and revocation requests, run them against the Token Service with an in-memory issued-token store, and check the properties of "Properties" in `change.md` after each operation. The generator draws scope claims from the §25.1 grammar, including an absent claim, `tools:*`, domain wildcards, and a value the canonical form refuses. The tests run in the unit tier beside `pkg/tokenservice` and cite §13.3. The owner has answered the open decisions of this change, and under decision 14 the step starts after change 0167 lands, so the tests encode the rules with 0167's fixes. The dependency on 0167 is stated here rather than in the `depends-on` field, because 0167 is not yet on `main`. The step depends on 0165.S1 for the property list and the model's scenarios, which become fixed regression cases.
