---
id: TOKX-I24
status: open
kind: spec-defect
severity: low
requirements: []
---

# The specification does not name the chart value that sets the reconnect spacing

## Specification

§13.3 (Credential Flow) **Authoritative durability for revocation.** states the reconnect spacing, 5 seconds by default, and that a gateway flag sets it. §17.8 (Capacity Planning and Defaults) lists the operator-tunable defaults with the chart values that set them, among them `auth.tokenLineageMaxDepth` and `auth.tokenRevocationTimeoutMs` of the same section.

## Evidence

- Step 0171.S3 adds the chart value `auth.revocationReconnectSpacing`, default `5s`, in `charts/lenny/values.yaml`, `charts/lenny/values.schema.json`, and `charts/lenny/templates/gateway-deployment.yaml`, and names it in `docs/reference/configuration.md` and `docs/operator-guide/security.md` (change 0171, decision 4).
- The step entry of 0171.S3 in `changes/0171-revocation-rehydrate-on-reconnect/steps.md` edits no specification text, because §13.3 states that a gateway flag sets the spacing and the chart value renders that flag.
- `spec/17_deployment-topology.md`, the defaults table of §17.8, has no row for the reconnect spacing, and `spec/` names neither `auth.revocationReconnectSpacing` nor `--revocation-reconnect-spacing`.

The general review of 0171.S3 raised it.

## Gap

The documentation names a chart value and its default that the specification does not define. `.claude/rules/doc-content.md` ("Names and values come from the catalogs") states that a setting a page names comes from the specification, and the catalog gate cannot check this name or its default once TOKX lands.

## Suggested resolution

When TOKX lands, or through a change before it, define the reconnect spacing as a setting with its default of 5 seconds, its chart value `auth.revocationReconnectSpacing`, and its flag, and add its row to the defaults table of §17.8.
