# Steps: 0175 A formal model of the session lifecycle

## Validation owed

None beyond the model's own checks.

## Steps

### 0175.S1 Review and model of the session lifecycle
- status: Planned
- lanes: [non-specification]
- depends-on: []

Reviews the state machine of 0146 as 0166 extends it, writes the properties, builds the model under `tests/formal/`, runs it, and records the results and each confirmed counterexample as an open decision of this change. It starts after branch `change/0166-delegation-primitives` merges into `main` ("Ordering" in `change.md`).
