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

### 0179.S2 The reduced model of the client-chosen upload secret
- status: Planned
- lanes: [non-specification]
- depends-on: [0182.S1]
- closes: []

0182 decision 4 and "UT-6" in `change.md`. Reduces the model under `tests/formal/uploadtokens/` to the properties FV-2 keeps: a secret is consumed at most once; a secret is accepted only for the session whose row records its digest, and only until the row's expiry; a session whose row records no upload phase accepts no secret; and two live sessions of one user never record one digest, across replicas and concurrent requests. It removes the key ring and the properties that concern it, reruns every configuration, records the results in `change.md` and the README, updates the `formal/` entry of `tests/README.md`, and records each confirmed counterexample as an open decision of this change. It reads §7.1 as step 0182.S1 edits it, and lands before M3 starts.
