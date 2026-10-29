# Problem: Session owner is client-assertable on every create surface and session access is not owner-scoped

## Statement

Session-owner authorization (plan §10.2 'Session-owner authorization proposal'). Read BUILD-GAPS F-15.1.55 (High), F-10.2.30 (Medium), and F-15.1.54 (Medium) in full, including their 2026-10-29 notes.

The specification already restricts a `user` caller to its own sessions. The §10.2 Authentication role text says a `user` "Cannot access other users' sessions (even within the same tenant) unless explicitly granted by a tenant-admin". The permission matrix grants `user` only "Read own session history", and §29.3 and the §24 lenny-ctl table (`user` (owner)) repeat the rule. §15.1 defines no client-supplied owner field on any session-create body. §4 (pre-authorized credential flow) states that `tenant_id` and `user_id` are derived from the authenticated JWT and are never supplied by the client. Most of the defect is code that does not conform to existing spec text. The spec gaps are narrower: the undefined "explicit grant", the absence of an explicit rule binding the owner on the session surface, the error a non-owner receives, and the case of a request with no authenticated principal.

The defect has two coupled halves. The coupling runs one way: an owner check compares against the stored owner, so the check is meaningless while the owner is client-chosen.

(A) Owner integrity, F-15.1.55 (High). On every client-facing create surface, the stored owner comes from the request rather than the authenticated principal:

- REST `POST /v1/sessions` and `POST /v1/sessions/start` store the body's `userId` (`create.go`, `create_and_start.go`).
- MCP `create_session` replays the body through `handleCreate`, and its fallback path stores `in.UserID` (`mcptools_register.go`).
- Derive accepts a body `userId` that overrides the source session's owner (`derive.go`).
- The built-in Open Responses and Chat Completions translators store the OpenAI `user` field (`UserID: req.User`).

The principal's `Subject` is read only as the per-user concurrency key, so the concurrency cap and the stored owner already disagree. Two derived paths are not client-asserted. Replay stamps `source.UserID`. A delegated child inherits `parent.UserID`, because the only caller of `delegation.Service.Delegate` (the MCP delegate tool) never sets `delegation.Request.UserID`. The service-level override in `buildChildSession` is therefore unreachable from a client today.

(B) Owner-scoped access, F-10.2.30 with F-15.1.54 as its `GET /v1/responses/{id}` face. Every session route in `sessionserver` is wrapped only in a permission gate: `read` checks `read_own_sessions` and `manage` checks `manage_own_sessions`. No handler compares the principal with `row.UserID`. `handleGet` reads by tenant and id. `handleList` builds a `ListFilter` with no user field. The `read` wrapper also covers the messages, transcript, tree, events, logs, artifacts, workspace, setup-output, webhook-events, and memory subresources. The `manage` wrapper covers create, delete, terminal transitions, messaging, derive, and replay. `OpenResponsesHandler.handleGet` reads by tenant and id with no owner check.

Specify and stage:

- The ownership rule. A session's owner is the authenticated principal that created it, recorded at creation. A client-supplied owner (`userId` on REST, MCP, or derive, and the adapter `user` field) is not used as the owner. State how a derived session gets its owner (the caller, or the source owner), and state the existing inheritance for replay and delegation children.
- The owner rule for a request with no authenticated principal subject. `sessionPermissionGranted` admits a request with no principal (the no-OIDC dev posture) and, in single-tenant mode, a principal with no roles. The rule decides between denying such a request and recording an empty owner. An empty owner also skips the user markers in the §12.8 creation fence (`StoreMarkers` returns early when `userID == ""`).
- One owner-or-bypass rule for `user`-role callers, enforced at a single seam (the route wrapper) rather than decided endpoint by endpoint. It covers session reads and session actions on the §15.1 session surface, the MCP session tools, and `GET /v1/responses/{id}` with `previous_response_id` resolution. The rule names the roles that bypass it: tenant-admin, tenant-viewer (read-only), platform-admin, and any service principal.
- The §10.2 "explicit grant". The minimal options are to define it as the existing tenant-admin role assignment (`tenant-viewer` or a custom role through `PUT /v1/admin/tenants/{id}/users/{userId}/role`), or to change the `user` matrix cell to "No" and drop the clause. Each option adds no new storage. The §12.9 T4 "explicit grant" row in the data-classification table is a separate mechanism, and the proposal states that it is out of scope.
- The error a non-owner receives. The `RESOURCE_NOT_FOUND` catalog row ("does not exist or is not visible to the caller") and the §9.2 `respond_to_elicitation` rule, which returns 404 rather than 403 so the existence of another user's object is not revealed, both point to 404. `handleGet` already returns `404 RESOURCE_NOT_FOUND` for a missing row. §15.1 has no separately named "existence-disclosure convention", so the proposal cites those passages.
- How the adapter `user` field is treated afterwards: ignored, or recorded as untrusted metadata.

Approved proposal 0108 (unimplemented) stages SPEC-2 paragraph B, which scopes `GET /v1/responses/{id}` and `previous_response_id` resolution to the tenant only, and its CODE-4 rewrites the Open Responses `handleCreate` and `handleGet`. 0108 lists the client-asserted `user` owner and the per-owner check as out of scope. This proposal amends 0108's staged tenant-scoping text so that the spec does not end up with two conflicting read rules. It records the amendment in Impacts rows, locates its edits by symbol and quoted text, and lands its code steps after 0108's.

If convergence stalls, split along the A/B line. Owner binding at creation (A) is the High-severity half, needs no grant definition, and lands first. The owner-scoped access check, the grant, and the error choice (B) follow, after A and after 0108.

## Evidence

- (verified) Plan entry: gateway-runtime-comms-remediation.md §10.2 "Session-owner authorization proposal" names F-15.1.55 High, F-10.2.30, and F-15.1.54, and orders the code steps after 0108's.
- (verified) BUILD-GAPS F-10.2.30 (Medium, OPEN), F-15.1.54 (Medium, OPEN), and F-15.1.55 (High, OPEN, filed by proposal 0094), each with a 2026-10-29 note. F-15.1.55's evidence names `create.go`, `create_and_start.go`, MCP `create_session`, and `derive.go` as well as the translators.
- (verified) Create paths: `pkg/gateway/sessionserver/create.go` (`requireCreationAllowed(..., req.UserID)` and `UserID: req.UserID`; `concUser = p.Subject` for concurrency only); `create_and_start.go` (`UserID string json:"userId,omitempty"`, and `UserID: req.UserID` at each row construction); `pkg/gateway/sessionserver/service.go` (MCP `CreateSessionService` replays through `handleCreate`); `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` (fallback `UserID: in.UserID`).
- (verified) Derive: `pkg/gateway/sessionserver/derive.go` documents "UserID overrides the derived session's user" and sets `userID := req.UserID`, falling back to `source.UserID`.
- (verified) Replay: `pkg/gateway/sessionserver/replay.go` stamps `UserID: source.UserID`.
- (verified) Delegation: `pkg/gateway/mcpfabric/delegation/service.go` `buildChildSession` uses `req.UserID` and falls back to `parent.UserID`; the only `delegation.Request{` literal outside tests, in `mcptools_register.go`, sets no `UserID`. Admission rejects a parent with no user and comments that `req.UserID` is not a substitute.
- (verified) Translators: `pkg/gateway/environment/translator/open_responses.go` and `openai_chat.go` both set `UserID: req.User`.
- (verified) Read and act wrappers: `pkg/gateway/sessionserver/route_table.go` (`manage` wraps `PermManageOwnSessions`, `read` wraps `PermReadOwnSessions`); `rbac_gate.go` (`sessionPermissionGranted` admits a request with no principal, and a role-less principal when not multi-tenant); `session_read.go` (`handleGet` calls `s.store.Get(ctx, tenantID, id)` with no owner comparison; `handleList` builds a `ListFilter` with no user field). A search of `sessionserver` for an owner comparison against the principal found none.
- (verified) Open Responses read: `OpenResponsesHandler.handleGet` in `open_responses.go` reads by tenant and id with no owner check.
- (verified) Creation fence: `pkg/gateway/sessionserver/user_gate.go` `requireCreationAllowed` checks the owner it is given; `pkg/gateway/session/sessionstore/marker_reader.go` `StoreMarkers` skips the user markers when `userID == ""`.
- (verified) Spec owner rule: spec/10_gateway-internals.md §10.2 Authentication (`user` role bullet; permission-matrix rows "Read own session history" and "Read other users' sessions (same tenant)" with `user` cell "Only with explicit grant"); spec/29_communication-scenarios.md §29.3; spec/24_lenny-ctl-command-reference.md (`user` (owner) on session reads).
- (verified) "Explicit grant" occurrences: only §10.2 (role bullet and matrix row) and the §12.9 data-classification table (T4 "RBAC role ≥ `admin` + explicit grant"). No section defines either.
- (verified) Identity-from-JWT precedent: spec/04_system-components.md, pre-authorized credential flow: "`tenant_id` and `user_id` are derived from the authenticated JWT — never supplied by the client." §13.3 forbids minting a token for another user except by presenting that user's token; platform-admin impersonation (`POST /v1/admin/impersonation`) is the sanctioned path.
- (verified) 404 precedent: spec/09_mcp-integration.md §9.2 `respond_to_elicitation` authorization; spec/15_external-api-surface.md `RESOURCE_NOT_FOUND` and `ELICITATION_NOT_FOUND` catalog rows.
- (verified) Proposal 0108 status is Approved (`proposal-status.mjs`), unimplemented.
- (unverified) §12.8 Compliance Interfaces "Lifecycle fence and drain": F-15.1.55 cites it because the erasure listers read the stored owner. The ownership rule reaches §12.8.

## Who observes it

- Any authenticated `user`-role principal in a tenant with more than one user, on the REST, MCP, and built-in adapter surfaces. Production multi-user OIDC tenants observe it.
- Any reader of §10.2, §29.3, or the lenny-ctl table, which promise that a `user` cannot access other users' sessions.
- A single-user dev posture with no roles does not observe the access half, because `sessionPermissionGranted` admits role-less and principal-less requests in that posture.

## What breaks if nothing changes

- Confidentiality: a `user`-role caller can list every session in its tenant and read another user's session metadata, TaskRecord, transcript, messages, events, logs, artifacts, workspace, and memory through the `read` routes, and another user's Open Responses record through `GET /v1/responses/{id}`.
- Integrity and availability: through the `manage` routes, the same caller can message, cancel, terminate, delete, derive from, or replay another user's session.
- Attribution: a caller can create a session attributed to another user, and derive can reassign the owner of a derived session.
- Compliance: the §12.8 creation fence checks the client-supplied owner. A suspended user, or a user with erasure in progress, can create sessions by naming another user, or by naming an empty owner, which skips the user markers. Sessions created that way are not reached by that user's erasure.
- The per-user concurrency cap keys on the principal while the stored owner is the body value, so quota accounting and ownership diverge.
- If 0108 lands without amendment, the spec states a tenant-only read rule for `GET /v1/responses/{id}` that contradicts §10.2.

## Findings this unblocks

- F-15.1.55 (High): client-asserted session owner on every create surface.
- F-10.2.30 (Medium): `user`-role session reads are not owner-scoped; the "explicit grant" is undefined.
- F-15.1.54 (Medium): `GET /v1/responses/{id}` has no owner check.

## Prior art considered

- Approved proposal 0108 does not stage this change. Its summary says "The §10.2 per-owner restriction is not staged", lists "Fixing the client-asserted `user` field as session owner" as out of scope, and files both as defects. Its non-spec changes state that the client-asserted `user` field is the session's `UserID` on both paths and is not an authorization boundary. Its SPEC-2 paragraph B scopes the responses read to the tenant only, which this proposal has to amend.
- The three BUILD-GAPS findings are open, have no owner other than this proposal, and say they need the proposal pipeline. No TEST-GAPS entry covers owner checks.
- The spec's existing owner rule (§10.2, §29.3, §24) and the identity-from-JWT principle (§4 pre-authorized credential flow, §13.3 token exchange, and the §4.8 bar on interceptors modifying `user_id`) give the ownership rule a basis to restate on the session surface.
- The §9.2 elicitation triple check, with its 404-rather-than-403 choice, is the precedent for the non-owner error. `SESSION_NOT_FOUND` (404) also exists in the §25 catalog.
- §10.2 already has a tenant-admin mechanism for cross-user reads: role assignment of `tenant-viewer` or a custom role through `PUT /v1/admin/tenants/{id}/users/{userId}/role`.
- Code seams: `concUser = p.Subject` in create and create-and-start already reads the principal. No owner-comparison helper exists in `sessionserver`. `requireSourceOwnerNotRestricted` in `user_gate.go` reads the source owner for derive and replay but does not compare it with the caller.

## Validated premises

- **Premise lens (revise).**
  - Revised: item (1) as first written said the owner is client-assertable only on the built-in OpenAI-dialect adapters. Refuted as too narrow. REST create, create-and-start, MCP `create_session`, and derive also take the owner from the body (verified in code above). Load-bearing; the Statement now names every surface.
  - Confirmed with correction: delegation children and replayed sessions are not client-asserted today. The MCP delegate path sets no `delegation.Request.UserID`, so the child inherits `parent.UserID`; replay copies `source.UserID`. For these paths the spec work is a statement of existing behavior.
  - Confirmed: `GET /v1/sessions` and `GET /v1/sessions/{id}` and every `read` subresource check only `read_own_sessions`; `manage` routes check only `manage_own_sessions`; no handler compares the principal with `row.UserID`. Load-bearing.
  - Confirmed: `OpenResponsesHandler.handleGet` has no owner check.
  - Confirmed: §10.2 promises an undefined "explicit grant"; §12.9 T4 uses the same undefined term; §29.3 and the lenny-ctl table are dependents.
  - Refuted: a named "§15.1 existence-disclosure convention" does not exist. The `RESOURCE_NOT_FOUND` row and §9.2 are the passages that decide the error.
  - Confirmed: the spec defines no client `userId` on session create or derive and does not map the adapter `user` field to the owner; §4 states the identity-from-JWT rule for the pre-authorized credential flow; the concurrency cap keys on `p.Subject` while the row stores `req.UserID`.
  - Added: not every request has an authenticated principal. The no-OIDC dev posture and single-tenant role-less principals are admitted, so the rule must state that case.
  - Confirmed: 0108 is Approved and unimplemented, and the sequencing stands.
- **Evidence lens (revise).**
  - Verified: the plan entry, the three BUILD-GAPS findings with their severities and notes, the translator `UserID: req.User` sites, the F-10.2.30 read path, the §10.2 grant text, the F-15.1.54 read path, and 0108's status.
  - Drift, load-bearing: item (1) understated F-15.1.55, whose evidence and High severity cover REST create, create-and-start, MCP `create_session`, and derive. Corrected in the Statement.
  - Drift, minor: the original spec list omitted §12.8 Compliance Interfaces, which F-15.1.55 cites. Added under Evidence (unverified for the exact §12.8 text).
  - The evidence lens ran without the lookup tool, using grep, sed, and the status tool.
- **Prior-art lens (stands).**
  - Confirmed: no proposal stages this change; 0108 explicitly excludes it and its SPEC-2 paragraph B conflicts with §10.2.
  - Confirmed: the findings are open and unowned; F-15.1.55 is broader than the adapters.
  - Reframing adopted: the read and act half is code not following existing spec text, rather than an absent rule. The missing spec content is the grant, the covered endpoints, the bypass roles, and the error.
  - Confirmed: the identity-from-JWT principle and the §9.2 404 precedent exist and are reusable.
  - Confirmed: owner sources differ per path, the `p.Subject` seam exists, and no owner-comparison helper exists.
- **Scope lens (revise).**
  - Confirmed: the problem has two concerns, owner integrity (A) and owner-scoped access (B), with F-15.1.54 as a face of B. B depends on A and A does not depend on B, so one proposal is defensible and A can land alone.
  - Confirmed in part: item (1) and the title framed the defect as adapter-only; the title and Statement now name every create surface. Refuted in part: the claim that delegation lets `req.UserID` override the parent owner is true of `buildChildSession` but unreachable, because the only caller sets no `UserID`.
  - Adopted: the access check is stated as one owner-or-bypass rule at a single seam rather than an endpoint-by-endpoint survey, which keeps the proposal convergeable.
  - Adopted: the §12.9 T4 "explicit grant" is a separate mechanism and is out of scope.
  - Adopted: keep the proposal whole, with an A/B split as the fallback.
- **Impact lens (revise).**
  - Confirmed, load-bearing: the spec gives `user` callers a confidentiality and integrity guarantee the code does not keep.
  - Confirmed, load-bearing: the read hole reaches session content as well as metadata, through every `read` subresource.
  - Confirmed: the impact includes act paths through the `manage` wrapper, which has no owner comparison; the Statement now names them.
  - Confirmed: the §12.8 creation fence checks the client-supplied owner, and an empty owner skips the user markers (`StoreMarkers` returns early), so a suspended or erasure-pending user can create sessions outside its own erasure.
  - Confirmed: production multi-user tenants observe the defect; the single-user role-less dev posture does not observe the access half.
- **Alternatives lens (revise).**
  - Confirmed, load-bearing: the spec already states the owner-scoping rule; the code does not conform to it.
  - Confirmed, load-bearing: a smaller spec change makes the grant redundant, by defining it as the existing role assignment or by dropping the clause.
  - Confirmed: the client-asserted owner has no spec basis; §13.3 and impersonation are the only sanctioned way to act as another user, so binding the owner to the principal with no exception closes the hole. Whether a service principal may name an end-user owner remains a narrow open question.
  - Confirmed: item (1) was narrower than F-15.1.55.
  - Adopted: this proposal amends 0108's staged tenant-scoping text rather than coordinating only through Impacts rows.
  - Confirmed: the 404 choice follows existing precedent, and `handleGet` already returns `404 RESOURCE_NOT_FOUND` for a missing row.
  - Not adopted as settled: the alternatives lens proposed that derived and replayed sessions take the caller as owner. Today replay inherits the source owner and derive falls back to it. The Statement leaves the derive owner as a decision and states the existing replay and delegation inheritance.
