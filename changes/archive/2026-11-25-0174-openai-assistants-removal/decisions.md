# Decisions: 0174 Removal of the openai-assistants reference runtime

## Open decisions

None.

## Decisions

1. **The `openai-assistants` reference runtime.** Settled by the owner on 2026-11-20, question DV-1 of the M3 readiness decision page, option (a), recorded as decision 12 of program [v1](../../programs/v1.md#decisions): a small change removes it, with its mentions in the specification (§26.10, §5, §14, and §15), its entry in `pkg/embedded/stack/catalog.go`, the embedded manifests, the chart values and the chart's reference-runtimes test, and the documentation. OpenAI shut the Assistants API down on 2026-08-26, as desk validation VAL-017 found. This change is that small change. The options not taken were (b), a new reference runtime over the Responses and Conversations APIs through its own change, and (c), keeping the runtime.
