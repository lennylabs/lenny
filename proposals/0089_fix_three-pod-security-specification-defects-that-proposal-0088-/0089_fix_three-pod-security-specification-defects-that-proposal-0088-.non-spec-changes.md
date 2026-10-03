# Non-spec changes: Pod-security specification defects left unstaged by proposal 0088

## Design (implementation-facing)

Groups A and C change specification text only. Group B removes the one place the code treats an admission rejection label as a §15.1 API code and points the comments that describe the labels at §17.2 **Rejection labels.**. No emitted denial, HTTP status, or message changes.

## Staged code changes

### CODE-B · errorclassify entry and rejection-label comments

1. `pkg/gateway/externalapi/errorclassify/errorclassify.go`: delete the `"EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN"` map entry together with its trailing `// spec: 15:1099` comment. Leave every other entry, including their line-citation comments, unchanged.
2. `pkg/admission/ephemeral_container_cred_guard/guard.go`: replace the doc comment on `RejectionCode` with:

   ```go
   // RejectionCode is the admission rejection label carried in every denial message.
   // It is not a §15.1 API error code.
   // spec: §17.2 (Rejection labels)
   ```

3. `pkg/admission/ephemeral_container_cred_guard/guard_test.go`: in `TestDecideRejectionCarriesTheSpecCode`, change the failure message `"rejection reason %q does not carry the §15.1 code %s"` to `"rejection reason %q does not carry the rejection label %s"`.
4. `tests/tier9_security/admission_ephemeral_test.go`: replace the comment on the `ephemeralCredRejectionCode` constant with `// ephemeralCredRejectionCode is the §17.2 rejection label the guard carries in every denial message.` Locate it by the constant name, because 0088 TEST-1 rewrites other comments in this file.
5. `pkg/admission/registry_digest/guard.go`: replace the doc comment on `RejectCode` with:

   ```go
   // RejectCode is the rejection label at the start of every denial this
   // webhook returns. It is a reason inside the Kubernetes admission denial
   // message rather than a §15.1 API error code, and logs, events, and
   // preflight findings match on it. The cosign-verify webhook
   // (IMAGE_SIGNATURE_INVALID) and lenny-pod-security
   // (POD_SPEC_HOST_SHARING_FORBIDDEN) follow the same convention.
   // spec: §17.2 (Rejection labels)
   ```

The guard's `Code: 403`, every `Reason` string, and the `RejectionCode` and `RejectCode` values stay unchanged.

## Staged schema, chart, and migration changes

None.

## Staged docs changes

### DOCS-B · docs/reference/error-catalog.md

Delete the table row whose first cell is `` `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` ``. Leave the neighbouring rows untouched. No other page under `docs/` names an admission rejection label.

## Testing

CODE-B changes no behavior: it removes a table entry no gateway path reaches and edits comments and one failure-message string. It falls under the refactor escape hatch in `test-coverage.md`, so it adds no test. The existing tests that must stay green, and what each one confirms:

- Tier 0 on `./pkg/gateway/externalapi/errorclassify/...`, `./pkg/admission/...`, and `./tests/tier9_security/...`: the tree builds and lints after the entry and comments change.
- Tier 1, `pkg/admission/ephemeral_container_cred_guard/guard_test.go`: `TestDecideRejectionCarriesTheSpecCode` still finds the label in the denial `Reason`, and the 403 assertion still holds. This is the assertion that discriminates a change to the emitted denial.
- Tier 1, `pkg/gateway/externalapi/errorclassify/errorclassify_test.go`: the remaining classifications are unchanged.
- Tier 3, `tests/tier3_contract`: the error-matrix and REST/MCP consistency tests pass with the entry removed; neither lists the deleted code.
- Tier 11 for SPEC-A, SPEC-C, SPEC-B1, SPEC-B2, and DOCS-B: the anchor and citation gates resolve every link the staged text adds (`#4710-deployment-model`, `#172-namespace-layout`), no inbound link targets the deleted catalog rows, and the line-citation ratchet does not rise.

## Edge cases and accepted failure modes

- An operator who matched `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` in a gateway error envelope finds nothing, because no gateway path ever returned it. Matching on the admission denial message is unaffected.

## Files touched on application (non-spec)

- pkg/gateway/externalapi/errorclassify/errorclassify.go
- pkg/admission/ephemeral_container_cred_guard/guard.go
- pkg/admission/ephemeral_container_cred_guard/guard_test.go
- pkg/admission/registry_digest/guard.go
- tests/tier9_security/admission_ephemeral_test.go
- docs/reference/error-catalog.md
