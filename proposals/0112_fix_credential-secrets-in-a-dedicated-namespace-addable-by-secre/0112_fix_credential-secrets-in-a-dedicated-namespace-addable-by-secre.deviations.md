# Deviations — 0112_fix_credential-secrets-in-a-dedicated-namespace-addable-by-secre

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: ProbeSecretAccess records no outcome for an InvalidArgument rejection

**Status:** proposed

**Reported by:** step S14 (CODE-2: keyed Get-only ProbeSecretAccess, metric, redaction, and Token Service rate flags).

**What the proposal says.** Testing item 2 in the non-spec changes (Testing) states that each call increments exactly one outcome.

**What landed instead.** `ProbeSecretAccess` in `pkg/tokenservice/grpc.go` records exactly one outcome for every call that reaches a read or finds no prober, and a call that finds no prober counts as `unavailable`. A request rejected with `InvalidArgument` (a nil request or an empty `secret_name`) records no outcome. `TestProbeSecretAccess_EmptySecretName` in `pkg/tokenservice/probe_secret_access_test.go` asserts this behavior.

**Why.** No read happens on a malformed request, and none of the four §16.1 outcomes (`success`, `not_found`, `denied`, and `unavailable`) describes an `InvalidArgument` rejection. Counting the rejection as `unavailable` would raise the counter for a caller bug rather than for a failed read.

**What a later reader would otherwise get wrong.** A reader of Testing item 2 would expect the sum of the outcome counter to equal the number of `ProbeSecretAccess` calls. The sum equals the number of calls that passed request validation, and malformed requests are absent from the metric.

## Proposed: Redaction interceptor lives in pkg/tokenservice and logs an `error` outcome

**Status:** proposed

**Reported by:** step S14 (CODE-2: keyed Get-only ProbeSecretAccess, metric, redaction, and Token Service rate flags).

**What the proposal says.** CODE-2 step 6 installs the redaction interceptor in `cmd/lenny-token-service/surfaces.go` beside `spiffeAuditInterceptor`, following the `pkg/adapter/credredact.go` pattern.

**What landed instead.** The interceptor is the exported `tokenservice.CredentialRedactionInterceptor` in `pkg/tokenservice/credredact.go`, together with the `IsCredentialSensitiveCall` classifier. `surfaces.go` chains it after `spiffeAuditInterceptor` with `grpc.ChainUnaryInterceptor`. Its log line and span record `rpc`, `secret_name`, and `outcome`. A non-`Unavailable` error is recorded with the outcome `error`.

**Why.** The tier 9 test in `tests/tier9_security` must import the production interceptor, and a test cannot import package `main`. The proposal names only the install site, so placing the interceptor in `pkg/tokenservice` keeps a single implementation. The `error` value covers `InvalidArgument` and appears only in the log and the span. The metric never carries it.

**What a later reader would otherwise get wrong.** A reader following CODE-2 step 6 would look for the interceptor's definition in `cmd/lenny-token-service/surfaces.go` and find only the install call. A reader comparing the log or span `outcome` attribute with the §16.1 metric outcomes would find the extra value `error`, which exists in the log and span only.

## Proposed: VCS adapter contract and INTERNAL_ERROR mapping are tested outside tier 3

**Status:** proposed

**Reported by:** step S18 (CODE-6: Token-Service-backed VCS reader).

**What the proposal says.** The TEST-1 tier 3 bullet places a contract test on `ProbeSecretAccess` between the gateway prober and VCS adapter and the Token Service. The testing item for the VCS adapter states that an `Unavailable` read returns the existing `INTERNAL_ERROR` tool error.

**What landed instead.** The VCS adapter is a method on `tokenServiceSecretProber` in package `main`, in `cmd/lenny-gateway/secret_probe.go`. The adapter-to-Token-Service wire check is `cmd/lenny-gateway/secret_probe_vcs_wire_test.go`, which runs the production Token Service gRPC server over bufconn. The new tier 3 test, `tests/tier3_contract/vcs_credential_read`, covers the plan-binding HTTP contract (status, code, category, retryable, and details keys) with the production `vcscred.StoreResolver`. For `INTERNAL_ERROR`, the adapter tests assert that an `Unavailable` error neither wraps `ErrNoUsableCredential` nor is a `VCSResolveError`, which is the branch `mcptools.vcsTokenError` maps to `INTERNAL_ERROR`. The tests do not call the unexported `vcsTokenError`.

**Why.** Package `main` cannot be imported from `tests/`, and the proposal leaves the adapter in `cmd/lenny-gateway`. The checklist assigns the full cross-tier tier 3 and tier 9 suite to the later TEST-1 step.

**What a later reader would otherwise get wrong.** A reader of the TEST-1 tier 3 bullet would look under `tests/tier3_contract` for the adapter-to-Token-Service contract test, and that test is in `cmd/lenny-gateway`. A reader of the VCS adapter testing item would expect a test that observes the `INTERNAL_ERROR` tool error. The adapter tests assert only the error classification that selects the `vcsTokenError` branch.

## Proposed: VCS reader wiring moved into buildVCSResolver and newTokenServiceVCSReader

**Status:** proposed

**Reported by:** step S18 (CODE-6: Token-Service-backed VCS reader).

**What the proposal says.** The CODE-6 Targets and step 2 state that `auxstores.go` builds `vcsCreds` inline from `w.tokenServiceConn` and `w.tokenServiceSubsystem`.

**What landed instead.** `auxstores.go` calls a new helper, `buildVCSResolver(conn *grpc.ClientConn, pools, credNS, subsystem)`, defined in `cmd/lenny-gateway/auxstores.go`. The helper returns nil when `conn` is nil. The adapter is built with a new constructor, `newTokenServiceVCSReader`, in `cmd/lenny-gateway/secret_probe.go`. The `tokenServiceSecretProber` struct gains a `tokenService *subsystem.Subsystem` field, and the admin probe path does not use that field.

**Why.** The helper lets a unit test check the wiring without building the whole `buildAuxStores` accumulator. The step reports that the behavior matches the inline wiring the proposal describes.

**What a later reader would otherwise get wrong.** A reader following CODE-6 step 2 would look for an inline construction of `vcsCreds` in `auxstores.go` and find a call to `buildVCSResolver`. A reader of the prober struct would assume the admin probe path uses the `tokenService` field, and only the VCS reader constructed by `newTokenServiceVCSReader` uses it.

## Proposed: The tier 3 cross-tier contract is split across several tests, and the prober half sits at tier 1

**Status:** proposed

**Reported by:** step S22 (TEST-1: cross-tier tests).

**What the proposal says.** The TEST-1 tier 3 bullet places one contract test on `ProbeSecretAccess` under `tests/tier3_contract`, between the gateway prober and VCS adapter and the Token Service over a fake clientset.

**What landed instead.** No new test sits under `tests/tier3_contract` for this item. The bullet's assertions are satisfied by these files: `tests/tier3_contract/tokenservice_secret_read` (no namespace field, a keyed `ALLOWED` carries the value, other verdicts carry none, and a failed read is `Unavailable`); `cmd/lenny-gateway/secret_probe_vcs_wire_test.go` (the VCS adapter over the production server); `cmd/lenny-gateway/secret_probe_admin_wire_test.go` (the production prober over the production server into `admin.NewRouter`, giving `503 CREDENTIAL_PROBE_UNAVAILABLE`); `tests/tier3_contract/admin_credential_pool_codes` (the `503` on the wire); and `tests/tier3_contract/vcs_credential_read` (the plan-binding codes).

**Why.** `tokenServiceSecretProber` is in package `main`, which `tests/` cannot import. The two wire tests carry no contract build tag, so they run in tier 1.

**What a later reader would otherwise get wrong.** A reader of the TEST-1 tier 3 bullet would look under `tests/tier3_contract` for the prober half and find it in `cmd/lenny-gateway`.

## Proposed: The tier 5 authenticated gitClone stops at the Token Service read

**Status:** proposed

**Reported by:** step S22 (TEST-1: cross-tier tests).

**What the proposal says.** The TEST-1 tier 5 bullet states that a `secretData` add followed by an authenticated `gitClone` succeeds.

**What landed instead.** `TestSecretDataCredentialReadThroughTokenServiceAtGitCloneBinding_spec_4_9` in `tests/tier5_e2e_kind/credentials_namespace_test.go` asserts that the `secretData` add writes the Secret, that the plan binding reads it through the Token Service with a `credential_rpc` success line, and that the create does not end in `GIT_CLONE_REF_UNRESOLVABLE`. Its `authenticated clone` subtest skips with a `blocked:` reason. `TestSecretDataValueAbsentFromGatewayAndTokenServiceLogs_spec_16_4` uses the same keyed read.

**Why.** The gateway runs `git ls-remote` and `git fetch` in process, and the gateway image is `gcr.io/distroless/static:nonroot`, which has no `git` binary. Every plan binding of a `gitClone` source on the deployed image therefore ends in `503 GIT_CLONE_REF_RESOLVE_TRANSIENT` with reason `network_error`. The kind install also has no HTTPS Git server the gateway trusts, and `allow-gateway-egress` excepts private ranges. Changing the image base is outside this proposal.

**What a later reader would otherwise get wrong.** A reader of the TEST-1 tier 5 bullet would take an authenticated clone on kind as verified. It is not, and the missing `git` binary affects every `gitClone` on a deployed gateway, public repositories included.

## Proposed: The tier 5 authenticated gitClone subtest skips on F-14.1.37

**Status:** proposed

**Reported by:** step S22 (TEST-1: cross-tier tests). This report covers the same bullet as the earlier entry "The tier 5 authenticated gitClone stops at the Token Service read" and adds the expected error code and the finding that now tracks the defect.

**What the proposal says.** The TEST-1 tier 5 bullet in the non-spec changes states that a `secretData` add followed by an authenticated `gitClone` succeeds.

**What landed instead.** `TestSecretDataCredentialReadThroughTokenServiceAtGitCloneBinding_spec_4_9` in `tests/tier5_e2e_kind/credentials_namespace_test.go` stops at the Token Service keyed read and expects `GIT_CLONE_REF_RESOLVE_TRANSIENT`. Its `authenticated clone` subtest skips with the reason `blocked: F-14.1.37`.

**Why.** The gateway image is distroless and has no `git` binary, so every `gitClone` `ls-remote` and `fetch` fails with `network_error`. The kind install has no trusted HTTPS Git fixture. Changing the image is outside this proposal, and the finding text left the bullet for a human to decide. `BUILD-GAPS.md` tracks the defect as F-14.1.37.

**What a later reader would otherwise get wrong.** A reader of the TEST-1 tier 5 bullet would take an authenticated clone on kind as verified. The test verifies the Token Service keyed read only, and the clone itself stays unverified until F-14.1.37 closes.

## Proposed: The new finding is F-4.9.45, and this step filed no unstaged-defect entries

**Status:** proposed

**Reported by:** step S24 (REC-1: BUILD-GAPS records).

**What the proposal says.** REC-1 in the non-spec changes files the new finding under the next free F-4.9.N id, F-4.9.42 at drafting, and files each entry of the summary's unstaged-defects list as an open finding.

**What landed instead.** The finding is F-4.9.45 in `BUILD-GAPS.md`, placed after F-4.9.44. Step S24 filed no unstaged-defect entries.

**Why.** F-4.9.42, F-4.9.43, and F-4.9.44 were already taken, and F-4.9.45 was absent from every local and remote ref checked at landing. The step task states that the unstaged-defect entries were already filed in merge `b3a35f1f0`, so filing them again would duplicate them. The step did not check each of those entries individually.

**What a later reader would otherwise get wrong.** A reader of REC-1 would look for the new finding under F-4.9.42 and find a different finding. A reader would also take step S24 as the step that filed the unstaged-defect entries. The record that those entries were filed in merge `b3a35f1f0` comes from the step task, and step S24 did not confirm it entry by entry.

## Proposed: Commit f6fe7c6f8 keeps proposal change ids in its message

**Status:** proposed

**Reported by:** the design-conformance review.

**What the proposal says.** The non-spec changes and the spec changes define `SPEC-1`, `SPEC-2`, and `SPEC-3` as staging labels of the proposal document.

**What landed instead.** The subject and body of commit `f6fe7c6f8` carry those labels where the sibling spec commits name the section and the behavior.

**Why.** Committed history is not rewritten, so the message stands. No such label appears in the code, comments, test names, `BUILD-GAPS.md`, docs, or spec text.

**What a later reader would otherwise get wrong.** A reader of first-parent history would look for `SPEC-1` in the specification. The commit lands the §4.9 credentials namespace, `secretRef` rules, `secretData` path, and per-read Token Service resolution.

## Proposed: A gateway with a Token Service and no cluster client holds a failing writer

**Status:** proposed

**Reported by:** the design-conformance review.

**What the proposal says.** The design wires the writer and the read adapter only when the Token Service address is configured, and it does not state what happens when that gateway has no cluster client.

**What landed instead.** The address alone gates the writer. When no cluster config or clientset can be built, the gateway logs the cause, keeps serving, and holds a writer whose every operation fails. An admin write that needs a Secret operation answers `503 SERVICE_UNAVAILABLE` under rule 5. Tenant deletion Phase 4 fails and retries, including for a tenant with no Lenny-written reference, because the writer is non-nil and the delete-collection runs on every Phase 4. The cluster config is resolved once at startup, so recovery requires a restart.

**Why.** A startup failure adds a precondition the design does not state, and a nil writer would report `TOKEN_SERVICE_NOT_CONFIGURED` for a configured Token Service, which rule 6 forbids.

**What a later reader would otherwise get wrong.** A reader of rule 6 would expect tenant deletion on such a gateway to proceed for a tenant with no Lenny-written reference. That branch applies only to a gateway with no Token Service.

## Proposed: The Token Service reads LENNY_CREDENTIALS_NAMESPACE

**Status:** proposed

**Reported by:** the design-conformance review.

**What the proposal says.** The proposal names the `--secret-namespace` flag and the Helm value `credentials.namespace`, and it does not name an environment override for the Token Service.

**What landed instead.** `--secret-namespace` defaults to `LENNY_CREDENTIALS_NAMESPACE` and then to `credential.DefaultNamespace`, and the `POD_NAMESPACE` fallback is gone.

**Why.** The gateway's `--credentials-namespace` flag has the same override. Without it, an operator who sets the variable would move the gateway alone and the two binaries would target different namespaces.

**What a later reader would otherwise get wrong.** A reader of the flag declaration would take the environment override for a proposal requirement. It exists only to keep the two binaries on one namespace.
