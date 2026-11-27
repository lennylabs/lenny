# Steps: 0179 A formal model of the upload-token keys and consumed state

## Validation owed

None beyond the model's own checks.

## Steps

### 0179.S1 Model of the upload-token keys and consumed state
- status: Landed
- lanes: [non-specification]
- depends-on: [0177.S2]
- closes: []

Reviews the upload-token rules of §7.1 and change 0177 as built through 0177.S2, writes the properties, builds the model under `tests/formal/`, runs it over configurations of two and three replicas and the default and changed durations, and records the results and each confirmed counterexample as an open decision of this change. It starts after step 0177.S2 lands ("Ordering" in `change.md`).
