# Decisions: 0179 A formal model of the upload-token keys and consumed state

## Open decisions

None.

## Decisions

1. **The modelling tool, and the model kept for reruns (O1).** TLA+ with the TLC model checker, reusing the runner and the pinned `tla2tools.jar` of change 0169. The model is stored under `tests/formal/uploadtokens/` with its module, one configuration file per scenario, a `run.sh` that runs every configuration, and a `README.md` that states what the model covers, the properties in plain words, how to run it, and its results, as `tests/formal/tokenconcurrency/` does, so that a later change that edits a rule the model describes updates the model and reruns it. The `formal/` entry of `tests/README.md` names the model. Owner's answer in chat, 2026-11-26, with the words "go ahead with 1-4" to the recommendation of TLA+, and "Store the TLA+ models so that we can update and re-run them later if we have to make changes". The options not chosen were Alloy, as change 0165 used, and a model-based property test in Go.
2. **Program membership (O2).** The change stays outside the v1 program, as change 0172 does, and "Formal verifications owed before M3" in [the v1 program](../programs/v1.md) tracks it as FV-2. Owner's answer in chat, 2026-11-26, with the words "go ahead with 1-4" to that recommendation. The option not chosen was membership in milestone M3.
