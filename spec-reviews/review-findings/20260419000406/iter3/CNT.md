# Iter3 CNT Review

**Date:** 2026-04-19 | **Scope:** Workspace Content / Container. Regression-check of iter2 commit `2a46fb6` (CNT-002 schema scope, CNT-003 `gitClone`, CNT-004 capability-matrix preamble). Prior iter IDs CNT-001 through CNT-004 resolved.

## Regression-check of iter2 fixes

- **CNT-002 (WorkspacePlan schema scope).** New §14.1 "Envelope terminology" cleanly separates the inner `WorkspacePlan` from the outer `CreateSessionRequest`, and the "Published JSON Schema" paragraph now explicitly enumerates the inner-only fields and the outer-only fields. The §14 canonical example in lines 7–81 is unchanged but is now consistent with the explicit envelope split. Error-code scope is disambiguated: `WORKSPACE_PLAN_INVALID` is reserved for inner-plan failures; outer-envelope violations use field-specific codes. **Cleanly resolved.**
- **CNT-003 (`gitClone` catalogue entry).** Section 14's `sources[]` catalogue (lines 85–91) now includes a `gitClone` row with `url`/`ref` required and `path`/`depth`/`submodules`/`auth` optional, plus a dedicated `gitClone.auth` paragraph on line 93. References in §26.2 (lines 43, 111, 202) still use the same source-type name. **Resolved, but see CNT-005/CNT-006 below for follow-on issues introduced by the new text.**
- **CNT-004 (Translation Fidelity Matrix preamble).** Line 1298 now reads: "The following matrix documents field-level fidelity for each built-in adapter, plus the REST surface, plus the Post-V1 A2A adapter for forward planning." A2A is clearly marked Post-V1 in the preamble. **Cleanly resolved.**

## New findings

### CNT-005 `gitClone` Auth Scope Hardcoded to GitHub While URL Accepts Any Git Host [REAL ERROR]

**Files:** `14_workspace-plan-schema.md:91,93`, `26_reference-runtime-catalog.md:111,202`, `04_system-components.md:§4.9 credential pool table`

**Description:** The `gitClone.url` field is specified in §14 as "HTTPS or SSH Git URL" with no host restriction — a client can legitimately declare `https://gitlab.com/...`, `git@bitbucket.org:...`, or a self-hosted Gitea URL. However, §14's `gitClone.auth` paragraph (line 93) constrains `auth.leaseScope` to be "one of the §26.2 credential-lease scopes — `vcs.github.read` for read-only clones ... or `vcs.github.write` when the session will push back". Those are the **only** two values enumerated, and §26.2 (lines 111, 202) and the credential-leasing service catalog in §4.9 (line 1067, "`github`: GitHub App credentials") confirm that GitHub is the only Git-hosting credential pool shipped in v1.

This is an unresolved mismatch between the URL surface (any Git host) and the auth surface (GitHub only). Three concrete problems:

1. **Private-repo clones against GitLab/Bitbucket/Gitea cannot be expressed.** A client needing to clone `https://gitlab.example.com/team/private-repo.git` has no valid `auth.leaseScope` value — the spec neither enumerates a `vcs.gitlab.read` scope nor says "public repos only for non-GitHub hosts".
2. **The gateway's `git` invocation of an SSH URL (`git@github.com:...`) is not specified.** The `gitClone.auth` paragraph says "`git` client inside the pod later uses an in-pod credential helper" but the HTTPS-based credential-helper flow does not cover SSH key material. Whether SSH URLs require a separate key-mounting path, are transparently rewritten to HTTPS by the gateway, or are simply unsupported in v1 is not stated.
3. **`auth.leaseScope` is documented as "one of the §26.2 credential-lease scopes"** but §26.2 also enumerates `llm.provider.*.inference` and derivatives. The enumeration needs to be tightened to "one of the VCS credential-lease scopes".

**Recommendation:** Pick one of:
- **(a)** Constrain `gitClone.url` to HTTPS-only GitHub URLs in v1 and document non-GitHub hosts and SSH as Post-V1 ([§21](21_planned-post-v1.md)). Update §14 line 91 to "HTTPS GitHub URL".
- **(b)** Keep the open-host URL but add an explicit `publicOnly` boolean that, when `true`, skips credential-helper injection; reject `auth` for non-GitHub hosts in v1 with a stated error code; document SSH URL handling (supported vs. rejected vs. rewritten).
- **(c)** Expand the scope enumeration to a future-safe `vcs.<host>.{read,write}` pattern and explicitly list which hosts ship in v1 (GitHub only) vs. which need operator configuration, with a rejection error for unsupported hosts.

Option (a) is simplest for v1 given that §4.9 only ships a `github` credential pool.

---

### CNT-006 `uploadArchive` Format List Mismatch Between §14 and §7.4 [REAL ERROR]

**Files:** `14_workspace-plan-schema.md:89`, `07_session-lifecycle.md:408`, `16_observability.md:20`

**Description:** Section 14 declares the `uploadArchive` source type's `format` field to accept **`tar`, `tar.gz`, or `zip`** (line 89, cell 3 of the sources-catalogue table). Section 7.4's Upload Safety rules, which govern the very same archive extraction, state: **"Supported formats: `tar.gz`, `tar.bz2`, `zip`. Other formats are rejected."** (line 408).

The two lists differ on **both ends**:

- §14 lists `tar` (uncompressed); §7.4 rejects it.
- §7.4 lists `tar.bz2`; §14 would reject it because it's not in the `format` enum.

Additionally, §16's upload-extraction metric labels (`error_type: zip_bomb, size_limit, path_traversal, symlink, format_error`) do not clarify which set wins. This is a direct contradiction between the WorkspacePlan schema (what the gateway validates at session creation) and the upload-safety extractor contract (what the gateway enforces during materialization). A client writing a plan with `"format": "tar.bz2"` would be rejected at §14 validation; a client writing `"format": "tar"` would pass §14 validation but fail §7.4 extraction.

**Impact:** §14 is the authoritative JSON Schema for the WorkspacePlan (CNT-002 resolution), so the published `workspaceplan/v1.json` schema will enumerate `tar`, `tar.gz`, `zip`. Clients that validate locally will submit `tar` uploads that then fail at extraction with no documented error code, and `tar.bz2` uploads will be rejected at validation despite §7.4 declaring it supported.

**Recommendation:** Pick one canonical list and apply it in both sections:
- If `tar` (uncompressed) is supported, add it to §7.4 line 408.
- If `tar.bz2` is supported, add it to §14 line 89's `format` enum.
- Recommended v1 minimum: `tar.gz` and `zip` only. Drop `tar` (uncompressed has no compelling use case for workspace uploads) and `tar.bz2` (seldom used; extraction requires bz2 libraries). Update both sections to list only `tar.gz` and `zip`, and note in §21 that additional archive formats are post-v1.

---

### CNT-007 `gitClone.auth` Paragraph Does Not Bind Credential-Pool Identity to the Session [MINOR — CLARITY]

**Files:** `14_workspace-plan-schema.md:93`

**Description:** The `gitClone.auth` paragraph says "The gateway resolves the scope against the tenant's configured Git-hosting credential pool" but does not specify which credential pool when the tenant has more than one GitHub App installation configured (e.g., one for `org-a/*` and another for `org-b/*`). §4.9's credential-leasing service supports multiple pools per scope; §26.2 refers generically to "the tenant's configured GitHub credential pool". If the client's `gitClone.url` is `https://github.com/org-a/repo.git` but the pool-selection logic does not look at the URL's org path, the lease request is ambiguous. This is a specification gap rather than a contradiction, but it affects multi-org GitHub App deployments.

**Recommendation:** Add one sentence to §14's `gitClone.auth` paragraph: either "pool selection uses the URL's `{host}/{owner}` path to route to the correct GitHub App installation; clients whose tenant has multiple GitHub App installations MAY pass an explicit `auth.credentialPoolRef` to disambiguate", or state that v1 supports exactly one GitHub App installation per tenant and multi-installation routing is post-v1.

---

## Summary

iter2 fixes for CNT-002, CNT-003, CNT-004 are cleanly applied; no regressions introduced by the envelope split, and the capability-matrix preamble now clearly labels A2A as Post-V1. Three new findings surface around the newly-introduced `gitClone` catalogue entry: **CNT-005** is a concrete contract mismatch between `gitClone.url` (any host) and `gitClone.auth.leaseScope` (GitHub-only); **CNT-006** is a pre-existing but now more visible contradiction between §14's `uploadArchive.format` enum and §7.4's supported-formats list; **CNT-007** is a minor clarity gap about credential-pool selection when a tenant has multiple GitHub App installations. CNT-006 is the highest-severity carryover because it causes valid-to-one-section plans to fail extraction in the other.

No PARTIAL/SKIPPED items. Container image contract (§5.1 digest pinning, cosign fail-closed, image supply chain, trusted registries) is complete and consistent. OutputPart schema coverage, MIME-type handling, blob resolution path (`GET /v1/blobs/{ref}`), and workspace snapshot download (`GET /v1/sessions/{id}/workspace`) remain internally consistent.
