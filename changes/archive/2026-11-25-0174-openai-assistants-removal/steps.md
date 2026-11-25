# Steps: 0174 Removal of the openai-assistants reference runtime

## Validation owed

None.

## Steps

### 0174.S1 Remove openai-assistants
- status: Building
- lanes: [specification, code, documentation]

§26.10 and the mentions of `openai-assistants` in §5.1, §14, §15.7, §26.1, and the specification's root map are deleted (decision 1). The embedded catalog, the chart values, the compliance catalog, the `lenny-ctl install` reference names, the rendered manifests, the generated CRD descriptions, and the tests that named the runtime drop it. The documentation pages and the root-level records that list the catalog drop it.
