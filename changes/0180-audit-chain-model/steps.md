# Steps: 0180 A formal model of the audit chain and redaction receipts

## Validation owed

None beyond the model's own checks.

## Steps

### 0180.S1 Model of the audit chain and redaction receipts
- status: Planned
- lanes: [non-specification]
- depends-on: [0177.S2]
- closes: []

Reviews §11.7, §12.8 step 14, and the verifiers as built through 0177.S2, writes the properties and the adversary's roles, builds the model under `tests/formal/`, runs it, and records the results and each confirmed counterexample as an open decision of this change. It starts after step 0177.S2 lands ("Ordering" in `change.md`).
