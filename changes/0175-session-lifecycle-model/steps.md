# Steps: 0175 A formal model of the session lifecycle

## Validation owed

None beyond the model's own checks.

## Steps

### 0175.S1 Review and model of the session lifecycle
- status: Building
- lanes: [non-specification]
- depends-on: []

First moves the rules 0166 adds to the session state machine into 0146, as revisions of SL-8 to SL-12, with a pointer from 0166 to each rule (decision 1). Then reviews the state machine of 0146, writes the properties, builds the model under `tests/formal/`, runs it, and records the results and each confirmed counterexample as an open decision of this change. It starts after branch `change/0166-delegation-primitives` merges into `main` ("Ordering" in `change.md`).
