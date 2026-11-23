# Steps: 0165 A formal model of the token rules

## Validation owed

None. The step records the Alloy results it relies on in `change.md`, under "Model results".

## Steps

### 0165.S1 Token rules model and property list
- status: Building
- lanes: [non-specification, specification]
- depends-on: [0167.S1 (part)]

The non-specification lane adds the Alloy 6 model under `tests/formal/tokens/` (`tokens.als`, the runner `run.sh`, and a README), runs every command at the scopes "Model results" in `change.md` states, and records the results and the counterexamples in this change. It adds no specification text. The lane landed on `main` with the merge of `change/0165.S1-token-rules-model` (gate run `eeadaf5be3f10ceeda2ff1d5` on `82edbbb86`): the owner accepted its results, including the base-scope results for the two large-scope checks that did not finish (decision 14). The model's README and comments were brought in line with the rules as built after 0167 on the branch of the specification lane, with no change to a check.

The specification lane states the property list of "Properties" in `change.md` in §13.3 (Credential Flow), under the bold label **Token rule properties.**, as the properties the rules of that section keep after change 0167, with the outcomes the owner accepted (CX-7, CX-12, and the rotation link's denial of service of 0164 decision 10). It waited on 0167, which carries the fixes of decisions 3 to 11 and edited the same paragraphs of §13.3; that part of the dependency is the `(part)` entry above, and 0167.S1 has landed. The lane is on branch `change/0165.S2-token-properties` with 0165.S2, because the property tests cite the list and the two steps land together.

### 0165.S2 Generated property tests against the Token Service
- status: Building
- lanes: [code]
- depends-on: [0165.S1, 0167.S1]

Go property tests generate random sequences of sign-ins, exchanges (with and without `scope`, narrowings, rotations, and exchanges by another caller), child mints, impersonations, playground mints, recursive-revocation requests, full revokes, and leaks of a token to another user, and check every item of §13.3 **Token rule properties.** after each operation. They run against the real Token Service, the apiKey-mode playground mint, the impersonation issuer, and the full revoke's store calls over one Postgres issued-token store with the production migrations, so they sit in the component tier, in `tests/component/tokenproperties`, rather than beside `pkg/tokenservice` with an in-memory store as first planned: no in-memory store carries the recursive revocation, the impersonator link, or the step (1a) re-read, and a fake of them would test the fake. The generator uses `pgregory.net/rapid`, already in `go.mod`, with a fixed seed and 200 sequences of 8 to 20 operations by default; longer runs pass `-rapid.checks`. The generator draws scope claims from the §25.1 grammar, including an absent claim, `tools:*`, domain wildcards, and a value the canonical form refuses. A second test replays the model's counterexamples CX-1 to CX-13 as fixed sequences under the same checks. The step found two sequences that break an item of the list, recorded as open decisions 1 and 2 in `decisions.md`; it changed no code.
