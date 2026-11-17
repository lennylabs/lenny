# Steps: 0155 Test tiers by name and the static and documentation tiers

## Validation owed

## Steps

### 0155.S1 Name the tiers
- status: Building
- lanes: [specification, code, documentation]
- depends-on: [0115.S9]

A script renames the tier directories and package clauses and rewrites paths and prose outside the historical records, `lenny-test` resolves names to the new directories, and `TESTING.md` keeps a table from the old numbers to the names. It lands after 0115 merges and before any area change starts.

### 0155.S2 Split the static tier
- status: Building
- lanes: [code, documentation]
- depends-on: [0155.S1, 0154.S2]

The `build`, `chart`, `lint`, and `structure` packages with a time budget, run concurrently by the gate, and the retirement of the category E gates that 0115's gates cover.

### 0155.S3 Unit tests beside their code, and the rule for documentation checks
- status: Building
- lanes: [code, documentation]
- depends-on: [0155.S1]

The tests of `tests/tier1_unit/` moved beside their code, the rule that a new documentation check is a general gate or a generator check and that no test matches specification prose, and the area exit criterion in the v1 program.
