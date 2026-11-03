# User credential delivery

Status: design for owner review, revision 8, 2026-11-03, after six rounds of independent review (see the "Review of revision" sections). It reads against `egress-and-git-design.md` revision 51 and `user-git-identity-design.md` revision 11. The owner has settled decisions 4 and 24 and the git path, in the words "go with T and your recommendations". Nothing in this document is built or staged. Spikes ran the request rules and the git candidates against a stub running stock git. **The stub proves forwarding only:** a row that reached it says nothing about whether a host would accept the spelling, grant the permission, or perform the action. No write, permission check, or quick action has run against a GitHub or GitLab host. Rules this revision changes after the last run are **[design]**. Statements about the tree were checked by reading the code and the specification on this date. Proposals carry the label UC-n. This revision followed independent security and consistency reviews whose final stop check found no Major or higher issue. That check traced the last round's changes and was not an end-to-end read, and no production component or live provider write has run.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[measured]** | A hands-on result from a validation round of `egress-and-git-design.md`, or from the spike of 2026-11-03 recorded in `user-git-identity-design.md`, whose origin was a stub. |
| **[captured]** | Seen at the stub or at the prototype `netagent` in the capture recorded in `user-git-identity-design.md`. It shows nothing about what a provider would do. |
| **[pending]** | Waits on a measurement or decision named beside it. |
| **[live]** | Read from a provider's host, anonymously, with no write. |
| **[documented]** | Stated on a vendor page cited in `user-git-identity-design.md`, read and not tested. |
| **[source]** | Read in the Lenny tree or the specification. |
| **[design]** | What this design requires. Nothing implements it. |

## Problem

The owner's direction: "a generic mechanism for tenants to deliver user tokens to a pod. Not tied to GH or anything else. This would require opting in, and it wouldn't have the same security guarantees as token injection, but the mechanism needs to exist because we can't possibly handle all injection scenarios out there." And: "Ideally, I would want user token injection to also be generic and configurable."

Lenny has no such mechanism today.

- [§4.9 Pre-Authorized Credential Flow](../spec/04_system-components.md#pre-authorized-credential-flow) stores a user's own credential for a closed set of providers and delivers it only to the LLM reverse proxy.
- The credential bindings of `egress-and-git-design.md` are tenant credentials. A binding has no notion of whose token it holds.
- `user-git-identity-design.md` revision 3 had a GitHub and GitLab filter in `netagent`. The owner's conditions for keeping a GitHub-specific injection were that it survive `gh` updates and work for all `gh` commands. Both were measured false for the allowlist, and the owner chose to replace it (see "History of this decision").

Some cases cannot use injection at all. A tool signs requests with the token, pins certificates, or speaks a protocol `netagent` does not terminate, and an arbitrary SaaS tool needs its token as a file. Those cases have a known cost, and this design opts into that cost openly.

## Scope

This design covers tenant-defined user credential types, a `registered` source a tenant backend writes to, an `oauth2Link` source for any OAuth2 provider, a `userCredential` binding delivered by `inject` or `file`, request rules held as data, and presets.

Out of scope: a tenant-run token endpoint, which is reserved as a later value of the source field. `user-git-identity-design.md` owns repository authorization, pull request authoring as a configuration of this mechanism, and the link flow. `egress-and-git-design.md` owns `inject`, `file`, I8, and the git path. `security-oversight-design.md` owns guardrails, `tenant-scoping-identity-design.md` owns Environments and user lifecycle, `forensics-evidence-preservation-design.md` owns evidence handling, and `tenant-outbound-connector-design.md` owns private hosts.

## Terms

| Term | Meaning |
| :-- | :-- |
| User credential type | A tenant object naming a kind of per-user token, its source, and the hosts where it works. |
| Source | Where a type's tokens come from: `registered` or `oauth2Link`. The value `endpoint` is reserved. |
| Type hosts | For `registered`, the hosts the type declares. For `oauth2Link`, the provider's `apiHosts`. Every host rule here evaluates against these. |
| Token trust record | The one record of everything that decides where a stored token can go and who can read it. See "Token trust record". |
| `userCredential` binding | A credential binding whose `secret` names a type, so each session receives its own user's token. |
| Request rules | Data on an `inject` binding that refuses requests before the token is attached. |
| Preset | A named, versioned set of request rules and request forms, shipped as data, with paths per API flavour. |
| Narrowing template | Data on a link provider describing how to derive a per-session token from the stored one. |

The API field is `linkProviders` and the table is `link_providers`, and both name the same object. An API flavour is `github`, `github-enterprise`, or `gitlab`, and it selects a preset's paths and a template's API base. No new link, channel, or register enters §28. The Token Service calls sit on `schemas/lenny-tokenservice.proto`, which [§28.7 Wire-contract artifact register](../spec/28_communication-channels.md#287-wire-contract-artifact-register) records as a surface the channel register holds no entry for, and the I8 fields are additions to an interface the egress design defines. Identifiers here use `userCredential` and `user_credential`, which share no stem with a channel identifier.

## Requirements

Alice is a developer at `acme`. Carol is `acme`'s tenant admin, Dave is its `security-admin`, and Bob is another developer. Erin is a developer at `initech`, and `globex` is another tenant.

- UCR-1. A tenant can deliver a per-user token for any service to that user's sessions, with no Lenny code for that service.
- UCR-2. A tenant's backend can write a token for a named user. A user can write her own when the type permits it.
- UCR-3. A tenant can link users to any OAuth2 provider that identifies its user, and the refresh token stays in the Token Service and goes only where its token trust record says.
- UCR-4. `delivery: inject` is generic for any host the type declares and any `attach` template, and needs no agent-readable gate.
- UCR-5. `delivery: file` exists behind a platform grant and a tenant opt-in per binding, with its limits stated plainly.
- UCR-6. Request rules are data. A provider's change or a client release needs no `netagent` build.
- UCR-7. A session receives only its own user's token, on a pod that cannot serve anyone else.
- UCR-8. A tenant can define a type on any host, GitHub included.
- UCR-9. Every delivery is audited.

## Threat model

### Security properties

- UCP-1. A session receives only the token of the user on its session row, and only when that row's `caller_type` is `human`. A delegated child's row copies its root's value, and an empty value is not `human`. No client or pod input names another user. The Token Service holds this from its own read through `pkg/binding`.
- UCP-2. A `userCredential` binding is refused on a pod that can serve another session or a later session, or that has served one. The binding validator and the gateway hold this at create. I8 releases nothing unless the claim carries `retireOnRelease`, and every release path honours that mark whatever the pool now says.
- UCP-3. An impersonation bearer cannot give session input to a session in a delegation tree where any session holds a `userCredential` binding or declares a repository under a `required` pool, cannot write to or delete from any user's memory, and cannot write a user's credential. The check sits at the session input layer, so every adapter inherits it.
- UCP-4. `delivery: file` needs the `userCredentialDelivery` grant, a tenant opt-in on the binding, and a delivery ceiling of `file` in the token's trust record. The write path, the Token Service, and `netagent` hold this.
- UCP-5. A refresh token never leaves the Token Service. A stored token of either source goes only to the names and route targets in its token trust record. **On any change to that record the stored token is unusable and is sent nowhere until a new user act or a revert to the identical record.** Below a ceiling of `file`, this protects stored tokens against reading through configuration. It does not protect a future grant, a name the customer operates, or a token under a ceiling of `file`, which a binding flip and a setup command can read.
- UCP-6. Under `inject`, no process in the runtime or adapter container can read the token, and `requestRules` apply. `netagent` attaches it only to the type's hosts. **Under `file`, none of this is true.** Under `inject`, an agent can still ask a host to mint a new credential through an operation the token permits and the preset does not name, and it then holds what the host returns.
- UCP-7. One tenant's type, token, preset choice, or OAuth2 client never reaches another tenant. A client secret is a write-only value stored under the tenant, never a reference a tenant can point elsewhere, and no client is shared across tenants. The Token Service re-checks the tenant at read time.
- UCP-8. A delegated child receives a `userCredential` binding only when the delegation lease names it. A pool's attach does not bypass this.
- UCP-9. Every release of a token on I8 produces a `credential.mint` record, and the first delivery to a session produces an audit event.
- UCP-10. When Lenny stops a credential (revocation, expiry, user disable, quarantine, or session end), I8 releases no more, and `netagent` removes the file or flushes the token within the bounds under "Stopping a credential". **Lenny makes no claim about copies of a token the agent was able to read, or about a credential the agent had a host mint.**

### Threats

| Attacker | Can | Cannot | Why |
| :-- | :-- | :-- | :-- |
| Prompt-injected agent, `file` delivery | Read the token. Send it to any host the effective policy allows. Use it for everything it permits, from anywhere, until the provider expires or revokes it. Copy it into the workspace, a transcript, an LLM request, or an export. | Read another user's token, or obtain a refresh token. | UCP-1 and UCP-5. "What `file` does not promise" is the full statement. |
| Prompt-injected agent, `inject` delivery | Do what the token permits through the type's hosts, except what `requestRules` refuse. A new provider operation the preset does not yet name passes. Ask a host to mint a credential through an operation the token permits and the preset does not name, and hold what comes back. | Read the token, except through a reflection the scrub misses, or send it to another host. | UCP-6, and the egress design's `inject` rules. |
| Compromised `netagent` | Read the token under either delivery, ignore `requestRules`, and use the token for its life. | Obtain another session's token, or a refresh token. | I8 answers for the pod's own binding. |
| Tenant admin or backend holding `manage_user_credentials` | **Plant:** write a token under Alice's name, so that Alice's agent acts with an account Alice does not own, or exfiltrates to an account the writer controls. Settled under decision 4. | Read back a token already stored. Write across tenants. | Writes are one-way and audited, and Alice can list and delete them. `registeredBy` limits who writes, and she can edit that type field too. |
| Tenant admin controlling configuration | **Obtain a future token.** She edits a record, and Alice's next session asks Alice to relink. If Alice proceeds past the page, Alice grants a new token to a host Carol runs. She can also re-point an automated backend, whose next write sends a value to a host she added. Make every stored token unusable by editing a record. **Read tokens on a name she also operates,** through its DNS, CA, or network, which the record cannot see. | **Read a token stored before an edit, below a ceiling of `file`,** under either source, through an endpoint, host, route, CA, `attach`, template, client, `kind`, `source`, or delivery change, or by deleting and recreating a type. | UCP-5. The future grant has no per-field fix, because she configures the tenant. The page and the sealed client secret reduce it, and the enforcing control is `tokenTrust.allowedRecords`. Decision 4 records the owner's settlement. |
| Compromised gateway replica | See a `registered` value and a `clientSecret` in transit on a write, and plant a token in any tenant through the pass-through route, supplying `written_by` itself, so a plant can name Alice and her `GET` shows her as the writer. Forge session rows and bindings, which `pkg/binding` reads, so it can use any stored token at its record's hosts through a pod it drives, and read any value whose ceiling is `file`, including through `leakScan`. **Resurrect a row** after `DELETE`, `revoke`, or erasure from a copied ciphertext, because the per-tenant key stays and no counter is sealed, and restore an earlier envelope. Rewrite the mutable fields: `state`, including `revoking` back to `active`, and exposure acknowledgements. Write provider rows directly, so `tokenTrust.allowedRecords`, which sits on EG-9's write path, does not hold against it for a future grant. Read LLM credentials in today's `credentials` table, which is a defect recorded under "Current state". | Point a stored token at another destination, move a ciphertext to another row, role, or Environment, or move a cached value to another key, because identity, role, and digest are sealed as additional data, so an edit fails to decrypt. Rewrite a sealed field. Decrypt a stored row itself, or obtain a refresh token. | UC-1 **Storage**. The key separation holds on a cloud KMS, and the local provider mounts one master key in the gateway, the Token Service, and `lenny-ops`. |
| Tenant admin able to change a runtime or `workspaceDefaults` | Run a setup command in Alice's session that reads a `file` token, since the runtime container can read it. Under `inject`, have a setup command use the token, including a host's mint operation, as UCP-6 states for an agent. | Read an `inject` token directly that way. | This is inherent in `file`. `lockEgressToHosts` narrows where the value can go. |
| A user registering a token that is not hers | Have her agent act under another account. | Be stopped by Lenny on `registered`, which cannot tell whose token it holds. | Residual stated. `oauth2Link` with an identity binding addresses it. |
| Tenant registering a hostile OAuth2 endpoint | Answer token calls as it likes, for its own users. | Reach a private address, name another tenant's secret, or shadow a reserved host. | The registration check, the write-only client secret, and the mint path bounds under "UC-2". |
| Another tenant's session | Nothing here. | Read any of this. | P4, UCP-7, and the pod refusal. |
| An operator impersonating Alice | Read what impersonation allows, and terminate or interrupt the session. | Give an affected session input through any adapter, write to Alice's memory, or write or rotate her credential. | UCP-3. |
| A crafted request on a host with rules | Spell an existing operation differently, such as an escape or a suffix. | Have a spelling reach the host that the rule did not check. | "What every rule set forces". The matcher is **[measured]** on the stub, which proves forwarding only, with 0 denied actions reaching with the credential. |

## History of this decision

| Step | What happened | Tag |
| :-- | :-- | :-- |
| 1 | `user-git-identity-design.md` revision 2 specified an allowlist filter, `vcs-authoring`, with input-key checks, in `netagent`. | **[design]** |
| 2 | On any image with `/etc/localtime`, `gh` sends `Time-Zone`, and the forwarded-header allowlist refused every GraphQL request with 403. | **[measured]** |
| 3 | Of 68 `gh` releases since 2024-10-01, 4 changed the mutation set. 16 of the 66 mutations in `gh` 2.103.3 were on the table and 50 were off. These are names read from source rather than run, and they are a lower bound on drift. | **[source]** |
| 4 | The owner measured the filter against both conditions and chose one generic binding with optional rules held as data, with the GitHub deny preset on by default. | Owner decision D1 |
| 5 | The owner accepted these losses: a new GitHub mutation that can approve or merge passes until the preset is updated, and the input-key and target checks go away. | Owner decision D1 |
| 6 | First-version sources are `registered` and `oauth2Link`. A tenant-run endpoint comes later. | Owner decision D2 |

## Current state

Each row was checked against the named source.

| Claim | Status | Source |
| :-- | :-- | :-- |
| `POST /v1/credentials` acts only for the authenticated caller | True. `caller` reads tenant and subject from the principal, and every handler uses them. | `pkg/gateway/credentials/credentialserver/credentialserver.go` |
| It refuses an impersonation bearer | False. That package has no `callercheck`, filed as F-13.3.26. | `credentialserver.go` |
| It accepts a closed provider set | True. `provider.IsValid()` rejects anything outside `credential.AllProviders()` with `VALIDATION_ERROR`, and §4.9 says the same. The `provider` column itself is `TEXT`, validated in application code. | `credentialserver.go`, `pkg/credential/credential.go`, migration 0036 |
| Three built-in providers register and never deliver | True. `credential.UserProxyDialect` has no dialect for `aws_bedrock`, `github`, or `vault_transit`, and `Materializer.Available` then reports them unavailable. Filed as F-4.9.46. | `pkg/credential/lease.go`, `usercreds.go` |
| A secret has a size limit | Only the 1 MiB request reader. `secret` is a JSON string. | `credentialserver.go` |
| A credential has an expiry | False. The table has status and timestamps and no expiry. | Migrations 0036, 0059, and 0073 |
| The wire format matches §4.9 | False. §4.9 names the body field `credential` and the identifier `credential_ref`. The code uses `secret` and `ref`. Filed as F-4.9.47, and no proposal here changes the names. | §4.9, `credentialserver.go` |
| Rotation reaches a running session | For the LLM proxy only. `RotateUser` re-caches the secret gateway-side. §4.9 also describes `RotateCredentials`. | `usercreds.go`, §4.9 |
| `DELETE` leaves running sessions alone | True for the LLM path, by statement: "Active session leases are unaffected". | §4.9 and [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api) |
| Ownership is the only rotate check | True. `ownedBy` compares `UserID` with the caller. | `credentialserver.go` |
| An impersonation bearer is refused on create, derive, replay, and create-and-start | True, through `requireCreatingBearerAllowed`. | `pkg/gateway/sessionserver/user_gate.go` |
| Other session input refuses an impersonation bearer | False. `manageSessionRoute` covers messages, resume, finalize, start, uploads, memory writes, and tool and elicitation answers with permission and ownership only. `lenny/send_message`, `lenny/memory_write`, and the `/v1/responses` and `/v1/chat/completions` translators reach `executor.Executor.Send` or the memory store on their own paths. Filed as F-13.3.26. | `pkg/gateway/sessionserver/route_table.go`, `pkg/gateway/mcpfabric/mcptools/send_delivery.go`, `pkg/gateway/environment/translator/open_responses.go` |
| A session row records the caller type | False. `Principal` carries `CallerType`, and the session store does not. | `pkg/gateway/middleware/auth/auth.go`, `sessionstore` |
| Gateway replicas decrypt user-scoped credentials | True, against §4.3's "This is the only component with KMS decrypt permissions for downstream OAuth tokens" and §4.9's "Token Service stores it". `credentialpg.New(w.pgPool, w.kmsProvider)` runs in the gateway. F-4.3.1 already holds this as its deferred cleanup. | `cmd/lenny-gateway/credentialsurface.go`, §4.3 |
| A pool's provider separates GHES from GitHub | False. `ProviderGitHub` is the one value, and §14 embeds it in `vcs.github.read`. The host separates them, as "Selecting the flavour" states. | `pkg/credential/credential.go`, §14 |
| Pool recycling is read from the current pool at release | True. `poolRecyclePolicy` reads the pool in the scrub reporter's seams. `lenny.dev/runtime-process` is stamped on a pod and immutable. | `pkg/gateway/session/recycle/scrubreporter_seams.go`, `pkg/admission/webhook/label_immutability.go` |
| Credential Secrets sit in one shared namespace, and tenant isolation holds where references enter a pool | True, for pools. Link providers have no such rule. | §4.9 **Secret references.** |
| A connector's `clientSecretRef` is resolved with no tenant check | True, and the route is `platform-admin` alone, so this is no tenant escalation. The gateway has no `get` on Secrets but the named admin token Secret, so a confidential connector's resolution fails (F-9.3.19). Both are recorded in `user-git-identity-design.md`. | `pkg/gateway/connectors/connectorsecret/connectorsecret.go`, `pkg/gateway/externalapi/admin/routes.go`, `charts/lenny/templates/credentials-namespace.yaml` |
| A delegated child inherits named bindings | True under the egress design's "Effective policy", which UC-3 narrows for this kind. | `egress-and-git-design.md` |
| A session's `env` is checked against a blocklist | True. The default holds exact names and `*_SECRET_*`, `*_SECRET`, `*_KEY`, `*_TOKEN`, and `*_PASSWORD`, and an operator can extend it. Other names pass. | [§14](../spec/14_workspace-plan-schema.md), `pkg/gateway/provisioning/envblock/envblock.go` |
| `inlineFile` content is inspected for secrets | False. | §14 |
| Events are durable | False. §25.5 says `Emit()` skips the Redis write when Redis is unreachable. | [§25.5](../spec/25_agent-operability.md) |
| Gateway interceptor phases exist for agent output, tool results, exports, messages, delegation, connectors, and proxy-mode LLM traffic | True, with `PreLLMRequest` in proxy mode only. | [§4.8 Gateway Policy Engine](../spec/04_system-components.md#48-gateway-policy-engine) |

## UC-1: Credential types and the `registered` source

### Types

A tenant defines types through EG-9's single write path. A type is a tenant object, and TS-1 can scope it by Environment.

```yaml
userCredentialTypes:
  - name: jira
    source: registered           # registered | oauth2Link. `endpoint` is reserved.
    hosts: [acme.atlassian.net]  # required for registered: exact canonical hosts
    delivery: inject             # the delivery ceiling: inject | file. Default inject.
    attach: "Authorization: Bearer {{secret}}"   # in the record, and format metadata under file
    registeredBy: [self, service]
    maxBytes: 16384
    requireExpiresAt: false
    maxLifetimeSeconds: 2592000  # optional
    environments: [web]          # optional, TS-1
```

A type's name matches `^[a-z][a-z0-9-]{0,62}$`, and it cannot equal a built-in provider name, so a type and a provider never collide. An unknown `source` value reads as absent to every component, which keeps `endpoint` additive. Each type receives an immutable `typeId` at creation, and rows key on it. Deleting a type deletes its rows, and a recreated type under the same name has a new `typeId` and inherits nothing.

**Hosts sit on the type.** Every host rule in this design evaluates type hosts rather than a binding's `hosts`: the VCS, pin, preset, and SP11 rules, and the one-binding rule. Type hosts are exact and canonical, and a binding's `hosts` must be a subset of them. A `registered` type can lie about its hosts, by declaring `docs.acme.com` and holding a GitHub token, and Lenny cannot tell. That is a residual, and the controls that enforce rather than check are `lockEgressToHosts` under `file` and `credentialPinned` on the host the token works on. EG-12 lands after these proposals, and until it does the second control is absent.

### Token trust record

Every decision about where a stored token can go and who can read it sits in one record. This replaces the separate per-field rules of the earlier revisions, each of which a review round found another way around.

| Field | `registered` type | Link provider |
| :-- | :-- | :-- |
| Identity | `typeId` and `source` | `name`, `kind`, and `clientId` |
| Endpoints | None | Authorization, token, revocation, and user info endpoints, and `host` on a VCS kind |
| Hosts | The type's `hosts` | `apiHosts` |
| Route and trust | The route value and the CA digest for each host | The same, for each endpoint host and API host |
| `attach` | On the type | On the provider, and fixed by kind on a VCS kind |
| Narrowing | None | Template identity: a shipped template's name and contract version, or a tenant template's digest, covering all of it |
| Delivery ceiling | `delivery` on the type | `delivery` on the provider |

- **Route value.** For a host, two ordered lists per scope, one for the tenant and one for each `environmentPolicies[E]`, in the order routes apply: one of egress routes, and one of git routes to a git endpoint, where a git route to `direct` or the mirror adds no entry, and the decision service's git list omits them too. Each entry names its matching pattern and target: kind, identifier, and the resolved target fields Lenny stores, including a registered address. Where no tenant route matches, the value names the platform default. A link has no Environment, so one value has to cover them all. Each route ends at a target that can present a certificate under a CA in the record, so both lists count. A git route to the mirror does not count: `netagent` terminates the host and sends the mirror its own service account token, so no user token goes there. An egress group does not count, because its Envoy tunnels bytes with no decryption, and an upstream it chains to is a route target, which does. A periodic re-resolution of a tenant-registered public endpoint suspends it on a bad answer and writes nothing here, so it flips no digest, and an edit to the address does change it. **The cost is that a scoped route, a move between Environments, a reorder, or a change to the platform default on a token's host makes tokens unusable for every Environment, and for the last, across tenants.** `tenant-scoping-identity-design.md` points here. A link provider endpoint or a VCS pool's mint host reachable only through an `environmentPolicies[E]` route is refused at write, because every Token Service call uses the tenant lists.
- **Digest.** The Token Service records the digest of the canonical record, which carries a format version, with each stored token, at write or at link, and keeps the record's content in `token_trust_records`. Each envelope is sealed with AEAD additional data under an injective, length-prefixed encoding: tenant, role (`access`, `refresh`, `clientSecret`, or `registered`), user where there is one, `typeId` or provider name, Environment, the digest, and the fields written with the value (`expires_at`, `written_by`, `generation`, `token_id`, `encoding`, and `provider_user_id`). An edited digest, a moved ciphertext, or a swapped role then fails to decrypt, and stored content that does not hash to the sealed digest is refused.
- **Comparison and use.** The Token Service computes the current digest and compares it at each I8 release, `leakScan` release, mint, refresh, narrowing, code exchange, user info, check, and revocation call. **Each call then derives its URL and expected route from the record content it compared. A call that carries a user token or a client secret asserts that route on the CONNECT, and the decision service refuses a disagreement.** That is a link provider call by the Token Service, an API request by `netagent` after a `userCredential` release, and a git request under `allowRuntimeHeldCredentials`. An App or project token mint carries neither, sits outside this record, and keeps the egress decision it has today, on the path the fetch takes, with no route assertion. A request's class, git path or API, tries in this order, in both the assertion and the decision: its own list for the session's Environment, its own list for the tenant, the other list for the Environment, the other list for the tenant, then the platform default. A delegation bound applies the formula the decision service uses, and every Token Service call uses the tenant lists. The assertion names the list it matched, `egress`, `git`, or `default`, and carries a digest of the target fields, so an address edit is seen. A host with a git route alone and a host with an egress route alone therefore both work **[design]**. The record holds a CA digest, and each call reads the CA from the host's endpoint or outbound host record and refuses one whose digest differs. On I8, `netagent` receives the expected route and CA and asserts the same. A route edit that reaches the decision service first therefore stops a held token rather than redirecting it. An assertion is compared at CONNECT, so `netagent` keys its upstream pool on the list and the route digest, and a route write closes pooled upstream connections to the hosts it changes **[design]**. In the run, idle pooled connections carried git past an edit, with 0 new CONNECTs, on a stand-in with no per-client-CONNECT decision **[measured]**. At level 2 the proxy must pass the header to its I3 call, as the egress design's contract states, and a proxy that drops it gets no comparison, which that conformance row is there to catch. A Token Service connection is reused only within one tenant, route, and CA, an asserted call never reuses an unasserted connection, a first release closes any unasserted upstream connection `netagent` holds to that host, and a call carrying a token or secret follows no redirect.
- **Mismatch.** The stored token is unusable and is sent nowhere, neither to an old endpoint nor to a new one, for both sources. Any difference counts, including a removed host or a lowered ceiling, because a direction per field is what this record replaces.
- **State.** A row has one state for this, `user_act_required`. It is derived from a mismatch, and it is stored when a provider refuses a refresh (UG-1). The new user act is a relink for a link, and a new write under `registeredBy` for a `registered` row.
- **Revert.** A record identical to the recorded one makes the token usable again, because nothing was sent in between. A row stays until a new act replaces it, its owner deletes it, or erasure runs. An operator who changes a record because it was compromised, such as after a CA leak or an endpoint takeover, also revokes at the provider or deletes the rows, because replaying the record would restore them.
- **Deleting a provider.** Deletion puts the provider in a `deleting` state, which keeps it, its record, and its client secret until its links have drained through UG-1's durable record or `linkProviderDrainSeconds` ends, 86,400 by default. Meanwhile it takes no new link and releases nothing, and a recreate under that name is refused. The ordinary comparison applies to each attempt, so a later route or CA change stops it. At the bound, or after a mismatch, each remaining row ends with an audit event and a user notification saying that its grant was not revoked. A deletion after a mismatch revokes nothing. Every `revoking` row, from a user's delete or §11.4, takes the same bound and notice, and a guardrail's `suspendedBy` provider still declares its endpoints for revocation.
- **Outside the record.** `permissions` on a shipped template limits what a child can do at the host, within what the App holds, so it stays out, and a change to it is audited and reported in posture. A rotated `clientSecret` value changes no field here. A row's `state` and exposure acknowledgements are mutable, and a replica can rewrite them, as the threat table states.
- **No CA on the binding.** A `userCredential` binding carries no CA and no `attach` of its own. The CA comes from the host's endpoint or outbound host record, which the record covers.

The guardrail `tokenTrust.allowedRecords` lists the records a `security-admin` has approved, each as its digest and content, because a person cannot review a hash, under SO-2's weakening delay. When it is set, EG-9's write path recomputes the digest of each provider and type a write touches, including a route or CA write, and refuses or suspends what is not listed. Posture lists each current record and whether it is on that list.

### What the record does not protect

**Stored tokens are protected against reading through configuration. Future grants are protected only by the page and the guardrail.** A tenant admin who controls configuration can still trick a user into granting a new token to a host she runs, or re-point an automated backend so its next write goes to a host she added. No per-field rule closes that, because she configures the tenant.

**On a name or CA the customer operates, whoever controls that DNS, CA, or network can read tokens, and the record cannot see it.** The record binds a name, and the target Lenny stores for it. A repointed `jira.acme.com` with a fresh certificate, or a new leaf under a private CA already in the record behind an unchanged outbound host, changes no field. GHES, self-managed GitLab, and internal trackers are the main users of link providers.

Two measures reduce the future-grant route:

- After a record change, the relink shows a page before the redirect, naming every endpoint host and stating that they changed. A first link shows the same hosts. The page gives little signal when a route or CA changed, because the names are the same, or under a new provider name, which shows as a first link.
- The client secret is sealed with the provider's record digest, so any record change leaves it unusable until a write re-supplies it, and a revert restores it. The stored client secret therefore does not reach her host, and a code she receives there cannot be redeemed with it. A tenant admin who holds the client secret in another way keeps that route.

The enforcing control for a tenant that needs this separation is `tokenTrust.allowedRecords`, which holds only when `security-admin` and `tenant-admin` are different people. Decision 4 records the owner's settlement of this limit.

### The known-provider restriction

This design **extends** the §4.9 restriction and leaves the `provider` path as it is for LLM credentials. `POST /v1/credentials` gains a `type` field, mutually exclusive with `provider`. `provider` stays a closed enum on the §4.9 router's path, and a `type` must name a type the tenant defines with `source: registered`.

Replacing the restriction, so any string is a provider, would let a typo store a secret silently and would send unknown strings through the LLM router.

The existing defect stays refused. `provider` values with no delivery path (`aws_bedrock`, `github`, and `vault_transit`) are refused with `VALIDATION_ERROR` once UC-1 lands, since a registration that can never deliver is misleading **[source]**. A tenant-defined type on a GitHub host is a different thing, and it is allowed (see "Interactions with VCS pools").

### Who can write

`POST /v1/credentials` acts only for the authenticated user **[source]**. A tenant backend needs a route that names the user.

```
PUT /v1/admin/users/{userId}/credentials/{type}
{"secret": "...", "encoding": "text", "expiresAt": "2026-11-04T00:00:00Z", "environment": "", "tokenId": "opaque-writer-id"}
```

- **Authorization.** A new permission, `manage_user_credentials`. It sits inside `TenantAdminPermissions()`, because §10.2 limits a custom role to that ceiling, so `tenant-admin` holds it, and a custom role such as `credential-writer` can hold it alone. A backend authenticates through §10.2's client credentials path under that role. This is a new role and permission rather than a scope, because `/v1/admin` routes authorize by permission, and §25.1 scopes bound tools rather than admin routes. Decision 4 records the cost that a tenant admin can write a token under any user's name. A `platform-admin` holds no special path here: the route acts within the caller's own tenant, so an operator cannot place a token in a customer's user, and the "all tenants" reach §10.2 gives that role does not extend to it.
- **Type check.** `registeredBy` must include `service`. `POST /v1/credentials` needs `self`, and it admits only a caller whose `caller_type` is `human`. A type with `registeredBy: [service]` lets a tenant refuse user-supplied tokens.
- **Owner check.** The row's owner is the named user. `ownedBy` extends for the route above: a caller with the permission may act on rows of its own tenant, and the existing routes still need the caller to be the owner. A rotation through `PUT /v1/credentials/{credentialRef}` by the owner remains possible where `registeredBy` includes `self`, under the same `caller_type: human` check. The row records `written_by`, the subject and caller type of its last writer, and Alice's `GET /v1/credentials` returns that value, so she can see a token she did not supply.
- **Named user.** The subject is accepted as given, the same choice TS-4's upsert makes, because a user may not have a row yet. These subjects are refused: one with an invalidation level or an Article 18 restriction, one with a completed erasure job, one holding a security role (SO-2, on SP2's reasoning), and one that is a service, by its registry row or a VCS pool's `serviceIdentities`.
- **Impersonation.** An impersonation bearer is refused on this route and on every `/v1/credentials` write, through `callercheck`. Otherwise `written_by` would record Alice for an operator's write, and rotation would reach her running agent.
- **Bytes.** The value is opaque. `encoding` is `text` or `base64`, for binary tokens and files. The limit is `maxBytes`, at most 64 KiB at the platform ceiling, because `netagent` holds the value in memory and I8 carries it. The current handler's 1 MiB reader stays as the outer bound.
- **Expiry.** `expiresAt` is optional, and required when the type sets `requireExpiresAt`. `maxLifetimeSeconds` bounds it. I8 releases nothing past it, and `netagent` removes the file at that time from the `expiresAt` it holds. The Token Service clamps `cacheUntil` to it.
- **Storage.** A new table in the Token Service's store, with row-level security, holds type rows keyed on tenant, user, `typeId`, and Environment, with `expires_at`, `written_by`, `encoding`, `token_id`, `generation`, `state`, and `trust_digest`. It is envelope-encrypted under a per-tenant KMS key that only the Token Service's identity can decrypt, as §4.3 reserves, with the additional data under "Token trust record". `pkg/kms/envelope`'s `Cipher.Seal` and `Open` bind the KEK alias alone today, so they gain that input, and `Reseal` rewraps the DEK, so re-encryption is unaffected. This shares a Postgres role with the gateway, so a replica can edit rows, and the sealed form is what makes an edit fail. The gateway passes a write through to the Token Service over mTLS and sees the value in transit, as it does today. `GET /v1/credentials`, `ownedBy`, and `revoke` read a type row's metadata through the same Token Service API. The existing `credentials` table stays for LLM credentials, unchanged, because its rows are decrypted in the gateway. §4.9.1 re-encryption gains this table with the Token Service as its runner, because `pkg/kms/rekey` runs in the gateway today, and §12.8's `TokenStore` deletion gains it too. Erasure of a writer pseudonymizes `written_by` on other users' rows, and the job re-seals each through the Token Service, because `written_by` is in the additional data. Under `kms.provider: local` the chart mounts one master key in the gateway, the Token Service, and `lenny-ops`, so this separation holds on a cloud KMS alone.
- **Audit.** `credential.registered`, `credential.rotated`, and the others of §4.9.2 carry `type` and `written_by`, and the secret stays out of every log, as §4.9 already requires of these routes.

### Rotation and removal

Rotation uses the existing `PUT /v1/credentials/{credentialRef}` and the new admin route. `RotateUser` only re-caches for the LLM proxy **[source]**, so a type row takes a new propagation path:

1. The store commits the new material and increments `generation`.
2. The gateway pushes a binding change for each session of that user holding a binding of that type, as it does for a binding definition edit.
3. `netagent` flushes, asks I8, and receives the new value. Under `file` it swaps the directory atomically, with `binding.json`'s version counter incremented, as the egress design describes.

A lost push costs at most the egress design's 30-second re-read for the binding, and `cacheUntil`, 300 seconds for this kind, is the backstop if the subscription is lost. Tools that re-read the file pick up the new value, and a tool that read it once keeps the old value. The egress design tested re-reading for the tools it names **[measured]**.

`POST .../revoke` and `DELETE` both stop use on a type row. §4.9 lets `DELETE` leave running leases alone. That rule belongs to the LLM path, and it is unsafe for a token a tenant is trying to remove, so type rows behave differently, which the API documentation states.

A token never enters a shared cache keyed without its owner. The Token Service's cache key for a user credential or a verdict always includes the row or `link_ref` and its `generation`, and it never uses the egress mint cache. Each cached value is sealed with the full cache key and the record digest as additional data, through the same input on `pkg/tokenservice/cache`'s `Cipher`, so a replica copying an entry to another key produces a value that fails to open.

## UC-2: The `oauth2Link` source

`user-git-identity-design.md` UG-1 owns the link flow: the browser binding, the page before the redirect, the confirmation page, the refresh lease, the lost response rule, and provider revocation. UG-1 keys links on the link provider's name from the start, so this generalization adds `kind: oauth2` and reworks no stored key or route.

```yaml
userCredentialTypes:
  - name: salesforce
    source: oauth2Link
    linkProvider: salesforce-acme
linkProviders:
  - name: salesforce-acme
    kind: oauth2                   # or github, github-enterprise, gitlab
    delivery: inject               # the delivery ceiling: identifier | none | inject | file
    attach: "Authorization: Bearer {{secret}}"   # fixed by kind on a VCS kind
    authorizationEndpoint: https://login.salesforce.com/services/oauth2/authorize
    tokenEndpoint: https://login.salesforce.com/services/oauth2/token
    revocationEndpoint: https://login.salesforce.com/services/oauth2/revoke   # optional
    userInfoEndpoint: https://login.salesforce.com/services/oauth2/userinfo   # required
    idField: user_id
    loginField: preferred_username
    apiHosts: [acme.my.salesforce.com]
    clientId: "..."
    clientSecret: "..."            # write-only
    scopes: [api, refresh_token]
    identityBinding: unverified
    narrowing: null                # optional template
```

- **Client secret.** `clientSecret` is a write-only value. The Token Service stores it envelope-encrypted under the tenant's Token-Service-only key, in a row keyed on tenant and provider name, with the provider's record digest sealed as additional data. No route returns it, and the Token Service sends it only when that digest matches, under "What the record does not protect". At read time the Token Service compares the row's tenant with the provider's and the caller's, and it refuses a mismatch. No field names a Secret, so a tenant cannot point at another tenant's.
- **Key.** A link is keyed on tenant, user, and link provider name. A VCS kind also has `host`, unique per tenant, because a `required` pool's check has to name one account.
- **Delivery ceiling.** `identifier` keeps the identity and discards the token, for UG-4, `none` keeps the token in the Token Service for a check, `inject` allows `netagent` memory, and `file` allows the agent's reach. The pages show it, and it is in the record, so a link a person made for a check does not become a delivery source when an admin adds a type.
- **Refresh token.** It never leaves the Token Service (UCP-5). `netagent` receives an access token, or a narrowed one, on I8.
- **Hosts.** A type's `inject` hosts are the provider's `apiHosts`. A binding cannot name another host, and the Token Service re-checks that on I8. An endpoint on a host a VCS pool covers, or on a VCS kind's API host, is refused on `kind: oauth2`, so that kind cannot stand in for a VCS kind, and a pool added later suspends such a provider. `apiHosts` on a `kind: oauth2` provider can otherwise misstate where a token works, as a `registered` type's hosts can.
- **Rotation assumption.** The Token Service treats every provider as rotating refresh tokens, which is UG-1's single-use rule. A refresh response with no new refresh token keeps the old one. A provider that issues no refresh token gives a link that ends at its access token's expiry and moves to `user_act_required`. A response with no `expires_in` is stored as non-expiring and reported in posture, with `cacheUntil` still applying. VCS kinds refuse that case, as UG-1 states.
- **Revocation.** Provider revocation runs when `revocationEndpoint` is set and the record matches, through UG-1's durable record, and **Deleting a provider** covers deletion. Otherwise Lenny deletes its copy, and the provider's token runs to its own expiry. The tenant admin's write response states this.
- **Identity.** `userInfoEndpoint` and `idField` are required on `kind: oauth2`, so the confirmation page always names an account, and the unique index on tenant, provider, and `provider_user_id` applies. A provider with no such endpoint cannot be linked, and a tenant uses `registered` for it. `identityBinding` takes the values UG-1 defines. `allowedAccounts` applies through a named claim in that response.
- **Callback.** The flow's callback is per provider, `GET /v1/user-links/callback/{provider}`, and the `state` names the provider. Where a provider returns `iss` (RFC 9207), the Token Service checks it, so a code from one provider cannot be exchanged at another's token endpoint.
- **Endpoints are hostile input.** UG-1's VCS kinds fix endpoint paths, and `kind: oauth2` cannot. Each endpoint must be HTTPS and pass the egress design's check for a tenant-registered public service: resolved at registration, refused on the exclusion list, a registered `ipBlock`, or the cluster's addresses, and re-resolved periodically. The authorization URL also passes the §9.2 domain check. A private provider follows `tenant-outbound-connector-design.md`: the host is an outbound host, and the calls go through the mint path to it.
- **Route.** The Token Service reaches every link provider endpoint through `lenny-egress` with the tenant asserted, under the mint path's bounds on time, response size, and redirects. The egress design's direct dial for a public host excepts these endpoints, so the decision service sees each call and re-resolution applies. At level 1 the egress design states the equivalent.

### Narrowing templates

A narrowing template derives a per-session token from the stored one. It is data, so `netagent` and the Token Service hold no provider code for it.

```yaml
narrowing:
  name: github-scoped
  followsEntryState: true
  request:
    method: POST
    url: "{{apiBase}}/applications/{{clientId}}/token/scoped"
    auth: basicClient
    json:
      access_token: "{{parentToken}}"
      target_id: "{{targetId}}"
      repository_ids: "{{sessionRepositoryIds}}"
      permissions: "{{permissions}}"
  permissions: {pull_requests: write, contents: read}   # the one field a tenant overrides on a VCS kind
  response: {token: "$.token", expiresAt: "$.expires_at"}
  revoke: {method: DELETE, url: "{{apiBase}}/applications/{{clientId}}/token", auth: basicClient, json: {access_token: "{{token}}"}}
```

- Lenny ships `github-scoped` and `gitlab-fine-grained`. The first rests on documented endpoints **[documented]**, and the second is **[pending]** spike S13 of `user-git-identity-design.md`.
- **A VCS kind takes shipped templates only**, with the tenant override limited to `permissions`, and a VCS kind whose `delivery` is not `none` requires one. A template is optional on `kind: oauth2`, where a tenant can define its own.
- **`followsEntryState` is honoured only on a platform-shipped template.** It says the template narrows to `granted` entries per mint, which `security-oversight-design.md`'s repository rule requires. Only `github-scoped` sets it today. Any other template is treated as non-`mint-scoped` for SP11.
- `{{apiBase}}` comes from the table under "Selecting the flavour". `{{parentToken}}` is the access token alone, and no variable reaches a refresh token. Available variables are fixed, and a template cannot reference another tenant's data.
- A tenant's `permissions` widening on a shipped template stays within what the App holds, and each widening is audited and reported in posture. The owner settled this under `user-git-identity-design.md` decision 42, which states what then relies on the deny preset alone. A tenant's own template on `kind: oauth2` is in the record whole, because it can render `{{permissions}}` as a scope.
- A template's URL must stay on origins in the record, because it carries the parent token.
- A template failure refuses the mint, and a response equal to the parent token is a failure. It never falls back to the parent.

**With no template, on `kind: oauth2`, the delivered access token has the user's full reach on that provider.** Under `file` an agent holds all of it. Under `inject` `netagent` holds all of it, and request rules are the only limit Lenny applies. A binding write response and the posture read state this.

## UC-3: The `userCredential` binding

The owner's core record:

```yaml
secret: { kind: userCredential, type: jira }
hosts: [acme.atlassian.net]
delivery: file        # or inject
```

The binding is the same record under both deliveries.

| Field | Meaning |
| :-- | :-- |
| `secret` | `kind: userCredential` and a `type`. The egress design's `secret` becomes a typed union. |
| `hosts` | A subset of the type's hosts. Under `inject`, `netagent` attaches to these alone. Under `file`, a write-time check only, as "What `file` does not promise" states. |
| `delivery` | `inject` or `file`, at or below the record's ceiling. `federation` and `metadata` do not apply, and environment variables are never offered. |
| `attach` | Absent. It comes from the type or link provider, in the token trust record, so an edit cannot move the token into a header a host logs. A write of an `inject` binding on a `registered` type with no `attach` is refused. Under `file`, `binding.json` carries it as format metadata, which the `netrc` projection reads. |
| `required` | Whether a session with no resolvable token is refused. Default `true`. |
| `scope` | `onRequest`, or `poolAttach`, which only a VCS pool's `prAuthoring: user` sets (`user-git-identity-design.md`, "The attach rule"). `always` is refused, because its cache keys on the tenant pin and it attaches to every session. |
| `requestRules` | `inject` only. See UC-4. A write setting it with `delivery: file` is refused with an error that says why. |
| `lockEgressToHosts` | `file` only, optional. See "What `file` does not promise". |
| `leakScan` | `file` only, optional. See "Leak scanning". |

### Limits under both deliveries

**Only the session's own user's token.** The Token Service reads the session row's user and `caller_type` through `pkg/binding`, and it looks up the type for that user. Nothing in the request names a user. The session's Environment selects the row when the type has rows per Environment, then the no-Environment row. An Environment row that is mismatched or suspended stops the lookup, so a session does not silently use a different token. `caller_type` is a new field on the session row, set at create from the principal, because only `Principal` carries it today **[source]**. A delegated child's row copies its parent's value, because §8.2 mints no token for a child. An empty value is treated as not `human`.

**Pods that can serve someone else are refused.** A session naming such a binding is refused with `422 USER_CREDENTIAL_UNSUPPORTED_POD` on these pods:

| Pod | Why |
| :-- | :-- |
| `maxConcurrentSessions > 1` | `netagent` cannot attribute a request to a slot, and a file is readable by every slot. |
| Service mode | One process serves several sessions, and §5.2 gives it no session binding. |
| `recycle.enabled: true`, whichever `runtimeProcess` | §5.2 states the scrub "is **best-effort** and is not a security boundary". On a `keep` pod the runtime process survives, and any process keeps answering on `netagent`'s loopback port under the next user. A copied token also survives in whatever the scrub misses. |
| A pod that has served a session | The same reason. It catches a pool edited back to `false` with used pods in it. |

These read pool policy at bind. At bind, for a session naming such a binding or declaring a repository under a `required` pool, the gateway writes `retireOnRelease` on the claim. **I8 refuses a release unless the claim carries the mark**, so a binding that a mid-session edit turns into a `userCredential` binding delivers nothing until a new session. Each release path honours the mark whatever the pool now says, because `poolRecyclePolicy` reads the current pool **[source]**: `podsession.Binder.Release`, `Server.ReleaseSuspendedPod`, `PodExecutor.Release`, and the scrub reporter's seams. A marked pod is never recycled and never given another slot.

SDK-warm pods remain allowed, because `netagent` receives the binding at bind and no earlier agent session has run. A warm process can read the file once it exists, and that process is this session's runtime.

**Impersonation.** `requireCreatingBearerAllowed` already refuses create, derive, replay, and create-and-start **[source]**. For a session in a delegation tree where any session, read by `root_session_id`, holds a `userCredential` binding or declares a repository under a `required` pool, one check, built on `callercheck`, refuses an impersonation bearer with `403 FORBIDDEN` and the reason `impersonation_user_credential`. The tree matters because a child with no lease entry can pass a message on to its parent under a principal with no JTI, which the check does not see. The check reads the tree at input time, so input to a tree before it becomes affected passes:

- **Messages, at the session input layer.** The tree has no single delivery service. `executor.Executor.Send` is the shared call, from `sessionserver`'s `restDelivery`, `mcptools`' `send_delivery.go`, and the `translator` package's `/v1/responses` and `/v1/chat/completions` handlers, and held messages enter through `sessioninbox.Inbox.Enqueue` and its DLQ. A wrapper on each reads the principal from the context, so every adapter inherits the check. `deliverBuffered` runs with no principal and passes, because what it drains passed at enqueue.
- **Other input, at the route.** `resume`, `finalize`, `start`, `upload`, `upload-archive`, `upload-to-session`, tool-use and elicitation answers, and an `inReplyTo` reply, which `inputwait.Registry.Resolve` takes before any `Send`, do not pass `Send`, so `manageSessionRoute` and the MCP session tools apply the check, by default-deny. Exempt routes are `DELETE`, `terminate`, `interrupt`, and reads.
- **Memory, unconditionally.** §9.4 memories persist across a user's sessions, so a write on an unaffected session reaches an affected one later. `handleMemoryWrite`, `handleMemoryDelete`, and `lenny/memory_write` refuse an impersonation bearer on every session.

A test enumerates every mounted mux (`httpsurface.go`, the MCP tools, and each registered `ExternalProtocolAdapter` prefix), and it fails when a route or tool declares no status.

**Sessions with no human user.** UCP-1 needs a human. A session whose `caller_type` is not `human` has no user token. With `required: true`, a session naming the binding is refused with `USER_CREDENTIAL_NOT_FOUND`. With `required: false`, the session starts: under `file` no directory exists, and under `inject` requests go out unauthenticated and the host answers 401. A human with no usable token gets the same outcomes, with `USER_LINK_REQUIRED` and `details.linkUrl` in place of the not-found code on an `oauth2Link` type, and `details.reason: trust_changed` on a mismatch. A `service` writer does not change that, because `registeredBy` governs writing and the session row governs reading.

**Delegation.** A delegated child carries its parent's user (§8.2 **Child identity.**) and, under the egress design, inherits named bindings. A `userCredential` binding is the exception. A child receives one only when the delegation lease names it, in a new `userCredentialBindings` list in the lease, which a parent can only narrow, and a pool's `poolAttach` does not bypass that. `credentialPropagation` is unrelated, as it governs LLM credentials. A child's pod must pass the pod test, and a pool it cannot pass refuses the delegation. A detached orphan keeps what it was granted until `cascadeTimeoutSeconds`. Under TS decision 14's recommended option a child stays in its parent's Environment, and under the alternative a crossing drops the binding unless the bound's `bindings` names it.

**One binding per host.** A session holds at most one `userCredential` binding per type host, under any delivery, at most one `inject` binding of any kind per host, and no `file` binding beside an `inject` binding on a host. The egress design carries this rule, evaluated on type hosts.

**Audit.** Each I8 release writes a `credential.mint` record with `tokenSource`, the opaque `credentialRef` or `linkRef`, the binding, its delivery, hosts, and expiry. `tokenSource` is `app`, `userRegistered`, `userOauth2`, or `userChild` on a release, and `userCheck` on a check's use of the parent, which delivers nothing, and `user-git-identity-design.md` uses the same values. An OAuth2 token or child records a token hash, which joins to a provider's log. A `registered` value records its `generation` and a per-tenant keyed hash, because a value of unknown entropy under a plain hash would correlate across tenants and permit offline guessing. The record carries no user identifier, so §12.8's statement for that stream holds, and the join lives in rows that erasure deletes. The first delivery to a session also writes a `user_credential.delivered` audit event to the tenant's §11.7 chain, naming the session, binding, type, and delivery. That chain is sized for low rates, so later refreshes appear in `credential.mint` alone.

**Provisional state.** On a resume rebuild, a `userCredential` binding is unavailable until `running`, as `file` and write tokens are under the egress design.

### Stopping a credential

| Event | I8 | `netagent` | Under `file`, copies | At the provider |
| :-- | :-- | :-- | :-- | :-- |
| Session end | Refused, as the binding is gone | Flushes, and removes the file | Remain wherever the agent put them | Unchanged |
| `revoke` or `DELETE` | Refused | Binding change: one round trip, or 30 s when a push is lost | Remain | `oauth2Link`: revoked when an endpoint exists. `registered`: Lenny cannot. |
| `expiresAt` passes | Refused | Removed at that time | Remain, and the provider decides | Provider's rule |
| User disabled or erased (TS-4, §12.8, [§11.4](../spec/11_policy-and-controls.md#114-user-invalidation)) | Refused | Binding change, and §11.4 ends sessions at `full_revoke` | Remain | As `revoke` |
| A token trust record changes | Refused, as the digest differs | Binding change | Remain | Unchanged, and Lenny sends nothing there |
| `quarantined` | Refused, from each instance's own read | Not relied on. See "Delivered user tokens" in `forensics-evidence-preservation-design.md`. | Remain, and the volume is preserved | Unchanged unless an investigator revokes |
| `userCredentialDelivery` withdrawn, an SO-2 guardrail, or a pin added on a type host | Refused, through `suspendedBy` | Removes the file | Remain | Unchanged |

## UC-4: Request rules and presets

Under owner decision D1, the provider-specific allowlist is replaced by one generic binding with optional rules held as data. `netagent` carries no provider code for this. A generic HTTP and GraphQL rule evaluator reads whatever rules I8 returns.

**Request rules apply only under `inject`. They cannot apply to a token the runtime holds, because the runtime sends it to the host itself.**

```yaml
requestRules:
  preset: {name: github-deny, version: 7}   # or `none`. `version` pins it.
  default: allow          # allow | deny
  rest:
    - {action: deny, methods: [POST], path: "/repos/*/*/pulls/*/reviews"}
  graphql:
    paths: [/graphql]
    denyMutations: [addComment]   # or allowMutations under default: deny
  bodyPatterns:
    - {action: deny, pattern: '(?i)^\s*/(lgtm|approve)\b'}
```

| Rule | Semantics |
| :-- | :-- |
| `default` | What a request matching no rule does. `allow` by default. A tenant can set `deny` and list what it wants, for an allowlist of its own. |
| `rest` | Method and path patterns on the canonical path, below any preset prefix. `*` matches one segment, a trailing `**` matches zero or more, and `{encoded}` declares an encoded segment. A family rule with no method named applies to every method but `GET`, `HEAD`, and `OPTIONS`. Matching is case-insensitive, so a deny rule catches more spellings rather than fewer. |
| `graphql` | The endpoint paths, and top-level mutation names to deny or allow. |
| `bodyPatterns` | RE2 patterns, applied per line to every string value in a JSON body, form value, and query value of a write, and to every string and block string literal in a parsed GraphQL mutation: arguments at any depth, variable defaults, directive arguments, list values, and object values **[measured]**. `denyBodyKeys` applies to the same mutations and to REST writes. |
| Tenant additions | A tenant's rules apply on top of the preset. Deny wins. A binding that sets rules and no `preset` key keeps the default preset, and only `preset: none` clears it. |

### What every rule set forces

When a binding has any rule, these apply to its requests. Rows are **[measured]** by the spike's prototype, on a stub that proves forwarding only, except as marked. 3,033 of 3,045 predicted rows matched across the flavours, and the 12 others were harness errors. Path, GraphQL path, and family matching are case-insensitive.

- **Canonical path.** Any `%` outside a declared `{encoded}` segment is refused, on every method. A backslash, `;`, dot segments, and control bytes are refused. Empty segments and trailing slashes are dropped. `netagent` forwards the canonical path, so a host never decodes what a rule did not. A deny rule on `DELETE /rest/api/*/issue/*` therefore also catches `iss%75e`, by refusing it.
- **Final segment.** For a deny rule and a refusal, the final segment is also compared with anything after its first `.` removed, and a dotted deny pattern also compares at each dot. The stem only widens a refusal: a suffixed path never matches an admit, whether an allow rule, a listed GraphQL path, or the uploads path. A path no rule denies keeps its suffix when forwarded. `merge.foo`, `merge.json.foo`, `merge.json.json`, `merge.JSON`, `reviews.x`, and GitLab `approve.foo` were refused, `POST .../issues.json` and `GET .../labels.json` were forwarded with the suffix, and a dot in a repository name is no final segment, so `POST /repos/acme/widgets.js/issues` passed. A generic Rails router sent `merge.foo` to the handler, and Grape answered 404. GitHub's stack is unknown. The direction rule held: `POST /graphql.json`, `.foo`, and `.json.foo`, and `/api/uploads/.../assets.json`, were refused with the exact paths passing, and under `default: deny` an allow rule refused `.json` and `.foo` on `GET` and `POST`. A deny on `/files/report.tar` refused `report.tar.gz` and `REPORT.TAR.GZ` and passed `x.report.tar` **[measured]**.
- **Encoded segments.** In a declared `{encoded}` segment, the value is decoded once, must contain no `%` and no dot segment, and must contain no word on that pattern's `refuseDecoded` list. The encoded form is forwarded as given. `glab`'s `acme%2Fwidgets`, nested groups, and lower-case `%2f` pass, and labels, contents, branches, and tags with escapes pass under the presets' declared segments, and `acme%2Fwidgets%2Fmerge_requests%2F7%2Fapprove` is refused on its route word.
- **The GraphQL and API families, under a preset.** A path in `/graphql*` or `/api/*` is refused unless it is the resolved flavour's GraphQL path or uploads path, or lies under a non-root `restPrefix`, and so is a path whose first segment below the `restPrefix` begins with `graphql`. Under a root prefix `/api/*` has no prefix exception. `/graphql/foo`, `/graphql-tools`, `/graphql2`, `/api/foo`, `/api/graphqlx`, and `/api/v3/graphql` were refused on each flavour. `/repos/acme/graphql-tools/...`, `/repos/acme/graphql`, `/repos/graphql/graphql-js`, `/users/graphql`, and `/search/graphql` reach on `GET`.
- **`POST .../graphql`, and suffixed GraphQL paths.** A `POST` whose final segment, or its stem, is `graphql` is refused unless it is the flavour's listed path, because a host's own routing of such a path is unknown. `POST .../widgets/graphql`, `.../graphql.json`, `.../GraphQL`, and `POST /repos/acme/graphql` were refused, and `PATCH /repos/acme/graphql`, `PUT` and `GET .../widgets/graphql`, and `POST /repos/acme/graphql/issues` passed **[measured]**. A suffixed spelling of a listed GraphQL path is also refused on any method **[design]**, because `PUT /graphql.json` with a denied mutation reached the stub on a generic rule set under the `POST`-only form **[measured]**, and a host could take a mutation in the query of `GET /graphql.json`. `PATCH` and `DELETE` on a repository named `graphql`, a label with that name, and `PUT .../contents/graphql` stay admitted. In GitHub's OpenAPI description, 1.1.4, no path contains `graphql`, and of its 197 `POST` routes 5 end in a parameter, so the one route this can refuse is `POST /orgs/{org}/artifacts/metadata/deployment-record/cluster/graphql`, for a cluster with that name. That check covers GitHub alone. On GitLab, `POST .../repository/files/graphql.json` at a repository root is refused, which is a known false refusal.
- **Writes outside a non-root prefix.** Under a preset whose prefix is not root, any write outside `restPrefix`, `graphqlPaths`, and the uploads path is refused, so a GitLab under a relative URL root has its writes refused rather than unchecked. On GHES, a write to `/repos/.../merge`, `/x`, and a relative root was refused, and `GET /x` passed, and GitLab was the same. On GitLab, `rest` patterns are rooted at `projects/*` below `/api/v4/`, and the unencoded `POST /api/v4/projects/acme/widgets/merge_requests/7/approve` still reached the stub, and Grape answered 404.
- **Method overrides, upgrades, and routing overrides.** `X-HTTP-Method-Override`, `X-HTTP-Method`, `X-Method-Override`, `_method` in a query or a form, `X-Original-URL`, `X-Rewrite-URL`, `X-Forwarded-Path`, and `X-Forwarded-Prefix` are refused, including with an empty value, on preset hosts and a generic rule set. `Upgrade`, `HTTP2-Settings`, `Sec-WebSocket-Key`, and `CONNECT` inside the tunnel are refused on the preset hosts tried. `X-Forwarded-Uri`, `X-Envoy-Original-Path`, and `X-Original-Method` are refused too, and the list is extendable as data. The egress design refuses the routing override list on every terminated host, with or without rules **[measured]**. `_method` inside a multipart body streams on a generic rule set, which is a stated limit, because parsing it would buffer every attachment. A preset's `writeForm` refuses a multipart write.
- **GraphQL, on a path a rule lists.** It is `POST` alone, and it must parse. **Unparseable GraphQL is refused, and the printed document is forwarded.** The client's document never reaches the host. A bare CR document passed an earlier filter and reads as `addPullRequestReview` under graphql-ruby's default lexer **[captured]**, and which parser a provider runs is unknown. A lexical rule also refuses control characters, U+0085, U+2028, U+2029, and U+FEFF. One operation per document. A mutation's root selections must be fields, whose names are checked with aliases ignored. A subscription is refused. **A `query` document that defines or spreads a fragment on `Mutation`, or names a denied mutation field at any selection, is refused,** including an inline `... on Mutation`, and none of the 166 corpus documents was refused by this rule. A query is otherwise not checked further. The 166 corpus documents all pass the bounds, and the 159 from the earlier run were idempotent under printing.
- **Envelope and JSON, where a body is inspected.** The envelope is an object, `query` is a string, and `variables` is an object, null, or absent, so a top-level array or scalar and string-encoded variables are refused. Decoding is at the token level at every depth. A duplicate key, a case-variant top-level key, invalid UTF-8, a lone surrogate, nesting past the bound, and data after the object are refused. `netagent` re-encodes the decoded tree, with number text preserved.
- **Bounds.** The defaults are 512 KiB per body, 64 KiB per document, 4,096 tokens, selection depth 32 with fragment spreads followed and a cycle refused, value depth 32 counting the leaf, JSON depth 32, 16 root fields per mutation, a 30 s body read, 4 concurrent checks per pod counting inspected requests, with 429 and `Retry-After` past that, and a 100 ms check budget, with 503 past that. They are data, within platform ceilings. The size and depth bounds each passed at their limit and were refused one past it, and body read, concurrency, the check budget, and head read ran on github.com, with no one-past row. The largest corpus values are 698 tokens, selection depth 14, 1 mutation root field, value depth 2, and 7,180 document bytes. The head read deadline is the egress design's listener default, since no host is known before a head is read. Check cost and the rest of the evidence are under "Bounds and cost" in `user-git-identity-design.md`.
- **Buffering.** Only a request needing inspection is buffered: a listed GraphQL path, a write under `bodyPatterns` or `denyBodyKeys`, and a form write, because `_method` in a form cannot be refused otherwise. Other writes stream, unre-encoded, so a Jira attachment passes. On GitLab a REST JSON write is re-encoded, and a GitHub REST write streams unchanged. A `reauth-on-401` retry applies only to a buffered body. A streamed write's 401 goes back to the client, and `netagent` flushes the token, so the client's own retry gets a fresh one.

### Request forms belong to a preset

These are preset properties, so a generic rule on Jira does not inherit GitHub's and GitLab's forms:

| Property | Meaning | `github-deny` | `gitlab-deny` | A rule set with no preset |
| :-- | :-- | :-- | :-- | :-- |
| `writeForm` | Query strings and body types admitted on a write | No query string, and JSON alone | No query string, because GitLab's page shows `POST .../notes?body=note` **[documented]**, and JSON alone | Any |
| `encodedSegments` | Segments where `%` is admitted | `contents`, `branches`, `labels`, `compare`, `git/ref`, `git/refs`, `git/matching-refs`, `releases/tags`, `commits`, and `environments` values | `projects` and `groups` values with `refuseDecoded: [merge_requests, ..]`, and `repository/branches`, `files`, `tags`, `commits`, and `tree`, `labels`, and `wikis` values | Those a rule declares |
| `refuseFamilies` | Whether the GraphQL and API family rule applies | Yes | Yes | No |
| `denyBodyKeys` | JSON keys refused on a write, at any depth | None | `merge_when_pipeline_succeeds`, `auto_merge` | None |
| `undecodableBody` | What a write `bodyPatterns` cannot decode does | Refused | Refused | Passes, with a metric |

### Headers

**The forwarded-header allowlist is replaced by stripping credential headers only.** `Time-Zone`, sent by `gh` on any image with `/etc/localtime`, made the allowlist refuse every GraphQL request **[measured]**, and a client release can add a header at any time. `netagent` overwrites the `attach` header and removes `Authorization`, `Private-Token`, `Job-Token`, `Deploy-Token`, `X-Api-Key`, `Cookie`, `Proxy-Authorization`, and the host's per-host credential list from the egress design's shipped list. `gitlab-deny` also strips `Sudo` **[measured]**. Every other header is forwarded. **A forwarded header can carry meaning at a host this design knows nothing about, and that is a residual.**

### Presets

Presets ship as data, as the egress design's provider templates do. A preset has a name, a version, paths per API flavour, the properties above, and rules. Rules reach `netagent` resolved, on I8, so a preset update reaches a pod at its next `cacheUntil` with no image change. `handling` names the rule evaluator's minimum version, so an image older than a rule's format refuses the credential.

```yaml
- name: github-deny
  version: 8
  flavours: [github, github-enterprise]     # paths from the table below
  repoRoots: [/repos/*/*, /repositories/*]   # rules below name {repo}
- name: gitlab-deny
  version: 3
  flavours: [gitlab]
```

**The body is the extended preset, which the owner settled under decision 24.** Each REST rule below `{repo}` applies under both root forms, because GitHub serves `/repositories/{id}/...` as well.

| Family | `github-deny`, GraphQL mutations | `github-deny`, REST writes |
| :-- | :-- | :-- |
| Review | `addPullRequestReview`, `submitPullRequestReview`, `updatePullRequestReview`, `deletePullRequestReview`, `dismissPullRequestReview` | `{repo}/pulls/*/reviews/**` |
| Merge | `mergePullRequest`, `enablePullRequestAutoMerge`, `disablePullRequestAutoMerge`, `enqueuePullRequest`, `dequeuePullRequest`, `mergeBranch`, `updatePullRequestBranch`, `revertPullRequest` | `{repo}/pulls/*/merge`, `merge-async`, `update-branch`, `{repo}/merges`, `{repo}/merge-upstream` |
| Content and ref | `createCommitOnBranch`, `createRef`, `updateRef`, `updateRefs`, `deleteRef`, `createLinkedBranch` | `{repo}/contents/**`, `{repo}/git/refs/**`, `{repo}/git/commits`, `trees`, `tags` |
| Deployment | `approveDeployments`, `rejectDeployments`, `createEnvironment`, `updateEnvironment`, `deleteEnvironment`, `pinEnvironment`, `reorderEnvironment` | `{repo}/actions/runs/*/pending_deployments`, `approve`, `deployment_protection_rule`, `force-cancel`, `{repo}/environments/*` on `PUT` and `DELETE`, `{repo}/environments/*/deployment-branch-policies/**`, and `{repo}/environments/*/deployment_protection_rules/**` |
| Policy | `createBranchProtectionRule`, `updateBranchProtectionRule`, `deleteBranchProtectionRule`, `createRepositoryRuleset`, `updateRepositoryRuleset`, `deleteRepositoryRuleset` | `{repo}/branches/**`, `{repo}/rulesets/**`, `/orgs/*/rulesets/**`, `{repo}/actions/permissions/**` |
| Credential | None named | `/user/keys/**`, `/user/gpg_keys/**`, `/user/ssh_signing_keys/**`, and `{repo}/keys/**` |

**Evidence for `github-deny` [measured].** A name search over GitHub's OpenAPI description (`rest-api-description`, `info.version` 1.1.4) and `schema.docs.graphql`, followed by manual reading, listed 72 write routes and 48 mutations. It is a starting list rather than a proof of completeness. **This table refused 94 of the 120 on each flavour, and 26 reach,** as the list predicted. The 5 environment secret and variable routes reach, `POST .../issues/{n}/suggestions/{id}/approve` reaches, and `createEnvironment`, `pinEnvironment`, and `reorderEnvironment` are refused. `branches/a/b/protection`, `branches/main%2Fprotection`, and `POST .../branches` are refused, with `GET` passing. Writes to each credential tail were refused, with `GET` passing. `POST` and `PATCH .../environments/prod` and writes under `secrets` and `variables` pass, while `PUT` and `DELETE .../environments/prod` and writes under `deployment-branch-policies` and `deployment_protection_rules` are refused. Every REST rule below `{repo}` refused under both roots, on 30 concrete routes, 4 write methods, and 3 flavours. `PUT .../contents/pulls/7/merge` is refused by the content family and no longer by the merge rule. The stub proves forwarding only.

**Commands [measured].** 103 `gh` commands on Alpine and Debian:

| Preset | Forwarded | Denied refused | Other refused | Missed | No write |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Earlier smaller | 69 | 23 | 0 | 0 | 11 |
| **This table, GHES and github.com** | **63** | **23** | **6** | **0** | **11** |

The 6 are `gh issue develop`, `gh repo sync`, `gh repo deploy-key add`, `gh repo deploy-key delete`, `gh api -X PUT .../contents/...`, and `gh api -X DELETE .../branches/main/protection`. Outside the matrix, `gh ssh-key add`, `gh ssh-key delete`, and `gh gpg-key add` are refused, and `gh gpg-key delete` did not confirm, because the stub has no key to list, while its raw `DELETE` is refused. **`gh release upload` on GHES and `gh secret` and `gh variable` with `--env`, and their deletes, pass.** `writeForm` refused no `gh` or `glab` command in the matrix. Across 7 `gh` versions on both clients and both flavours, 12 rows each, 8 writes were forwarded, merge was refused, and 0 others were refused.

**Attack rows [measured].** The same 1,230 rows on each of GHES, github.com, and `ghe.com`: **1,069 refused, 160 reached with no denied action, 0 denied actions with the credential**, and 1 denied action with no credential through an opaque tunnel, which is refused when that host is bound to a generic rule set. With 280 added rows for the added mutations, all refused, the totals are 1,350, 160, 0, and 0 of 1,510. The follow-up's 4 denied actions were a `query` naming a denied field or spreading a `Mutation` fragment and `POST /repos/acme/widgets/graphql`, and the rules above closed them. No value reached a wrong host in the audit.

| Family | `gitlab-deny`, not run on any GitLab host |
| :-- | :-- |
| Review | `projects/*/merge_requests/*/approve`, `unapprove`, `reset_approvals`, and approval rule routes **[measured]** |
| Merge | `projects/*/merge_requests/*/merge`, `rebase`, `cancel_merge_when_pipeline_succeeds`, mutation `mergeRequestAccept`, and `denyBodyKeys`, at any depth and in a GraphQL variable **[measured]** |
| Content and ref | `projects/*/repository/commits`, `repository/files/**`, `repository/branches` writes, and mutations `commitCreate`, `branchCreate`, and `branchDelete` **[measured]** |
| Policy | `projects/*/protected_branches/**` writes **[measured]** |
| Credential | `/personal_access_tokens/self/rotate`, `/user/keys/**`, `/deploy_keys/**`, `projects/*/deploy_keys/**`, `projects/*/access_tokens/**`, and `projects/*/deploy_tokens/**` **[measured]**. |
| Text | `(?i)^\s*/(approve\|lgtm\|unapprove\|merge\|rebase\|submit_review\|target_branch\|run_pipeline)\b`, per line. It refused these in JSON, escaped, CR-separated, query, form, note, and GraphQL forms **[measured]**, also refused prose in a line starting `/merge the two lists` and a `/merge` inside a code block, and passed `/merge` after a non-ASCII space, because RE2's `\s` is ASCII, and `see /merge` and `/merge_requests/1`. The alias list is unverified, and whether GitLab executes a command through a given token is **[pending]** S17. |

GitLab has no route inventory like GitHub's. Of 67 `glab` and API rows, 8 were forwarded, 37 denied refused, 20 refused beyond the table's expected list, and 2 had no write **[measured]**. Its other evidence is Grape 3.3.5, an app built to match GitLab's routes under GitLab's pinned `grape ~> 3.2`, which routed `appr%6Fve`, a trailing slash, `//`, and `approve.json` to the approve handler **[measured]**. That is the framework rather than GitLab's whole application. A generic Rails 8.1.4 router routed a trailing slash, `//`, and `.json`, and answered 404 for `%65` **[measured]**. GitHub's stack is unknown.

**Selecting the flavour.** The flavour derives from a VCS pool's provider and host, with no stored field, and I8 reads it:

| Provider and host | Flavour | API host | `apiBase` | `restPrefix` | `graphqlPaths` | Uploads | `attach` |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| `github`, `github.com` | `github` | `api.github.com` | `https://api.github.com` | `/` | `/graphql` | `uploads.github.com`, which needs its own binding | `Authorization: token {{secret}}` |
| `github`, `<name>.ghe.com` | `github` | `api.<name>.ghe.com` | `https://api.<name>.ghe.com` | `/` | `/graphql` | The host `upload_url` names, which is owed | `Authorization: token {{secret}}` |
| `github`, any other host | `github-enterprise` | The host | `https://<host>/api/v3` | `/api/v3/` | `/api/graphql` | `/api/uploads/repos/*/*/releases/*/assets`, admitting a query string and a binary body **[measured]** | `Authorization: token {{secret}}` |
| `gitlab`, any host | `gitlab` | The host | `https://<host>/api/v4` | `/api/v4/` | `/api/graphql` | None | `Private-Token: {{secret}}` |

This covers GHES with no stored input, because every `provider: github` host outside `github.com` and `*.ghe.com` is GHES. §14's `vcs.github.write` scope still covers it, and §14 is unchanged. A tenant admin has no field to set, so she cannot satisfy `requiredPresets` with rules that match nothing. Resolution reads every VCS pool of the tenant, unfiltered by Environment and whether or not a guardrail has suspended it, and a write refuses two pools on one host whose flavours differ. `/api/graphql` is a listed path for both GHES and GitLab, so on a host no pool covers each preset carries the other's denied mutation names, and REST fails closed on the prefix **[measured]**, with 13 of the 36 names tried. A link provider's `kind` must agree with the host's flavour at write.

**On by default, resolved at I8.** A binding whose `requestRules` has no `preset` key, whatever its source, on the API host of a VCS pool, from the table above, resolves at I8 to that flavour's preset. It covers an App-sourced binding, a project token, and a `registered` type, because an App token with `pull_requests: write` can also approve. `preset: none` clears it explicitly. On a host no VCS pool covers, no preset applies unless the binding names one, and then its family and the host give the flavour: `github-deny` is `github` on `api.github.com` and `api.<name>.ghe.com`, and `github-enterprise` elsewhere, and `gitlab-deny` is `gitlab`. No field can select another. Resolution at I8 means a pool added later takes effect at the next `cacheUntil`.

**What the owner accepted.** Under `default: allow`, a new GitHub mutation that can approve or merge passes until the preset is updated. GitHub's schema went from 122 mutations (2021-01) to 278 (now), and in one reading about one a year needs only `pull_requests: write` and carries merge or review power **[source]**. The input-key and target checks are gone, so `updatePullRequest` with `baseRefName` or `state` passes. **Operations that exist today are in the preset's scope and are not part of this acceptance**, which the owner settled with the extended preset under decision 24.

**Keeping presets current.** A CI job diffs GitHub's public GraphQL schema and REST OpenAPI description, and GitLab's quick action page and fine-grained tables, on a schedule. It raises a preset change for review when a new name matches a family above. Its window is the schedule plus the review plus the time to the next `cacheUntil`.

### Who can change a preset

Under the owner's D1, a tenant can change it.

| Action | Who | Conditions |
| :-- | :-- | :-- |
| Set rules, or clear a preset with `preset: none` | `tenant-admin`, through EG-9's write path | An audit event names the old and new rules. Posture reports each binding's preset and any cleared preset. |
| Require a preset | `security-admin`, through a new SO-2 guardrail `requestRules.requiredPresets`, by host pattern with a minimum version | EG-9's check evaluates the resolved form, at a binding write and at a VCS pool write. A binding breaking it is refused, or `suspendedBy` when the guardrail arrives later, so a pin to an old version does not satisfy it. I8 refuses a resolved rule set lacking a required preset recorded on the binding. Relaxing it is a weakening under SO-2's delay. |
| Ship or update a preset | The platform, in the chart | A binding that sets `version` stays there, above any guardrail minimum, and others follow the latest. |

## UC-5: `delivery: file`

### Gate

A new per-tenant platform-admin grant, `userCredentialDelivery`, is its own risk class under the egress design's **Gates** paragraph, separate from `agentReadableDelivery`. That class holds tenant credentials, and this one holds a person's. Granting one grants nothing in the other. A tenant admin then opts in per binding by writing `delivery: file`, and the token's record must carry a ceiling of `file`, on the type or the provider. `security-admin` can forbid it through SO-2's `credentialDelivery.forbid`, in which `file` covers every `file` binding and `userCredential.file` covers this kind alone.

### Mechanics

This reuses the egress design's `file` mechanics.

- `/run/netagent/credentials/<name>/value` holds the raw value with no trailing newline, and `binding.json` holds the name, hosts, `attach`, `expiresAt`, and a version counter. `LENNY_CREDENTIALS_DIR` names the directory.
- `netagent` swaps the directory atomically, and the mode is 0444 in an emptyDir that only `netagent` writes and the runtime container mounts read-only.
- `projections` work as defined: `netrc`, from an `attach` Basic template as metadata, and a deployer's file templates.
- `netagent` gets the value on I8 under the current binding check: pod token, peer address, binding state, session user, and pod model, with the decision service as the second gate.
- `netagent` refreshes at `cacheUntil`, 300 seconds for this kind.

Environment variables stay never offered. That design's reasons hold: `/proc/<pid>/environ`, child inheritance, diagnostic reports, and no rotation.

### What `file` does not promise

| What it does not promise | What can be done | Status |
| :-- | :-- | :-- |
| **The token can leave through any allowed host.** An agent reads a file and sends it anywhere the policy allows, and an opaque tunnel hides that. | `lockEgressToHosts: true` limits the session's effective allow set, and every descendant's, to the binding's hosts and the base destinations, at the cost of everything else that session wanted to reach. It is refused on an `internet` pool, whose raw sockets bypass it, including at delegation, and a `file` binding there is reported in posture. **It covers egress alone:** a connector tool argument, a message to a child, and a workspace export leave through gateway channels, which `leakScan` alone reads. Bytes-out alerts under EG-12 detect volume and not a 40-byte token. | **[design]** |
| **`hosts` is enforceable only as a check against the allow set at write time.** `netagent` attaches nothing, so it never decides where the value goes. | The write rejects a host outside the type's hosts or the tenant's effective allow set, and `binding.json` carries the rest as a statement of intent. `lockEgressToHosts` is the enforcing form. | **[design]** |
| **Removing the file does not invalidate copies.** | Prefer a short `expiresAt`, a narrowing template, and `oauth2Link` with revocation. Rotation after exposure is the tenant's action. | **[design]** |
| **Lenny cannot revoke a `registered` token at the provider.** It knows nothing about the provider. | See "Exposure records". The tenant's backend is the party that can. | **[design]** |
| **The agent can do whatever the token permits.** Rules do not apply. | Choose a token whose own scope fits. The type's documentation states this. | Stated |
| **The token can reach transcripts, LLM traffic, workspace exports, checkpoints, and forensic evidence.** Anything the agent prints or stores carries it. | "Leak scanning" below narrows it. Nothing removes it. A session holding a `file` binding is marked `agentHeldCredential`, which posture reports, and which a tenant can use to shorten that session's retention. | **[design]** |

### Exposure records

§25.5's events are best effort **[source]**, so they cannot carry this. Each generation of a value released under `file`, of either source, writes a durable exposure row in the Token Service's store, with the `credentialRef` or `linkRef`, `generation`, the writer's `tokenId` when there is one, the type, and the session. The row is opened at release, and it becomes actionable at the session's end or at the next rotation. A §25.5 `user_credential.exposure_ended` event notifies the tenant, and `GET /v1/admin/user-credential-exposures` lists rows not yet acknowledged, so a missed event is recoverable. `POST .../{id}/ack` closes one. An alert fires on rows left open past a tenant-set age. A backend can also register one short-lived token per session, from its handling of session events. Erasure pseudonymizes an open row and keeps it until it is acknowledged, because a person's departure does not end a live token.

### Leak scanning (UC-6)

This is optional, per binding (`leakScan: on`), and absent under `inject`, whose value the pod does not hold.

- The Token Service gives the gateway replica coordinating a session that session's current value over mTLS, checked against the session's coordinator record, the record digest, and a ceiling of `file`, with an audit event per release. It is held in memory for the session. **A replica can acquire any session's lease through REG-COORDLEASE, so that check does not bound its reach:** a compromised replica can obtain the value of any `leakScan` session in the deployment, across tenants. That is the cost of the option.
- A new built-in interceptor scans `PostAgentOutput`, `PreToolResult`, `PreExportMaterialization`, `PreMessageDelivery`, `PreDelegation`, `PreConnectorRequest`, and proxy-mode `PreLLMRequest` for the value and its base64 and URL-encoded forms, and it redacts and raises an event.
- FE-6's collector and adapter do not receive these values, because the collector is a node-level process shared across tenants. Its structural and entropy layers apply as stated there.

Its limits are stated: a direct-mode LLM call, an opaque tunnel, a re-encoded or split value, and checkpoints are not scanned. A checkpoint can hold a copy, which is why §12.9 applies and why `agentHeldCredential` exists.

## Interactions with VCS pools

### Git and hosts

The owner settled the git path as T, and `egress-and-git-design.md` states it. **Only the T column below is normative. L, the prior loopback rewrite, and P, an `http://` rewrite with a loopback proxy, stay as the evidence behind that decision.** Rows are **[measured]** with an agent that owns its config and environment, on a stub running stock git, unless marked. **The stub proves forwarding only.**

| A token the runtime holds, with no flag | T, normative | L, evidence | P, evidence |
| :-- | :-- | :-- | :-- |
| `git push` on a covered host by helper, header, or `gh auth git-credential` | Dropped, the mint is used, and patterns hold | Replaced by the mint in the header rows. The helper rows did not run on L. | Same as T on the rewrite path |
| The same with a URL token | Replaced by the mint | The request fails with no token at the origin | Replaced by the mint |
| Rewrite or `http.proxy` config removed, covered host | Enforced, as the rows above | Refused with an API binding. With none, an opaque tunnel with the held token reaching the origin. | Refused with an API binding. With none, **an opaque tunnel: the token reached the host, and a push to `main` was accepted** |
| A host no pool, route, or binding covers | **The token reaches the host in an opaque tunnel.** The run's push ended non-fast-forward, so a push completing is inferred from P. | Dropped on the rewrite path | Dropped on the rewrite path |
| `gh` on the API host, no binding | The token passes with no rule, and `pr merge` reaches the stub | Did not run. `gh` stops at its remote check. | As T |

**On a covered host with no flag, a held token does not reach the host for git:** the push acts as the minted identity. Rules apply where a binding names them, so a `file` token on an API host has no rule. The prototype's filter-only binding refused `pr merge` for such a token, and this design offers no such binding, because the one-binding rule keeps `file` and `inject` off one host.

**`allowRuntimeHeldCredentials`.** UC-5 owns it: the pool field, the refusal below, and the attach rule here. A session qualifies on a covered host when it names a `file` binding the flag applies to, whose type hosts include that host, whatever the release or suspension state, and the gateway records those hosts on the claim at bind, beside `retireOnRelease`, so a type deleted later does not change them. A binding the owner's rule later suspends, or a tenant deletes, still counts for the running session that named it, and a plan naming a suspended `file` binding is rejected at create with `422 USER_CREDENTIAL_BINDING_SUSPENDED`. **A qualifying session gets the current value `netagent` holds for that host on every git request, whatever the client sent, in the form the mint uses on that provider, and no mint is used for that host. `netagent` asks I8 before it concludes there is none **[design]**, since the run held its state in a file and made 0 Token Service requests. With no current value, nothing attaches, and the host answers 401, so no state silently pushes as the App.** A Token Service outage gives the stated 503 rather than a 401 **[design]**. On I8 the release names a `handling` value, so an image without this rule refuses the credential. The Token Service refuses a mint to `netagent` for that session and host. The mint refusal does not apply to a read mint in the provisional state, which grants no write token, so a rebuild's clone reads as the App, as a mirror route does. It does not refuse the mirror's own I8 request, so a read on a mirror route is served from the mirror's store under its App mint, with `netagent`'s service account token at the mirror, and the session's one identity holds for what `netagent` sends to the host. The agent gains nothing, since it already holds the value. A delegated child with no lease entry names no binding, so it does not qualify, and its push acts as the App, under the identity rules of `user-git-identity-design.md`. Path enforcement, `allowedRefPatterns`, the push option refusal, and `git.operation` stay. A pool flag changes no other session. `allowRuntimeHeldCredentials` defaults to `false`, and a `security-admin` can forbid it.

**Evidence for the attach form [measured],** on the stub, which proves forwarding only. With all conditions holding, the origin saw the current value on clone and on push, on GHES, whatever the client sent: nothing, a helper, a URL token, a header, or a different token. A client with no helper cloned. A rotated value took effect with no action by the runtime, and a different token appeared 0 times at the origin. A push to `main`, a mixed push, a deletion, a push option, a push outside the set, a push to a read-only repository, and a read outside the set were refused, on GHES and github.com. An LFS push and smudge through a separate store host passed with the store seeing no credential, and an out-of-scope redirect got 403. A reflected value was masked. In a Token Service stand-in, a qualifying clone and push made 0 requests, a direct mint got 403, an image without the rule got 403 so git failed, and an image older than the `handling` value refused before asking. On a mirror route the fetch carried `MIRROR-SA`, and the push advert carried the current value. **A suspended binding, a cleared flag, no release, and two pools with the flag on one each gave no credential and a 401, on all 5 client forms on clone and on push, with 0 Token Service requests and 0 pushes succeeding.** A session that released but named nothing, the delegated child's case, took the mint. A push to `main` in those states stops at the advert's 401, so the ref check is not reached. On a mirror route when suspended, a read carried `MIRROR-SA`, and a push got 401 at the host. An earlier run had these states fall to the mint and push as the App.

**The owner's rule on a `file` binding beside a pool.** A `file` binding whose type hosts include a host a tenant pool or git route covers is refused unless every pool covering that host, read unfiltered by Environment, sets the flag, whatever the pool's mode. This closes the case where a pool with `userAuthorization: off` accepts the binding and the push silently acts as the App.

| Case | Outcome |
| :-- | :-- |
| A pool without the flag is added later on that host | The binding is suspended at the pool write, naming the pool. A running session that named it gets a 401 on git rather than the App. |
| The flag is cleared later | The same. A write removing the pool or setting the flag again clears the mark it set, as `security-oversight-design.md` states for guardrails. A plan naming a suspended `file` binding is rejected at create with `422 USER_CREDENTIAL_BINDING_SUSPENDED`. |
| A host covered by a git route alone, with no pool | The binding is admitted, because no pool can set the flag. The session does not qualify, the held token is dropped on the git path, and a push is refused, since no pool covers it. |
| A platform warm-up asset on the host | No effect. An asset's host is covered for a warm pod until bind, and after bind only the tenant's pools and routes count, except on a `restricted` pool with `gitAccess`, where every host of the formula stays covered for that pool's pods **[design]**, because a host held as data ran and the computed formula has not. This rule reads tenant pools and routes alone. |

### Other hosts

- **Stored token on a covered git host.** The egress design refuses a stored token binding, under either delivery, on a git host a VCS pool covers, because the token would reach that host's REST write routes with no path enforcement. A `registered` type under `inject` is a stored token, so the refusal applies, evaluated on type hosts. The one exception is a `userCredential` binding under `file` when the flag applies.
- **A type a link uses.** A `file` binding is refused on a type whose link provider `prAuthoring` or a UG-2 check uses, unless the flag applies and the record's ceiling is `file`.
- **Pinned hosts.** `credentialPinned` strips other credentials, so a `file` token sent to a pinned host breaks: `netagent` removes it. A `file` binding whose type hosts include a pinned host is refused at write, and a pin added later suspends that binding, naming the pin, rather than leaving one that silently fails. A session the flag applies to is excepted from a `required` pool's own pin, on that host alone, once it has received a release, and suspension ends the exception.
- **A host with an `inject` binding and no pool.** `netagent` classifies smart HTTP on every terminated host, so git to it carries no credential, the secret stays off the git request, and a push is refused **[measured]**. A `file` binding's host is not terminated for that reason, so with no pool or route it stays opaque.
- **A host with an `inject` binding.** `netagent` overwrites the `attach` header, and its credential list strips others, so a runtime token sent there is replaced. The one-binding rule refuses this at bind.
- **Hosts under a `required` pool.** See below.

### What a `required` pool still gives

A tenant-defined type on GitHub hosts is possible under the opt-in. It does not bypass the UG-2 check on App tokens, and it does sit beside it. `prAuthoring: user` requires `userAuthorization: required`, as `user-git-identity-design.md` states.

**The rule is by host, for every session of the tenant.** On the API host of a `required` pool, the only permitted bindings under `inject` are an App-sourced binding, which the check governs, and the pool's link, whose provider is the one on that host. A stored secret and any other `userCredential` type have no check, so they are refused at write, and setting `required` on a pool suspends one that exists, as a pin does.

| A `userCredential` binding on that host, with type hosts evaluated | Under `inject` | Under `file` |
| :-- | :-- | :-- |
| On the tenant, named by no session here | Refused at write, unless it is the pool's own link. | Nothing changes for a session that does not name it. UP-4, UP-6, and the check hold. |
| The pool's own link | The binding `prAuthoring: user` attaches. Its token is limited by its template, and `requestRules` apply. | Refused unless the flag applies. |
| Any other type on that host | Refused. | Refused unless the flag applies. |
| Named by a session under `allowRuntimeHeldCredentials: true`, on a pool with no `prAuthoring: user`, with no App binding on the API host for that session | Refused. | **UP-4 still holds for App tokens: no App token without a verdict. UP-9 does not hold, UR-2 does not hold for what the agent's token reaches, UR-4 does not hold, and UR-6 does not hold, since the token leaves every Lenny control.** Posture reports the pool, and the session is marked. Other sessions on the pool are unchanged. |
| The same, on a pool with `prAuthoring: user`, or with an App binding on the API host | Refused. | Refused at bind by the one-binding rule. |

A tenant that needs `required` to mean what it says leaves the flag off and takes SO-2's guardrail to forbid it.

### Tokens smuggled through `inlineFile` or `env`

A client can place its own token in a session: `inlineFile` content is not inspected, the `env` blocklist passes a name matching no pattern, such as `MYPAT` **[source]**, and text in a message reaches the agent too. Input-side checks cannot close this, because they cannot recognize a token. Under a `required` pool this is a gap, since an agent using such a token bypasses the check.

**The rule, which the owner settled under `user-git-identity-design.md` decision 40:** when any `required` pool covers a host, that host and the kind's API hosts are `credentialPinned` for every session of the tenant, once EG-12 has landed, because a token reaching that host reaches the pool's repositories whichever pool the session is on. The pin source reads every VCS pool of the tenant, unfiltered by Environment and whether or not a guardrail has suspended it. UG-2 carries the wiring, and EG-12's pin takes a VCS pool as a source. A pinned host strips other credentials and refuses a host with no binding, so a smuggled token cannot be used there. A pool can opt out with `pinHosts: false`, which posture reports. A session holding a `file` binding the flag applies to is excepted, and no other session is. An `internet` pool's raw sockets bypass `netagent`, so its sessions are outside the pin, and posture reports that. Until EG-12 lands, under the opt-out, or on an `internet` pool, this is a stated residual: a smuggled token reaches an unpinned host through an opaque tunnel. `user-git-identity-design.md` states the same rule under its decision 40. Other hosts stay outside the rule, since the mechanism is not about them.

### Connectors

The owner settled connectors under `user-git-identity-design.md` decision 3: refused only when a pool on that host is `required`. This design has no connector behaviour of its own: a `userCredential` is never delivered on the gateway path, except the session-bound value of "Leak scanning".

## Configuration by scope

| Setting | Scope | Who sets it | Default |
| :-- | :-- | :-- | :-- |
| `userCredentialDelivery` grant | Per tenant | `platform-admin` | Absent |
| `userCredentialTypes` | Tenant, with optional TS-1 `environments` | `tenant-admin` | None |
| `hosts`, `attach`, and the delivery ceiling, on a `registered` type | Type | `tenant-admin`, and a change makes its rows unusable | None, none, and `inject`. A write must set `hosts`, and an `inject` binding needs `attach`. |
| `linkProviderDrainSeconds` | Deployment | Platform (Helm) | 86,400 |
| `registeredBy`, `maxBytes`, `requireExpiresAt`, `maxLifetimeSeconds` | Type | `tenant-admin` | `[self, service]`, 16 KiB, `false`, unset |
| `maxBytes` ceiling | Deployment | Platform (Helm) | 64 KiB |
| `linkProviders`, `kind: oauth2` | Tenant | `tenant-admin` | None. A write must set `attach`. |
| `delivery` on a link provider | Link provider | `tenant-admin`, and a change makes its links unusable | `none` |
| `apiHosts`, `userInfoEndpoint`, `idField`, and `clientSecret` | Link provider | `tenant-admin` | None. A `kind: oauth2` write must set them. |
| `tokenTrust.allowedRecords` | Tenant security record | `security-admin`, under SO-2's delay | Absent |
| `identityBinding` | Link provider | `tenant-admin`, and SO-2 can forbid `unverified` | None. A write must set it. |
| `narrowing`, and its `permissions` | Link provider | `tenant-admin`. Shipped templates alone on a VCS kind, with `permissions` as the one override. | None on `kind: oauth2`. Required on a VCS kind that delivers. |
| `delivery`, `required`, `lockEgressToHosts`, `leakScan` | Binding | `tenant-admin` | `inject`, `true`, `false`, off |
| `requestRules` | Binding, `inject` only | `tenant-admin`, and SO-2 can require a preset | The VCS pool's flavour's preset at I8, unless `preset: none`, or none on a host no pool covers |
| Presets | Deployment | Platform (chart) | Shipped |
| Rule bounds, and the routing override header list | Helm, with data within ceilings | Platform | As under UC-4 |
| `allowRuntimeHeldCredentials`, `pinHosts` | VCS pool | `tenant-admin`, and SO-2 can forbid the first | `false`, `true` under `required` |
| `userCredentialBindings` | Delegation lease | Client, within the parent's | Empty |
| `manage_user_credentials` | Role | `tenant-admin` | Held by `tenant-admin` |
| `credentialDelivery.forbid: userCredential.file` | Tenant security record | `security-admin` | Absent |
| `requestRules.requiredPresets`, with a minimum version | Tenant security record | `security-admin` | Absent |

Every non-spec default is operator-tunable and documented as such.

## Worked examples

### A Jira token as a file

The platform admin grants `acme` `userCredentialDelivery`. Carol defines type `jira`, `source: registered`, `hosts: [acme.atlassian.net]`, ceiling `delivery: file`, `registeredBy: [service]`, with `requireExpiresAt: true`, and a binding with the owner's record and `delivery: file`.

1. `acme`'s backend, under a `credential-writer` role, calls `PUT /v1/admin/users/alice@acme.com/credentials/jira`, with a token that expires tonight and a `tokenId`. An audit event names the writer. **[design]**
2. Alice creates a session naming the binding, on a pool with one session per pod. The gateway checks the pod and that the caller is not an impersonator, and it writes `retireOnRelease` on the claim. **[design]**
3. `netagent` asks I8. The Token Service reads Alice from the session row, checks the claim's mark, reads the row, compares its trust digest, checks expiry, asks I3, and answers. `credential.mint` and `user_credential.delivered` are written, and an exposure row opens. **[design]**
4. `netagent` writes `/run/netagent/credentials/jira/value` and `binding.json`. The runtime reads it with its own client. **[design]**, on mechanics **[measured]** in the egress rounds.
5. The backend rotates at noon. The binding change reaches `netagent`, the file swaps, and the morning generation's exposure row becomes actionable. **[design]**
6. An injected agent sends the token to an allowed host it should not have. Lenny does not see this. Under `lockEgressToHosts` the CONNECT is refused. At session end, `acme`'s backend receives `user_credential.exposure_ended`, or lists the open rows, and it revokes each generation at Atlassian by its `tokenId`. **[design]**

### The same token under `inject`

Carol changes the binding's `delivery` to `inject` and lowers the type's ceiling, so the backend writes again, and the type's `attach` is `Authorization: Bearer {{secret}}`. No grant is needed. The agent never reads the token, the tool sends a placeholder, and `netagent` terminates TLS for `acme.atlassian.net`, as the egress design describes. Carol adds `requestRules` with `default: allow` and a deny rule for `DELETE /rest/api/*/issue/*`, and `netagent` refuses that one with a decision identifier. With no preset, a Jira attachment upload still streams, and a write with a query string still passes. **[design]**

### A generic OAuth2 link

Initech defines `salesforce` on a link provider with `kind: oauth2` and `delivery: inject`. A session naming the binding with no link gets `USER_LINK_REQUIRED` and a `linkUrl`, and the UG-1 flow runs, with a confirmation page naming Erin's Salesforce login and the delivery. No template exists, so `netagent` holds Erin's access token with her full reach, as the write response said. The Token Service refreshes under a row lease. An admin at `initech` edits `tokenEndpoint`, and the sealed `clientSecret` is unusable until a write re-supplies it. Every link's digest now differs, and nothing is sent to either endpoint. Erin's next session offers a relink, and the page before the redirect names every endpoint host and says they changed. Erasure deletes the row and calls `revocationEndpoint` when the record matches. **[design]**

### A GitHub token on a `required` pool

Pool `github.com/acme/` is `required`, with no `prAuthoring: user`. Carol adds type `gh-pat`, `registered`, `hosts: [github.com, api.github.com]`, and a `file` binding. The write is refused, because the pool lacks `allowRuntimeHeldCredentials`. Declaring `hosts: [docs.acme.com]` passes that check, since a `registered` type can lie, and the agent then holds the token anyway. The enforcing control on that path is the pin on `github.com`, which strips it. She sets the flag instead, and posture reports that UP-9, UR-2, UR-4, and UR-6 no longer hold for sessions naming this binding. Alice's agent runs `git push` with the token:

- With no opt-in, the mint replaces it on a covered host **[measured]**, under "Git and hosts".
- With the opt-in, `netagent` attaches the current value it holds on every git request and keeps path enforcement and `git.operation`, with no mint for that host **[measured]**.
- App mints stay checked. A session that does not name the binding keeps the pin, and the mint replaces any token it sends.

### Refusals

- An operator calls `POST /v1/sessions` under an impersonation bearer for Alice: refused, as today.
- An operator uses `lenny/send_message`, `POST /v1/responses`, `upload`, or `start` on Alice's session under an impersonation bearer, or writes to her memory on any session: refused with `impersonation_user_credential`.
- An operator calls `PUT /v1/credentials/{ref}` under that bearer: refused.
- `acme` writes a link provider, and no field exists to name `globex`'s client secret.
- A session on a pool with `recycle.enabled: true` names the binding: refused with `USER_CREDENTIAL_UNSUPPORTED_POD`.
- A service caller names a `required: true` binding: refused with `USER_CREDENTIAL_NOT_FOUND`.
- Alice's agent delegates to a child with no `userCredentialBindings`: the child has no file, and its requests carry no token.
- `POST /repos/a/b/pulls/1/r%65views`, `.../reviews.json`, `POST /graphq%6C`, and `POST /graphql/x` on a host under `github-deny`: refused **[measured]**, and the stub proves forwarding only. `.../reviews.x` and `POST /repos/a/b/graphql`: refused **[measured]**.
- Carol deletes type `jira` and recreates it with another host: the new type has a new `typeId`, and it holds no rows.
- Carol raises a type's ceiling to `file`: stored rows are unusable until a new write.

## Changes requested of other designs

### `egress-and-git-design.md`

Applied in its revision 51, as hook rows under "Reserved for other designs" or in the text they govern: the typed `secret` union with `userCredential`, unknown kinds read as absent, the `userCredentialDelivery` gate, the P2 and P3 exceptions, the request inspection stage with `requestRules` and replacement, `handling`, `cacheUntil`, the one-binding rule on type hosts across deliveries, `scope: poolAttach`, the decision service honouring a link provider's endpoints on the mint path, the delegated child exception, `lockEgressToHosts` in the session formulas and on descendants, `caller_type` from `pkg/binding`, a VCS pool as a source of pins, `suspendedBy` naming a gate, a pin, or a VCS pool, a refusal of differing flavours on one host, an expected route and CA on I8 with the route asserted on the CONNECT and in the level 2 contract, a `handling` value and a mint refusal for a qualifying `file` session, and no CA or `attach` on a `userCredential` binding. Its git path text now states T, including what happens to a held token on covered and uncovered hosts.

### `user-git-identity-design.md`

Applied in its revision 11: links keyed on the provider's name, `prAuthoring` attach, the preset model, and the token trust record in its flow. The owner settled decision 3.

### Others

| Design | What it says now |
| :-- | :-- |
| `security-oversight-design.md` | The `userCredential.file` value of `credentialDelivery.forbid`, `requestRules.requiredPresets` with a minimum version, `tokenTrust.allowedRecords`, `allowRuntimeHeldCredentials`, oversight of the grant and of preset changes, `followsEntryState` on shipped templates, and posture fields. |
| `forensics-evidence-preservation-design.md` | Delivered tokens in evidence, `quarantined` stopping release on I8, the limits of rotation, and volumes holding a file. |
| `tenant-scoping-identity-design.md` | Types and link providers scoped by Environment, delegation, `poolAttach`, and user lifecycle. |
| `tenant-outbound-connector-design.md` | An OAuth2 link to a private host. |

## Dependencies

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| UC-1 (types, `registered`, the token trust record) | EG-9 | The single write path. It builds the digest format and `token_trust_records`, which UG-1 uses. It can land with no delivery. Closes F-4.9.46 and the `/v1/credentials` half of F-13.3.26. |
| UC-2 (`oauth2Link`) | UC-1, UG-1, EG-2's mint path, and OC-1 and OC-2 for a private host | The link flow, and the route to a token endpoint. |
| UC-3 (`userCredential` binding, `inject`) | UC-1, EG-4, EG-2 with the requests above, and TS-4 for the invalidation level | Bindings, I8, and `pkg/binding`. Addresses the session input half of F-13.3.26. It also builds the session input check and `retireOnRelease`, which UG-2 uses. |
| UC-4 (request rules and presets) | UC-3, and the EG-4 stage | The stage. |
| UC-5 (`file`) | UC-3, and EG-4's `file` delivery | The mechanics, and `allowRuntimeHeldCredentials` with its refusal and attach rule. It needs the grant under EG-9. |
| UC-6 (leak scanning) | UC-5 | Optional. |

No `EG-` proposal depends on a `UC-` proposal, and the hooks sit inside EG-1, EG-2, EG-3, EG-4, EG-7, EG-9, and EG-12. UG-1 depends on UC-1 for the digest format, UG-2 depends on UC-3, and UG-3 depends on UC-2, UC-3, and UC-4. UC-2 depends on UG-1, which keys on a provider name from the start. SO-2's new guardrails depend on UC-1 for `tokenTrust`, with UG-1 for link providers, UC-4 for `requestRules`, and UC-5 for `userCredential.file` and `userAuthorization.forbidRuntimeHeld`, TS-1 scopes the objects here when both have landed, and neither waits for the other.

## Specification sections affected

- [§4.2 Session Manager](../spec/04_system-components.md#42-session-manager): `caller_type` on the session row, copied to a child.
- [§4.3 Token Service](../spec/04_system-components.md#43-token-service): a user credential category with its table and key, the token trust record, and "Pods never hold downstream OAuth tokens" gains the stated exceptions.
- [§4.9 Pre-Authorized Credential Flow](../spec/04_system-components.md#pre-authorized-credential-flow): the `type` field, the providers with no delivery refused, the admin write route, the impersonation refusal, and the different `DELETE` semantics on a type row.
- [§4.8 Gateway Policy Engine](../spec/04_system-components.md#48-gateway-policy-engine): the built-in scan interceptor.
- [§5.2 Pool Configuration and Execution Modes](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): the refused pods, and `retireOnRelease`.
- [§8.3 Delegation Policy and Lease](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease): `userCredentialBindings`.
- [§10.2 Authentication](../spec/10_gateway-internals.md#102-authentication): `manage_user_credentials`, the permission matrix, and the session input check.
- §9.4: memory writes refuse an impersonation bearer.
- [§11.4 User Invalidation](../spec/11_policy-and-controls.md#114-user-invalidation): stopping a user's credentials at each level.
- [§12.8 Compliance Interfaces](../spec/12_storage-architecture.md#128-compliance-interfaces) and [§12.9 Data Classification](../spec/12_storage-architecture.md#129-data-classification): the new columns, the exposure rows, and `agentHeldCredential`.
- [§13.3 Credential Flow](../spec/13_security-model.md#133-credential-flow): "Pod never receives" gains the stated exception, and session input routes refuse an impersonation bearer on affected sessions.
- [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api): the routes, and the wire names §4.9 and the code disagree on. [§16.5](../spec/16_observability.md#165-alerting-rules-and-slos) and [§16.7](../spec/16_observability.md#167-section-25-audit-events): alerts and events.

## Documentation

- An operator page on the grant, stating every row of "What `file` does not promise" in those words, for the platform admin deciding on it.
- A tenant admin page on types, hosts, sources, bindings, request rules, and presets, stating that rules apply under `inject` alone, that no template means full reach, and that any change to a token trust record makes stored tokens unusable.
- A backend integration page: the write route, the role, expiry, rotation, and exposure records.
- A runtime author section on the file layout, and on what a refusal looks like.
- A preset reference generated from the data, and doc tests pinning error codes to their pages.

## Tests

Every test carries a `// spec:` annotation once the specification sections land.

### Tier 0: static

Preset and template data pass schema checks. A lint refuses a type row's secret outside the Token Service's read path. A test fails when a route on any mounted mux, an MCP session tool, or a registered adapter prefix declares no impersonation status.

### Tier 1: unit

- Types: name rules, reserved names, an unknown source read as absent, exact canonical hosts, and a new `typeId` on recreate.
- The token trust record: each field changes the digest, including `attach`, a route under any `environmentPolicies` entry, a move between scopes, a reorder, and a resolved address, while a mirror route, an egress group, a shipped template's `permissions`, a re-resolution, and a rotated secret value do not. A revert restores use. An edited digest, a moved ciphertext, a swapped role, a moved Environment, an encoding collision, a rewritten sealed field, a copied cache entry, and content not hashing to the sealed digest each fail.
- Registration: each permission, owner, type, size, encoding, and expiry path, and each refused subject, including a security principal, a service, and an erased subject.
- Rule evaluation: each rule kind and default, tenant additions over a preset, path patterns, and each preset property per flavour.
- Everything under "What every rule set forces", on the spike's attack rows and new rows: `%` escapes in each position, trailing slashes, format suffixes, `/graphq%6C`, `/graphql/x`, `/api/v5/...`, GraphQL literals carrying `\n/merge` in arguments at any depth, variable defaults, and directive arguments, string-encoded `variables`, REST twins under both root forms, routing override headers, and `/repos/acme/graphql-tools` passing. `/graphql.json` and an uploads path with a suffix matching no admit, `merge.foo`, `merge.json.foo`, a dotted deny pattern at each dot, a `POST` to `/repos/a/b/graphql`, any method on a suffixed listed path such as `/graphql.json`, with `PATCH` and `DELETE` on `/repos/acme/graphql`, a label named `graphql`, and `PUT .../contents/graphql` passing, a `query` with a `Mutation` fragment or a denied field, `/api/v3/repos/...` under a root flavour, and a wrong family's denied mutation names and REST writes refused. `gh secret set --env` and `gh variable set --env` pass, and `gh release upload` passes on GHES.
- A generic rule on a Jira stand-in: a multipart write and a write with a query string pass.
- Template rendering, with a URL off the recorded origins refused and a failure never falling back to the parent.
- Header stripping, with `Time-Zone` forwarded and `Cookie` removed.
- A cache key always includes the row or `link_ref` and generation.

### Tier 2: component

The tests run pod model validators against envtest, `retireOnRelease` honoured after a pool edit in both directions on each release path (`podsession.Binder.Release`, `Server.ReleaseSuspendedPod`, `PodExecutor.Release`, and the scrub reporter's seams), I8 refusing a release with no mark, and suspension when a pin or guardrail arrives.

### Tier 3: contract

I8 with `handling`, the expected route and CA, resolved rules, and a `file` value, including an old `netagent` stand-in refusing an unknown handling. `gh` and `glab` recorded traffic replayed under presets on each flavour, with `glab`'s `%2F` paths passing and a refused mutation failing on GHES paths.

### Tier 4: integration

- A backend write through to a file, with rotation, revocation, expiry, and deletion each reaching the pod.
- An impersonation bearer on every mounted route, on `lenny/send_message`, `lenny/memory_write`, `/v1/responses`, an `inReplyTo` reply, a message held for an unbound session, a child whose parent is affected, and `/v1/credentials` writes, including memory writes and deletes on an unaffected session.
- A service and an `agent` session under each `required` value, and a human with no token.
- Delegation with and without the lease naming the binding, including under `poolAttach`.
- An OAuth2 provider stand-in: link, refresh race, revocation, a hostile redirect, an oversized response, no refresh token, and no `expires_in`.
- A change to each trust record field, for both sources, including a route and a CA on an unchanged host: the token is unusable at I8 and at each mint, and a stand-in on each endpoint receives no old token. A route edit after a comparison: the decision service refuses the asserted route. An App or project token mint on the same host succeeds with no assertion and no refusal. **The Initech topology, a git route alone to a registered private endpoint, and the connector topology, an egress route alone with the git route `direct`, each work** for an API request, a git request under the flag, and a link provider call, each naming the list it matched. A host with both lists gives each class its own route, and a cached decision for one list does not answer the other. A route edit stops a pooled upstream connection. A provider in `deleting` drains, refuses a recreate, stops on a later route or CA, and ends each row at the bound with an audit event and a notification.
- The page before the redirect, and a sealed `clientSecret` unusable after a record change and restored by a revert.
- `tokenTrust.allowedRecords` refusing or suspending an unlisted record.
- Each row of "What a `required` pool still gives", and two pools on one host.
- Exposure rows across rotation, and a missed event recovered through the read route.
- Audit and `credential.mint` for each delivery, with no secret in any record.

### Tier 5: end to end on Kind

The tier covers each refused pod, a runtime reading a file across a swap, and `inject` with rules through a client binary.

### Tier 6: end to end on cloud

The tier runs a hosted OAuth2 provider and a provider-issued token through both deliveries.

### Tier 7: load

The tests measure rule evaluation at the concurrency bound with bodies at their bounds, and a burst of rotations reaching pods.

### Tier 8: chaos

The tests cover Token Service loss with a file present, a lost binding push, a `netagent` restart, and Redis down at session end with the exposure row surviving.

### Tier 9: security

- **`acme` attempts to reach `globex`'s client secret** by every field and by a row with a forged tenant, and the read-time check refuses it. A template, an endpoint, a route, a CA, a delivery flip, and a recreated type each try to send a stored token elsewhere.
- A gateway identity cannot decrypt a type row on a cloud KMS.
- The attack table and bypass table under deny mode, and fuzzing of the rule evaluator.
- A cross-tenant probe on every object, and a token from another user.
- Each row under "Git and hosts", including a qualifying session where a rotated, different, or absent client credential gets the current value and never a mint, a suspended, unreleased, or flag-cleared session that gets a 401 and never a mint, a binding deleted mid-session the same, a plan naming a suspended `file` binding rejected at create, a refused mint to `netagent` with the mirror's own request served, a rebuild's read mint served in the provisional state with no write mint, a Token Service outage giving 503, and a delegated child with no lease entry.
- A leak scan with each encoded form, with the misses recorded.

### Tier 10: conformance

The runtime battery gains reading a rotated file.

### Tier 11: documentation

The tier checks error codes, alert runbooks, and the preset reference.

## Validation owed

The spike ran on a stub with stock git, and **the stub proves forwarding only**. No GitHub or GitLab host, permission check, or quick action ran.

| Item | Question | Decides | State |
| :-- | :-- | :-- | :-- |
| V1 | The attack table under deny mode, spellings, literals, REST twins, and override headers | The claims under UC-4 | **Closed on the stub [measured]** for this table, as "Evidence for `github-deny`" lists: 3,033 of 3,045 predicted rows, and 1,230 attack rows on each flavour with 0 denied actions with the credential. A later run matched 403 of 403 rows on the stem's direction, the carried names, the routing list, and the GitLab token tails, with the attack and command numbers unchanged. **Owed:** how GitHub, GHES, and GitLab decode each spelling, and the refusal of a suffixed listed GraphQL path on any method. |
| V2 | `gh` and `glab` under the presets | "`gh` works" | **Closed on the stub [measured].** The commands table, 7 versions with 0 other refusals, and `gh release upload` on GHES and the `--env` commands passing. `glab` passed `mr create`, `view`, `list`, `note`, `update`, and `close`, and `issue create`, with `%2F` paths. **Owed:** `gh release upload` on a GHES host, amd64, macOS, Windows, the .NET SDK image, and a `glab` branch with `%2F`. |
| V3 | Variable re-encoding on both flavours | Forwarding | Owed. Printing ran **[measured]**, and re-encoding has not. |
| V4 | The T column of "Git and hosts", and `allowRuntimeHeldCredentials` | The git path | Column **[measured]**, on a stub. The attach form and each 401 state are **[measured]**. A decision service, the fall-through between lists, and the Token Service's assertion have not run, and `netagent`'s assertion ran on a stand-in. A pin exception, a guardrail, and posture have no prototype. |
| V5 | S13 and S17 of `user-git-identity-design.md`, for `gitlab-fine-grained` and `gitlab-deny` | Those two data files | Owed, live |
| V6 | S1, S2, S5, and S15 there, for `github-scoped` and `github-deny` | The template, the budget, and whether the host's limit binds | Owed, live |
| V7 | A bot's view of `performed_via_github_app` | The recommendation for bot owners | Owed, live |
| V8 | `netagent` memory at the final bounds | The concurrency cap | Partly **[measured]**: `memory.peak` reached 41.7 MB under a 15,000 token limit. Not measured under the final bounds. A 40,000 node body checks in 10.5 ms and 7.5 MB. |
| V9 | Leak scanning's cost at the listed phases' rates | Whether UC-6 is on by default anywhere | Owed |

## Residual risks

- **`file` is what it says.** Everything under "What `file` does not promise" stands after every mitigation. The opt-in exists because the alternative for those cases is no mechanism at all, as the owner stated.
- **`file` beside a runtime edit.** A tenant admin able to change a runtime or setup commands can read a `file` token in a user's session.
- **Future grants.** A tenant admin who controls configuration can obtain a token a user grants after her edit, past the page, or a value an automated backend writes after it. Only `tokenTrust.allowedRecords` enforces against this, and it holds only when `security-admin` is a different person.
- **Names the customer operates.** Whoever controls the DNS, CA, or network of such a name can read tokens sent to it, and the record cannot see that.
- **A compromised replica.** It can forge session rows and bindings, so it can use any stored token at its record's hosts and read any `file`-ceiling value, and it can plant a token in any tenant, or write a provider row with no guardrail check for a future grant. It can resurrect a row after `DELETE`, `revoke`, or erasure from a copied ciphertext, and rewrite `state` and exposure acknowledgements. It cannot redirect a stored token. A role for the Token Service's tables alone, on a second DSN, would narrow this, and the design does not need it for the destination rule.
- **Input before a tree is affected.** An impersonation bearer's input to a tree passes when no session in it is yet affected, and a later child declaring a repository under a `required` pool inherits what that input did.
- **`_method` in a multipart body** passes on a generic rule set.
- **A `file`-ceiling token and a setup command.** A tenant admin flipping a binding within a ceiling of `file` can read a stored token with a setup command, which the settled position accepts.
- **A held token on an uncovered host.** It reaches that host in an opaque tunnel, and a push is not refused, as the egress design's **Uncovered git hosts** states.
- **A token that mints a credential.** Under `inject`, an agent can use an operation the token permits and the preset does not name to obtain a new credential, which the scrub and Lenny's revocation do not reach.
- **Full reach without a template.** With no narrowing template, a delivered token holds the user's whole reach on that provider.
- **`inject` with `default: allow`.** A new operation with approval or merge power passes until the preset is updated. The window is the schedule, the review, and `cacheUntil`.
- **Targets and inputs.** Without input-key and target checks, `updatePullRequest` with `baseRefName` or `state`, and any allowed mutation on someone else's pull request, pass where the token permits. Under `inject` the child, when a template exists, limits this to the session's repositories.
- **Compromised `netagent`.** It holds the token under either delivery and ignores rules. It holds no refresh token.
- **A token Lenny cannot attribute.** A `registered` token proves nothing about whose it is. A backend or tenant admin holding the permission can place a token under a user's name.
- **Hosts can lie.** A `registered` type's `hosts`, and a `kind: oauth2` provider's `apiHosts`, are statements Lenny cannot check. A type declaring an innocent host and holding a GitHub token passes the VCS, pin, preset, and SP11 checks on the label. `lockEgressToHosts` and `credentialPinned` on the host the token works on are the enforcing controls, and the second waits on EG-12.
- **A forwarded header** can carry meaning at a host this design knows nothing about.
- **Bots and CI.** Anything a token writes can trigger automation under that identity, as `user-git-identity-design.md` states.
- **A smuggled token** reaches an unpinned host, per "Tokens smuggled through `inlineFile` or `env`".
- **Leak scanning** lets a compromised replica obtain the value of any `leakScan` session across tenants, and it misses encoded and split values, direct-mode LLM traffic, opaque tunnels, and checkpoints.
- **Key separation.** Under `kms.provider: local`, one master key is mounted in the gateway and the Token Service, so a compromised replica can decrypt type rows there.
- **A `registered` token removed from Lenny** stays valid at the provider until the tenant acts on its exposure rows. A record change leaves old OAuth2 tokens valid at the provider until they expire, because sending them anywhere is what the rule forbids.
- **Presets are a deny list.** They fail open on what they do not name, and this was the owner's accepted cost for new operations. A REST twin missed today is a defect in the data.

## Decisions

| # | Decision | Recommendation | Cost of the recommendation | Alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Replace the allowlist with generic bindings and data rules | **Settled by the owner (D1).** Generic binding, optional rules as data, and the GitHub deny preset on by default. | A new mutation with merge or approval power passes until the preset is updated, and input-key and target checks are gone. | The allowlist, which failed both of the owner's conditions. |
| 2 | First-version sources | **Settled by the owner (D2).** `registered` and `oauth2Link`, with `endpoint` reserved. | A tenant wanting Lenny to fetch from its own endpoint at session time writes rows from its backend instead. | The endpoint now, which adds a tenant-reachable outbound call per session, and an SSRF surface. |
| 3 | Known-provider restriction | Extend it with a `type` field, and refuse the providers with no delivery | Two fields in place of one, and a change for anyone registering those providers today, of which there are none in the wild. | Replace it with free strings, which sends typos and new types through the LLM router. |
| 4 | What a tenant admin can obtain | **Settled by the owner.** Stored tokens: she cannot read one, under either source, through any change to the token trust record. Future grants: she can trick a user past the page into granting a new token to a host she runs, or re-point an automated backend, and no per-field rule closes that, because she configures the tenant. Planting stays accepted: she can write a token under a user's name. A tenant needing separation sets `tokenTrust.allowedRecords` under SO-2's delay. | The page and the sealed secret reduce the future-grant route and leave it open, and the page gives little signal on a route or CA change or under a new provider name. A name the customer operates sits outside the record. Every token breaks on any record change. The guardrail needs a separate `security-admin`, and every CA rotation needs an approval. | Hold all link provider and type writes to `security-admin` or the platform, which takes configuration away from the tenant admin. Or a permission outside the `tenant-admin` ceiling for writing tokens, which §10.2's custom-role ceiling does not admit today, and which addresses planting alone. |
| 5 | Row semantics on `DELETE` | Stops use on a type row, differing from the LLM path | Two meanings for one verb, by row kind. | One meaning: leave running sessions alone, which is unsafe for a token being removed. |
| 6 | The gate | `userCredentialDelivery` as its own class | Another grant to manage. | Reuse `agentReadableDelivery`, which conflates a tenant's token with a person's. |
| 7 | `file` near a VCS host | **Adopted by the owner.** Evaluate on type hosts, and refuse unless every pool covering the host sets `allowRuntimeHeldCredentials`, whatever its mode. A session the pool's attach reaches is refused at bind. | A tenant wanting a PAT there sets a flag that removes most of what `required` promised for sessions naming the binding, and the posture read says so. A `registered` type can lie. | Allow on an `off` pool, where the push silently acts as the App. Or never, which excludes GitHub from the mechanism, against the owner's direction. |
| 8 | Runtime-held credentials on the git path | A session naming a `file` binding the flag applies to gets the current value `netagent` holds for that host on every git request, whatever the client sent, and no mint is used to `netagent`. With no current value, nothing attaches, and the host answers 401. **[measured]**, including the suspended and no-release states. With no flag, a held token is dropped on a covered host and the mint is used **[measured]**. | What `netagent` sends to the host carries one identity, the file's, and a suspension stops git rather than falling to the App. Reads on a mirror route and a rebuild's clone come from an App mint. | The comparison form, where a rotated or different token silently pushes as the App, which the owner ruled out. Or a fall to the mint in the non-qualifying states, which ran and silently pushed as the App. |
| 9 | Smuggled tokens under `required` | **Settled by the owner, under `user-git-identity-design.md` decision 40.** Pin the host for every session of the tenant once EG-12 lands, with an opt-out, and a stated residual until then | An API host with no binding is refused, so a `required` pool without `prAuthoring` loses unauthenticated API reads unless it adds a read-only binding, and other pools on that host are pinned too. | Pin only that pool's sessions, which leaves a session on another pool holding a token that reaches these repositories. Or a stated residual alone. |
| 10 | Headers | Strip credential headers, refuse named routing overrides, and forward the rest | A header carrying meaning at the host passes, which is a residual. | The allowlist, which refused all of `gh` on any image with `/etc/localtime` **[measured]**. |
| 11 | Preset clearing | Only `preset: none` clears it, by `tenant-admin`, audited and in posture, and `security-admin` can require it with a minimum version | A tenant admin can remove the protection, and a customer without a security principal has nobody above that admin. | Platform-only clearing, which does not match the owner's "a tenant can change it". |
| 12 | Preset distribution | Resolved rules on I8, so an update needs no image | Rules travel on each I8 response, and a stale chart serves a stale preset. | In the `netagent` image, which ties a preset fix to a pod roll. |
| 13 | Delegation | Only when the lease names the binding, including under `poolAttach` | A runtime that delegates must say so, and an unaware runtime's child has no token. | Inherit, as other bindings do, which hands a person's token to every descendant, including one the parent knows nothing about. |
| 14 | Sessions with no user | `caller_type` on the session row, and a per-binding `required` | A new field, and a tenant must decide per binding. A service session cannot use a user token. | A service token per type, which is a tenant credential and already exists as a stored binding. |
| 15 | Leak scanning | Optional, per binding, with the value released to a session's coordinating replica | A compromised replica can obtain any `leakScan` session's value across tenants, because a replica can acquire any lease. It costs a scan per message, and it misses encodings. | None, which leaves detection to forensics after the fact. Or signed coordinator claims, which is a change to §10.1 for one option. |
| 16 | `lockEgressToHosts` | Optional, per binding, persisting to descendants, refused on `internet` pools and at delegation to one | A session under it reaches almost nothing else, including package registries, and its children inherit that. It covers egress alone. | None, which leaves `hosts` advisory in every case. |
| 17 | Client secret | A write-only value, stored under a tenant-scoped key, re-checked at read, and sealed with the record digest | The admin API takes a secret on a write, with §4.9's logging exclusions, and any record change needs the secret again unless reverted. | A reference with a platform-only registration, as pools have, which needs an operator ticket per provider. |
| 18 | Spelling | Adopt the spike's strict matcher: refuse `%` outside a declared segment on every method, drop trailing slashes and empty segments, compare the final segment with and without anything after its first `.`, forward the canonical path, refuse the unmatched `/api/*` and `/graphql*` families under a preset, refuse a `POST` with a final `graphql` segment, and refuse a suffixed listed GraphQL path on any method | A tool sending an unusual but valid spelling breaks. In the runs, no `gh` command did **[measured]**. | Decode and broaden the match, which missed `.json` suffixes and `/graphql/foo` **[measured]** and depends on how each host decodes. |
| 19 | Request forms | Preset properties | A tenant's generic rule has no query or body form limit unless it writes one. | Forced on any rule, which refuses multipart Jira writes. |
| 20 | Pod eligibility after an edit | `retireOnRelease` on the claim at bind, I8 refusing a release without it, and a pod with a prior session refused | A pod carrying this binding always retires, and a binding that a mid-session edit creates delivers nothing until a new session. | Stamp all pool policy on the pod, which is a larger change for one field. |
| 21 | Exposure | Durable rows for both sources and a read route, with the event as a notification | A store, a route, and an alert for tenants who may ignore them. | The event alone, which §25.5 says is best effort. |
| 22 | Delivery ceiling | On the type or provider, in the token trust record, and shown on the pages | A change of purpose interrupts every user. | No ceiling, which turns a link made for a check, or a value stored under `inject`, into a file when an admin edits a binding. |
| 23 | One record for trust | A token trust record with a digest sealed on each stored token and cached value, compared at I8 and at each provider call, with each call using the compared content and asserting its route, and one `user_act_required` state, replacing `relink_required`, `reregister_required`, origin generations, and per-field triggers | Any difference breaks every token under the record, including a narrowing, a CA rotation, a route move, a reorder, or one team's scoped route. | Direction per field, which is more rules, and three review rounds each found a field the rules missed. |
| 24 | Preset body | **Settled by the owner.** The extended preset, with both root forms and the `/repositories/{id}` alias. It refused 94 of 120 listed routes and mutations, by name search and manual reading **[measured]**. | It refuses `gh issue develop`, `gh repo sync`, `gh repo deploy-key add` and `delete`, `gh ssh-key add` and `delete`, `gh gpg-key add`, `gh api` contents writes, and `gh api` branch protection writes **[measured]**. | The earlier smaller preset (11 REST rules and 18 mutations), under which 94 of the 120 reach the stub, including `merge-async` and every protection write. |
| 25 | Where `registered` rows live | A Token Service table under a key only it can decrypt, as §4.3 reserves, with row identity and digest sealed as additional data | A second store beside `credentials`, and a write passed through to the Token Service. The key separation holds on a cloud KMS alone. It shares a Postgres role with the gateway, so a replica can still forge a session or plant a token. | The `credentials` table, which every gateway replica can decrypt. Or a grant-restricted role on a second DSN as well, which narrows forgery and is a deployment change. |
| 26 | API flavour | Derived from the VCS pool's provider and host, or from a preset's family and the host where no pool covers it, with no stored field or key. Revision 3's stored `apiFlavour` and revision 4's `flavour` key are removed. | A GHES host under an unusual name is GHES by default, and a GitLab under a relative URL root has its writes refused under a preset. | A stored `apiFlavour` or a `flavour` key, which let a tenant admin satisfy `requiredPresets` with rules that match nothing. Or a `github-enterprise` provider value, which changes §14's lease scopes and the mint's provider switch. |

## Review of revision 1

Two independent reviews read revision 1, one adversarial and one for consistency across documents. Each finding was verified against the text and the tree. "S" numbers are the security review and "H", "M", and "m" the sync review. Findings that concern other documents are recorded there too.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| S1, another tenant's client secret | Holds. No check on `clientSecretRef`, and `lenny-credentials` is shared. | Write-only `clientSecret` under a tenant-scoped key, re-checked at read, with a tier 9 test. |
| S2, provider edit sends tokens to the editor | Holds. | Relink on any token-bearing edit, origin binding, and type-limited `inject` hosts. The remaining `registered` route is in the threat table, and "Cannot read" is replaced. Widening re-registers rows, which is a smaller rule that narrows it further. |
| S3, the `file` gate checks a label | Holds. | Hosts on the type. All rules evaluate those. The lie is a residual, with the enforcing controls named. |
| S4 and M17, spelling | Holds as a design gap. How hosts decode is **[pending]**. | Fail closed on spelling, rather than broadening the match. |
| S5, impersonation covers three routes | Holds. `route_table.go` confirms it, and `lenny/send_message` is separate. | Default-deny gate, exempt list, and route-table test. |
| S6, impersonation can write a token | Holds. `credentialserver` has no `callercheck`. | Refused on all `/v1/credentials` writes. `platform-admin` has no route here, so `act_sub` needs no recording. |
| S7 and M7, parent token delivered | Holds. | `delivery` attribute, template required on VCS kinds, and UP-1 and UP-10 corrected in the other document. |
| S8, pool flag changes other sessions | Holds. | Per session, conditioned on holding the binding. |
| S9, inline GraphQL literals | Holds. | `bodyPatterns` walk string and block string arguments. |
| S10, REST twins | Holds. Routes come from published API knowledge, and this round did not re-fetch them. Policy mutations confirmed in the schema **[source]**. | Preset names twins and policy families, and CI diffs the OpenAPI description. Owed under V1. |
| S11, H4, H5, and M16, presets and flavours | Holds. | Per-flavour paths, explicit rule kinds, default keyed on provider kind, resolved at I8. |
| S12, service sessions | Holds. No `caller_type` on the session row **[source]**. | Persist it, refuse writes naming a service subject, refuse `self` for a service caller, and state `agent`. |
| S13, pool edit | Holds. `poolRecyclePolicy` reads the current pool **[source]**. | `retireOnRelease` at bind, and a pod with a prior session refused. Smaller than stamping all policy. |
| S14, exposure event | Holds. | Durable rows, generation and `tokenId`, a read route, and an alert. |
| S15, leak scanning | Holds. | Bound to the coordinated session, audited, more phases, FE-6's collector excluded, and the reach stated. |
| S16, `kind: oauth2` confirmation | Holds. | `userInfoEndpoint` required. |
| S17, `internet` pools | Holds. | Lock refused there, and `file` reported in posture. |
| S18, headers | Holds. | Claim replaced by a residual, a routing list, and more stripped credentials. |
| S19 and M4, `inject` on a `required` API host | Holds. | One rule: only the pool's named link. |
| S20, token hash | Holds for `registered`. | Generation and a keyed hash. |
| S21, erasure gaps | Holds. | Pseudonymize `written_by`, and refuse a subject with a completed erasure job. |
| S22, preset pinning | Holds. | Guardrail minimum version. |
| S23, cache key | Holds. | Stated rule. |
| S24, callback | Holds. | Per-provider path and `iss`. |
| S25 and M10, `netrc` | Holds. | `attach` as format metadata under `file`. |
| H1, link key | Holds, in the UG text. | UG-1 keys on the provider name. |
| H2, egress refuses `file` | Holds, in egress. | The sentence is limited to `inject`. |
| H3, implicit attach | Holds. | `scope: poolAttach`, replacing a same-host App binding for a human session. |
| M1 to M3 | Hold. | One code per case, one reason, and one `tokenSource` list. |
| M5, GitLab option F | Holds. | Withdrawn, in UG. |
| M6, `mint-scoped` | Holds. | `followsEntryState` on the template. |
| M8, `file` beside the attach | Holds. | Rows restated, and refused with `prAuthoring: user`. |
| M9, security principal | Holds. | Added to "Named user". |
| M11, M13 | Hold, in egress. | Rows and the formula corrected. |
| M12, delegation under the attach | Holds. | Lease rule stated. |
| M14, flow differences | Holds. | Stated for `kind: oauth2`. |
| M15, forced rules | Holds. | Preset properties. |
| M18, UR-5 | Holds. | In UG, with `permissions` as template data. |
| Minors | Hold, except as noted. | Corrected. `USER_LINK_REQUIRED` is the one code for a missing link. Lost push is 30 s, with `cacheUntil` as the backstop. `OC-1 and OC-2` is the correct dependency for a private host, since OC-2 holds outbound host registration, so UG and UC moved to match OC. |

None was rejected outright. S6's suggestion to record `act_sub` was not needed, since no route admits the bearer. S10's REST claims stay unverified against a live host. S13's stamp and S15's HMAC matcher were replaced by smaller rules.

## Review of revision 7

A stop check found no Major or higher issue and 13 Minors, all of which held. That check traced the last round's changes and was not an end-to-end read. The two tag corrections are the pool key and closure, now **[design]**, where the run shows the window and depends on a per-client-CONNECT decision that did not run, and `netagent` asking I8 before a 401, also **[design]**, since the run made 0 Token Service requests. The others write out the list order, name `default`, omit `direct` and mirror routes from the decision's git list, drop `POST` from the family sentence, extend the suffixed rule to any method, state a GitLab false refusal, record qualifying hosts on the claim, and name the error code.

## Review of revision 6

A final regression review and a further run read revision 6, each verified against the text and the raw output.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| N1, the route lists have no fall-through | Holds. | **Reduced.** Only a call carrying a user token or a client secret asserts. A class tries its own list, then the other, then the platform default, and the assertion names the list it matched. |
| N2, no clone on a snapshotless resume | Holds. | A provisional read mint is outside the mint refusal. |
| N3, a new session naming a suspended binding | Holds. | Rejected at create, and a deleted binding counts for a running session. |
| Run: pooled connections carried git past a route edit | Holds. | The upstream pool key holds the list, the route digest, and the generation. |
| Run: `PUT /graphql.json` reached the stub | Holds. | A suffixed spelling of a listed GraphQL path is refused on any method **[design]**, and `POST` with a final `graphql` segment stays refused. A wider first form of this revision, covering every write to that final segment, was withdrawn, because it refused `PATCH` and `DELETE` on a repository named `graphql`, a label with that name, and `PUT .../contents/graphql`, with no revision bump. |
| Minors 4, 5, 6, 8, and 12 | Hold. | Corrected. Others are in the egress and git identity designs. |

None was rejected.

**Did the reduction reopen R3-2?** No. R3-2 protected a stored user token and client secret from a route edit between comparison and use, and those calls still assert. An App or project token mint is the tenant's own credential, which the record never covered, and it keeps its decision. **What the reduction gives up** is R4-4's refusal of an unasserted Token Service CONNECT, which a host cannot express, because App mints share those hosts. A level 2 proxy dropping the header on a Token Service link call now gets no comparison, as was already so for `netagent`, and the conformance row is the control.

**Run folded in.** The 401 states, the stem's direction, the carried names, the routing list, and the GitLab token tails moved to **[measured]**. The stub proves forwarding only, and the Token Service and egress were stand-ins. The computed `gitAccess` formula and the Token Service's assertion stay **[design]**. The attack, command, and GitLab numbers did not change.

## Review of revision 5

Round 5 of the reviews and the revision 5 run read revision 5, and each finding was verified against the text and the raw output. "M" and numbered Minors are the security review, and "N-M" and "m" the sync review.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| M2, silent App push outside the qualifying state | Holds. The run shows it: after suspension, a push succeeded as the App. | A session qualifies when it names the binding, whatever its state, and with no current value the host answers 401 **[design]**. The pin exception stays conditional on a release, and a delegated child's clause is stated. |
| N-M2 and Minor 7, routes | Hold. | Two ordered lists, and each call's list is stated. Every Token Service call uses the tenant list. |
| Minor 1, unasserted refusal blocks App mints | Holds. It would break the git path for those tenants. | The Token Service asserts on every CONNECT, deriving from current configuration where no record applies. |
| Minors 2, 3, 6, 11, 12, 13, 14 | Hold. | Erasure re-seals, the plant naming Alice is in the threat row, the stem only widens a refusal, `inject` hosts are named, a suspended provider declares its endpoints for revocation, the presets carry each other's names, and the mint refusal is limited to `netagent`. |
| m3, m4, m5, m6, m18 | Hold. | Corrected. |
| Security M1 | Holds, in the egress design. | The `file` rule keeps reading tenant pools and routes alone. |

None was rejected.

**Revision 5 run folded in.** Tags moved to **[measured]** where it ran. Numbers follow it: 1,069, 160, 0, and 1 on attack rows, 3,033 of 3,045, 63, 23, 6, 0, and 11, and 94 of 120 as predicted. Its row that a suspended session pushes as the App is what the rule above changes. Not run: any live write, permission check, or quick action, a live upload or transfer, `gh release upload` against a GHES host, a decision service, `upload_url` on `ghe.com`, and `gh` on amd64, macOS, or Windows.

## Review of revision 4

Round 4 of the reviews and the spike follow-up read revision 4, and each finding was verified against the text, the tree, and the raw output. "R4" is the security review, and "M" and "m" the sync review.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| R4-1, cached tokens | Holds. `pkg/tokenservice/cache` binds the alias alone. | Each cached value and verdict is sealed with the full key and the digest. |
| R4-2, role | Holds. | Role and Environment join the additional data, under an injective encoding, with the fields written with the value. |
| R4-3 and M1, route value | Holds. | An ordered list per scope, with pattern, target, and the platform default. The asserted route is its first match, as a digest of target fields. |
| R4-4 and m15, level 2 | Holds. | A contract row in egress with a conformance test, and an unasserted Token Service CONNECT is refused. |
| R4-5, `flavour` key | Holds. | Dropped. `/api/*` has no prefix exception under a root prefix. |
| R4-6, deleted provider | Holds. | A `deleting` state replaces the snapshot, with a stated bound. |
| M4, flag owner | Holds. | UC-5 owns it in all three documents, with a `handling` value and a mint refusal. |
| m9, m13, m14, m16, m17, m19, m22, m23, m24 | Hold. | Corrected. m22: the push on an uncovered host is inferred from P, and the token reaching is measured. m23: `t4/table.txt` shows L's tunnel. m24: `gh issue develop` is in the cost. |
| Security Minors | Hold. | The client secret clearing rule is removed, because the seal makes it redundant. Mutable fields, resurrection, and the replica's future-grant reach are stated. The comparison list, assertion details, `file` wording, runtime row, suffix text, suspended pools, Environment-only routes, and digest format are corrected. |

None was rejected.

**Spike follow-up folded in.** Numbers follow it: 96 of 120, 63 and 64 forwarded, the refused command list, and the 4 denied actions and their cause. Its "Readings the text left open" are written as rules. Its findings are closed as follows, each **[design]** because it was not re-run:

| Follow-up finding | Resolution |
| :-- | :-- |
| 1, no credential under the flag | Replaced by the attach rule. |
| 2, `gh release upload` on GHES | A flavour uploads path and form. |
| 3, environment secrets and variables | The rule narrows to protection and deployment settings. |
| 4, denied names in a query | Refused. No corpus document uses them. |
| 5, `merge.foo` | The first-dot stem. |
| 6, `POST .../graphql` | Refused unless listed. Checked against the OpenAPI description: 197 `POST` routes, no `graphql` path, and one parameter route where the word is a possible value. |
| 7, credential tails and signing keys | Added. |
| 8, environment mutations | Added. |
| 9, form writes | The buffering paragraph admits them, and multipart `_method` on a generic rule set is a stated limit. |
| 10, override headers | Added. |

## Review of revision 3

Round 3 of the reviews read revision 3, and each finding was verified against the text and the tree. "R3" is the security review, and "M" and "m" the sync review.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| R3-1, a replica repoints a stored token | Holds. `token-service-deployment.yaml` shares the DSN, and `Cipher.Seal` binds the alias alone. | Tenant, user, identity, and digest sealed as AEAD additional data, including the client secret's. The threat row now states what a replica can forge. No second DSN, because the sealed form holds for the destination rule. |
| R3-2, the route is chosen after the comparison | Holds. | Each call uses the compared content and asserts the route, and the decision service refuses a disagreement. `netagent`'s comparison of two configuration copies is removed. |
| R3-3 and H1, routes per Environment | Holds. | The route value is the set across tenant routes and every `environmentPolicies` entry. **Verified against the terminating path:** the mirror route takes `netagent`'s service account token and no user token, so it does not count, and a held credential is dropped there. An egress group tunnels with no decryption, so it does not count, and its upstream does. A git route to a git endpoint does count. |
| R3-4, names the customer operates | Holds. | The value includes the stored address, and a bullet states the limit. No mechanism. |
| R3-5, `attach` | Holds. | On the type or provider, in the record, fixed by kind on a VCS kind. The binding has one field fewer. |
| R3-6, revocation after deletion | Holds. | **Durable revocation** defines the current digest, and deletion after a mismatch revokes nothing. |
| R3-7 and M1, stored `apiFlavour` | Holds. | The field is dropped, with a table from provider and host. It covers GHES with no stored input, because N1's gap was the absent input, and the host is enough. A non-root prefix refuses writes outside it. Resolution reads pools unfiltered, and differing flavours on a host are refused. |
| R3-8, delegation tree | Holds. | Evaluated by `root_session_id`. |
| R3-9, suffix list | Holds. | The list is dropped for a comparison with and without what follows the last `.`. It has not run. |
| M2, I7 and I8 fields | Holds against the comparison, which is removed. | No I7 addition. The I8 row gains the expected route and CA. |
| M3, UG-1's digest | Holds. | UG-1 depends on UC-1. |
| m6 to m10 | Hold. | Guardrail dependencies, the list format, later pool writes, the read path, and level 1 in egress. |
| m11, header names | Holds. `source-headers.txt` searched four names. | Limited to those. |
| m12, L cells | Holds. `raw/gitopt/t11/` holds T and P alone. | L cells are marked as not run or inferred, and the push to `main` is attributed to P. |
| m14, the alias "did not run" | **Rejected.** `raw/gitopt/attack/deny-ghes.txt` and `deny-ghcom.txt` hold the rows "`/repositories` alias, merge" and "review", refused on both protocols. The review searched the wrong files. | The text now cites those rows. |
| m15, unlabelled preset | Holds. `ext-*.table` shows 66, 23, 11, and 3. | V2 labels both. |
| m16, settled wording | Closed by the owner's decisions. | Decisions 4, 7, 9, and 24 read as settled. |
| Security Minors | Hold. | The page's signal, a tenant template whole, the revert sentence, `inputwait.Registry.Resolve`, the record's list format, `branches` words, `gitlab-deny` data **[design]**, later pool writes, key separation, the rotation check, the row fallback, and memory delete. |

**Owner's decisions applied.** Decisions 4 and 24 are settled, and so is the rule on a `file` binding beside any pool. "Git and hosts" holds T as normative, and the follow-up run on this revision's preset text and on forwarding has not reported, so those items are **[pending]**.

## Review of revision 2

Two independent reviews read revision 2, and each finding was verified against the text and the tree. "H" and "M" are the security review, and "N" the sync review, of which this table lists those on this document. `user-git-identity-design.md`, `egress-and-git-design.md`, and the others record theirs.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| H1, route plus CA | Holds. Origin binding compared names, and neither edit touched a listed field. | The token trust record covers route target and CA digest. |
| H2, relink hands fresh tokens to an edited endpoint | Holds. | A page before the redirect, and a cleared secret. **This reduces the route and does not end it,** as decision 4 states. |
| H3, delivery flip and type recreation | Holds. | The ceiling is in the record, types have an immutable `typeId`, and type hosts are exact. A separate rule making `source` immutable was unnecessary, since `source` is in the record. |
| H4, session input | Holds. `open_responses.go` and `openai_chat.go` call `exec.Send` with no route gate, and `loadSessionForMemory` has no state check. | A check at `executor.Executor.Send` and `sessioninbox.Inbox.Enqueue`, since the tree has no delivery service by that name. Memory writes are refused unconditionally. The test covers every mounted mux. |
| M1, N2, template label | Holds. | Shipped templates alone on VCS kinds, `followsEntryState` honoured on shipped data alone, and a response equal to the parent refused. Known VCS endpoints are refused on `kind: oauth2`, and the EG-12 gap is stated. |
| M2, GraphQL values | Holds. The spike's fixed walk caught variable defaults, directive arguments, and nested arguments. | Every literal is walked. The envelope rule closes string-encoded `variables` and arrays, and it has not run. |
| M3, preset gaps | Holds. `createLinkedBranch`, `updateEnvironment`, and `deleteEnvironment` were confirmed in the schema. | The extended preset, under decision 24. GitLab's table is marked unmeasured. |
| M4, lock escape | Holds. | The lock persists to descendants, `internet` is refused at delegation, and its egress-only reach is stated. |
| M5, replica reach | Holds. `credentialpg` runs in the gateway, against §4.3. | **Adopted the coordinator's preference:** a table under a Token-Service-only key, with no conflict in the tree beyond a new alias and KMS policy. The local provider's shared key is stated. Signed coordinator claims were not adopted, and the reach is stated instead. |
| M6, N4, other credentials on `required` hosts | Holds. | The rule is by host and covers stored secrets, and the `file` refusal is restored in egress. |
| M7, minting a credential | Holds as a residual, with routes untested. | A credential family in the presets **[design]**, and the statement under UCP-6. |
| M9, shared App | Holds. | **The shared App option is removed** in `user-git-identity-design.md`, which reworks less than a platform-only write route with fixed endpoints. |
| M10, later bindings | Holds. | I8 refuses without the mark, and the test names each release path. |
| N1, flavour input | Holds. `github-enterprise` appears in no tree or spec text. | A stored `apiFlavour`, which leaves §14's lease scopes unchanged. |
| N7, silent preset loss | Holds. | Only `preset: none` clears it. |
| N8, guardrail view | Holds. | The check reads the resolved form at binding and pool writes, and I8 refuses a gap. |
| N9, N17, families and escaped reads | Hold. | The strict matcher's rule, anchored. `percentEscapes` and `formatSuffix` are removed as properties. |
| N10, GHES template | Holds. | `{{apiBase}}`. |
| N11, gate trigger | Holds. | The trigger names `required` pools, and UG-2 depends on UC-3. |
| N12, child `caller_type` | Holds. | Copied from the parent. |
| N13, pin reach | Holds. | Unfiltered by Environment, and `internet` pools are stated. |
| N15, buffering | Holds. | `denyBodyKeys` is inspected, and a streamed 401 is stated. |
| N16, route | Holds. | Every link provider endpoint goes through `lenny-egress`. |
| N18 to N20, N22, N30, N35, N36, N41 | Hold. | Corrected. N35's `reregister_required` no longer exists. N41's branch segment is declared, and `glab`'s use of it is owed. |
| Minors of the security review | Hold. | The pin exception follows a release, `self` needs `human`, exposure covers both sources, and `registeredBy` is no longer offered as a defence against Carol. |

None was rejected. H1's alternative, refusing routes and CAs on those hosts, was not needed. The per-field rules of revision 2 are removed.

**Spike results folded in.** V1 and V2 close on the stub, as "Validation owed" lists. The strict matcher replaces revision 2's path text where they differ, in dropping doubled slashes rather than refusing them and in stripping suffixes for matching. "Git and hosts" holds the measured columns. Not run: any GitHub or GitLab host, permission check, or quick action; `gh` on amd64, macOS, or Windows, the .NET SDK image, and drift on the Debian client; a `glab` fork flow, read after push, and git against `ghe.com` beyond the stub; a T and P hybrid, and a GitLab route inventory.

## Sources

Vendor pages are listed in `user-git-identity-design.md`, and the capture and history data are recorded there.

The evidence for this document's **[measured]** rows is under `evidence/user-git-identity-design/`, in `spike-gh/RESULTS.md` and `spike-gh/raw/`, and that folder's `README.md` lists what was left out. Citations in this document resolve under `evidence/user-git-identity-design/spike-gh/`: `raw/gitopt/...` as written, `t4/table.txt` at `raw/gitopt/t4/table.txt`, `deny-ghcom.txt` at `raw/gitopt/attack/deny-ghcom.txt`, and `source-headers.txt` at `raw/gitopt/review/source-headers.txt`.
