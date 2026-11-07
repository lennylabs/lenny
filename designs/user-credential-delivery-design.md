# User credential delivery

Status: design for owner review, revision 21, 2026-11-07. Nothing in this document is built or staged. It reads against [`egress-and-git-design.md`](egress-and-git-design.md) revision 56, [`user-git-identity-design.md`](user-git-identity-design.md) revision 28, [`github-credential-addon-design.md`](github-credential-addon-design.md) revision 1, and [`gitlab-credential-addon-design.md`](gitlab-credential-addon-design.md) revision 1. The owner has settled decisions 1, 2, 4, 29, 30, 31, and 32. Every measurement this document cites ran against a stub running stock git, except the live spikes recorded in the add-on designs. **The stub proves forwarding only:** a row that reached it says nothing about whether a host would accept the spelling, grant the permission, or perform the action. Rules changed after the last run are **[design]**. Statements about the tree were checked by reading the code and the specification on this date. Proposals carry the label UC-n. This document defines no provider behaviour of its own. Examples and evidence name the provider or client they were measured against. "Decision history" and "Review history" at the end record how the design reached this form.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[measured]** | A hands-on result from a validation round of [`egress-and-git-design.md`](egress-and-git-design.md), or from the spike of 2026-11-03 recorded in [`github-credential-addon-design.md`](github-credential-addon-design.md), whose origin was a stub. |
| **[captured]** | Seen at the stub or at the prototype `netagent` in the capture recorded in [`github-credential-addon-design.md`](github-credential-addon-design.md). It shows nothing about what a provider would do. |
| **[pending]** | Waits on a measurement or decision named beside it. |
| **[live]** | Read from a provider's host, anonymously, with no write. |
| **[documented]** | Stated on a vendor page cited in the add-on that relies on it, read and not tested. |
| **[source]** | Read in the Lenny tree or the specification. |
| **[design]** | What this design requires. Nothing implements it. |

## Problem

The owner's direction: "a generic mechanism for tenants to deliver user tokens to a pod. Not tied to GH or anything else. This would require opting in, and it wouldn't have the same security guarantees as token injection, but the mechanism needs to exist because we can't possibly handle all injection scenarios out there." And: "Ideally, I would want user token injection to also be generic and configurable."

Lenny has no such mechanism today.

- [§4.9 Pre-Authorized Credential Flow](../spec/04_system-components.md#pre-authorized-credential-flow) stores a user's own credential for a closed set of providers and delivers it only to the LLM reverse proxy.
- The credential bindings of [`egress-and-git-design.md`](egress-and-git-design.md) are tenant credentials. A binding has no notion of whose token it holds.
- [`user-git-identity-design.md`](user-git-identity-design.md) revision 3 had a GitHub and GitLab filter in `netagent`. The owner's conditions for keeping a GitHub-specific injection were that it survive `gh` updates and work for all `gh` commands. Both were measured false for the allowlist, and the owner chose to replace it (see "Decision history" near the end).

Some cases cannot use injection at all. A tool signs requests with the token, pins certificates, or speaks a protocol `netagent` does not terminate, and an arbitrary SaaS tool needs its token as a file. Those cases have a known cost, and this design opts into that cost openly.

## Scope

This design covers tenant-defined user credential types, a `registered` source a tenant backend writes to, an `oauth2Link` source that links a person's account at any OAuth2 provider, the link flow and the stored links, a `userCredential` binding delivered under `delivery: inject` or `delivery: file`, request rules held as data, the preset format, and the add-on model through which a provider family adapts all of these.

Out of scope: a tenant-run token endpoint, which is reserved as a later value of the source field. [`user-git-identity-design.md`](user-git-identity-design.md) owns the VCS layer: repository authorization, pull request authoring, and the VCS pool rules that apply to these bindings. [`github-credential-addon-design.md`](github-credential-addon-design.md) and [`gitlab-credential-addon-design.md`](gitlab-credential-addon-design.md) own the GitHub and GitLab kinds, templates, and presets. [`egress-and-git-design.md`](egress-and-git-design.md) owns the `inject` and `file` delivery mechanisms, the pod credential API, and the git path. [`security-oversight-design.md`](security-oversight-design.md) owns guardrails, [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) owns environments and user lifecycle, [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md) owns evidence handling, and [`tenant-outbound-connector-design.md`](tenant-outbound-connector-design.md) owns private hosts.

## High-level design

Some tools an agent runs need to act as the person who started the session, with that person's own token for a service such as Jira, GitHub, or an internal API. This design lets a tenant give that token to the person's sessions and to no one else's. The token comes from one of two places. The tenant's own systems can write it to Lenny, or the person can sign in to the service once, on a browser page Lenny shows before any session needs the token. Lenny keeps the token encrypted in its Token Service, and a session receives it only when the session belongs to that person. A tenant configuration change cannot send a stored token to a new destination, because each token is sealed to the configuration it was stored under, and any change makes the token unusable until the person acts again.

A session uses the token in one of two ways. In the default way, the agent never sees the token: `netagent`, a proxy in the agent's pod, adds the token to the agent's requests on their way to the service, and it can refuse requests the tenant's rules forbid, such as approving or merging a pull request. In the other way, `netagent` writes the token to a file the agent can read, for tools that cannot work through the proxy. Once the agent has read the token, Lenny no longer controls where it goes, so this way needs a grant from the platform operator and an opt-in from the tenant. Behaviour particular to one provider, such as GitHub's per-session child tokens or the `gitlab-deny` preset, lives in add-ons that plug into this mechanism.

The rest of this section states each part in the design's terms: where a token comes from, how a session receives it, which limits apply on the way to the host, and how an add-on adapts the mechanism to a provider family.

**Types and sources (UC-1, UC-2).** A tenant defines a user credential type, which names a source and the hosts where its tokens work. Under the `registered` source, a tenant backend or the user writes a token for a named user through the credentials API. Under the `oauth2Link` source, the user links an account at a link provider through a browser flow that Lenny starts before any session needs the token. The flow is bound to the user's browser login and to a cookie, and the link provider's `identityBinding` decides how the provider account is tied to the Lenny user. An optional narrowing template derives a per-session token from the stored one. The Token Service stores each token envelope-encrypted under a per-tenant key that only it can decrypt, refreshes linked tokens on demand under a row lease, and never releases a refresh token. Each stored token is sealed with the digest of its token trust record, which holds everything that decides where the token can go: endpoints, hosts, routes, CAs, the `attach` template, the narrowing template, the kind block, and the delivery ceiling. Any change to that record makes the token unusable until the user acts again or the record reverts, so a configuration edit cannot redirect a stored token.

**Delivery (UC-3, UC-5).** A `userCredential` binding names a type and a delivery. `netagent` asks the Token Service for the binding over the pod credential API, and the Token Service releases only the token of the user on the session row, and only when that session's caller is a human. Under `delivery: inject`, `netagent` holds the token and attaches it to requests to the type's hosts, and the runtime holds only a placeholder. Under `delivery: file`, `netagent` writes the token to a file the runtime reads. `delivery: file` needs a platform grant, a tenant opt-in on the binding, and a delivery ceiling of `file` in the token's record, and "What `file` does not promise" states what it leaves unprotected. Both deliveries are refused on pods that can serve another session, a delegated child receives a binding only when its delegation lease names it, and an impersonation bearer cannot give input to an affected session.

**Request rules and presets (UC-4).** Under `delivery: inject`, a binding can carry request rules held as data: REST path rules, GraphQL mutation rules, and body patterns. Under `requestRules.default: allow`, the default, a request passes unless a rule denies it. Under `requestRules.default: deny`, allow-list mode, a write also needs a rule that allows it. A preset is a named, versioned rule set that an add-on ships. `netagent` checks each request in a canonical form and forwards bytes it generated from the checked request, so a parser difference at the host cannot carry a refused operation past the rules.

**Exposure and leak scanning (UC-5, UC-6).** Under `delivery: file`, durable exposure rows tell the tenant which token generations a runtime held, so its backend can revoke them at the provider. Optional leak scanning redacts the value from the traffic the gateway carries.

**Add-ons (UC-7).** A link provider carries a `kind` and a configuration block keyed by that kind. An add-on registers the kind, the block's schema, and its implementation of named extension points: the link kind itself, template variables, a release check, a request layout and default preset, a host policy, and an attach. The `oauth2` kind is built in. The [VCS layer](user-git-identity-design.md) and the GitHub and GitLab add-ons are the add-ons that exist today. An add-on can refuse what this design admits, and it cannot admit what this design refuses.

```
 Tenant backend, or the user ──(write a token)──────> Gateway ──┐
 Browser ──(link flow, UC-2)────────────────────────> Gateway ──┤
                                                                ▼
             Token Service: types, link providers, token trust records, sealed tokens, add-on checks
                                                                ▲
                                                                │ pod credential API: token, hosts, rules
 Pod: runtime <──(file, under delivery: file)────── netagent ───┘
      runtime ──(placeholder, under delivery: inject)──> netagent ──(token attached, rules applied)──> lenny-egress ──> type hosts
```

Two limits frame the result. A token the runtime holds under `delivery: file` leaves every Lenny control once it is read. Request rules default to a deny list, so an operation a preset does not yet name passes until the preset is updated, unless the binding uses allow-list mode. "Residual risks" states both.

## Terms

| Term | Meaning |
| :-- | :-- |
| User credential type | A tenant object naming a kind of per-user token, its source, and the hosts where it works. |
| Source | Where a type's tokens come from: `registered` or `oauth2Link`. The value `endpoint` is reserved. |
| Type hosts | For `registered`, the hosts the type declares. For `oauth2Link`, the provider's `apiHosts`. Every host rule here evaluates against these. |
| Token trust record | The one record of everything that decides where a stored token can go and who can read it. See "Token trust record". |
| `userCredential` binding | A credential binding whose `secret` names a type, so each session receives its own user's token. |
| Request rules | Data on a binding with `delivery: inject` that refuses requests before the token is attached. |
| Preset | A named, versioned set of request rules and request forms, shipped as data, with paths per API flavour. |
| Narrowing template | Data on a link provider describing how to derive a per-session token from the stored one. |
| Delivery | A binding's `delivery` field. Under `delivery: inject`, `netagent` attaches the token to requests and the runtime holds only a placeholder. Under `delivery: file`, `netagent` writes the token to a file the runtime reads. |
| Delivery ceiling | The highest delivery a stored token may take, set by the `delivery` field on a `registered` type or on a link provider and held in the token trust record. Its values, in increasing order, are `identifier`, `none`, `inject`, and `file`, and `identifier` and `none` exist on link providers alone. |
| Pod credential API | The gRPC API on which `netagent`, and the git mirror, ask the Token Service for a binding's token. A response carries the token with the binding's hosts, attach template, `cacheUntil`, `handling`, and resolved request rules. [`egress-and-git-design.md`, "Interfaces"](egress-and-git-design.md#interfaces) defines it as I8. |
| Egress decision API | The API on which a proxy, the git endpoint, or the Token Service asks `lenny-egress-authz` whether a connection is allowed. [`egress-and-git-design.md`, "Interfaces"](egress-and-git-design.md#interfaces) defines it as I3. |
| Link provider | A tenant object naming a provider kind and the tenant's OAuth2 client for it. A tenant can have several of one kind. |
| Link | One Lenny user's stored authorization under one link provider, keyed on tenant, user, and link provider name. |
| Parent token | The access token and refresh token a link holds. The refresh token never leaves the Token Service. |
| Kind | A link provider's `kind`, which selects the add-on that defines its configuration block and its behaviour. `oauth2` is built in. |
| Kind block | The configuration block on a link provider keyed by its kind, whose schema the kind's add-on registers. |
| Add-on | A compiled-in package that registers one or more kinds and implements extension points. See UC-7. |
| Extension point | A named interface this design calls at a defined moment, which an add-on implements to supply provider behaviour. See UC-7. |
| `user_act_required` | The one state of a stored token that needs a new user act. It is derived from a trust record mismatch, and stored when a provider refuses a refresh. |

The API field is `linkProviders` and the table is `link_providers`, and both name the same object. An API flavour names a request layout that an add-on registers, and it selects a preset's paths and a template's API base. No new link, channel, or register enters §28. The Token Service calls sit on `schemas/lenny-tokenservice.proto`, which [§28.7 Wire-contract artifact register](../spec/28_communication-channels.md#287-wire-contract-artifact-register) records as a surface the channel register holds no entry for, and the fields this design adds to the pod credential API are additions to an interface the [egress design](egress-and-git-design.md) defines. Identifiers here use `userCredential` and `user_credential`, which share no stem with a channel identifier.

### Labels from other designs

Proposals, properties, and requirements in other designs carry a label whose prefix names the design. This document cites them by label and links each citation to its definition. The table names what each cited label stands for.

| Labels | Design | What they name |
| :-- | :-- | :-- |
| EG-1, EG-2, EG-3, EG-4, EG-7, EG-9, and EG-12 | [`egress-and-git-design.md`, "Proposal split"](egress-and-git-design.md#proposal-split) | Proposals of the egress design: EG-1 git prerequisites, EG-2 the pod network agent and proxied egress, EG-3 the pod git path, EG-4 credential delivery, EG-7 level 1 replacement, EG-9 egress policy objects with the single write path for tenant opt-ins, and EG-12 exfiltration controls. |
| P2, P3, and P4 | [`egress-and-git-design.md`, "Security properties"](egress-and-git-design.md#security-properties) | Egress security properties: P2 keeps long-lived credentials out of pods, P3 keeps egress and VCS credentials unreadable by the agent, and P4 keeps one tenant's data and credentials away from another tenant's pods. |
| UG-1, UG-2, UG-3, and UG-4 | [`user-git-identity-design.md`](user-git-identity-design.md) | Proposals of the VCS layer: UG-1 VCS link kinds, UG-2 repository authorization, UG-3 pull request authoring, and UG-4 the independent approval check. |
| UP-n and UR-n | [`user-git-identity-design.md`](user-git-identity-design.md) | The VCS layer's security properties (UP) and requirements (UR). |
| SO-2, SP2, and SP11 | [`security-oversight-design.md`](security-oversight-design.md) | SO-2 is the tenant security record, which holds a tenant's guardrails and delays any change that weakens one. SP2 and SP11 are security properties of that design. |
| TS-1, TS-4, TS-6, and TS-7 | [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) | TS-1 environment scoping, TS-4 user lifecycle and invalidation, TS-6 identity source records and browser login, and TS-7 login routing. |
| FE-6 | [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md#fe-6-agent-activity-streams) | Agent activity streams. |
| OC-1 and OC-2 | [`tenant-outbound-connector-design.md`, "Proposals"](tenant-outbound-connector-design.md#proposals) | The outbound connector's gateway, agent, and identity (OC-1), and its hosts and policy (OC-2), which reach a private host. |
| S15 | [`github-credential-addon-design.md`, "Validation owed"](github-credential-addon-design.md#validation-owed) | A GitHub spike run against a provider-issued token. |

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
- UCP-2. A `userCredential` binding is refused on a pod that can serve another session or a later session, or that has served one. The binding validator and the gateway hold this at create. The pod credential API releases nothing unless the claim carries `retireOnRelease`, and every release path honours that mark whatever the pool now says.
- UCP-3. An impersonation bearer cannot give session input to a session in a delegation tree where any session holds a `userCredential` binding or declares a repository under a VCS pool with `userAuthorization: required`, cannot write to or delete from any user's memory, and cannot write a user's credential. The check sits at the session input layer, so every adapter inherits it.
- UCP-4. `delivery: file` needs the `userCredentialDelivery` grant, a tenant opt-in on the binding, and a delivery ceiling of `file` in the token's trust record. The write path, the Token Service, and `netagent` hold this.
- UCP-5. A refresh token never leaves the Token Service. A stored token of either source goes only to the names and route targets in its token trust record. **On any change to that record the stored token is unusable and is sent nowhere until a new user act or a revert to the identical record.** Below a delivery ceiling of `file`, this protects stored tokens against reading through configuration. It does not protect a future grant, a name the customer operates, or a token under a delivery ceiling of `file`, which a binding flip and a setup command can read.
- UCP-6. Under `delivery: inject`, no process in the runtime or adapter container can read the token, and `requestRules` apply. `netagent` attaches it only to the type's hosts. **Under `delivery: file`, none of this is true.** Under `delivery: inject`, an agent can still ask a host to mint a new credential through an operation the token permits and the preset does not name, and it then holds what the host returns.
- UCP-7. One tenant's type, token, preset choice, or OAuth2 client never reaches another tenant. A client secret is a write-only value stored under the tenant, never a reference a tenant can point elsewhere, and no client is shared across tenants. The Token Service re-checks the tenant at read time.
- UCP-8. A delegated child receives a `userCredential` binding only when the delegation lease names it. A pool's attach does not bypass this.
- UCP-9. Every release of a token on the pod credential API produces a `credential.mint` record, and the first delivery to a session produces an audit event.
- UCP-10. When Lenny stops a credential (revocation, expiry, user disable, quarantine, or session end), the pod credential API releases no more, and `netagent` removes the file or flushes the token within the bounds under "Stopping a credential". **Lenny makes no claim about copies of a token the agent was able to read, or about a credential the agent had a host mint.**

- UCP-11. A link's provider account is one the tenant's identity source ties to the Lenny user, unless the link provider records `identityBinding: unverified`. The link flow holds this, and so does the re-evaluation under "Identity binding".

### Threats

| Attacker | Can | Cannot | Why |
| :-- | :-- | :-- | :-- |
| Prompt-injected agent, `file` delivery | Read the token. Send it to any host the effective policy allows. Use it for everything it permits, from anywhere, until the provider expires or revokes it. Copy it into the workspace, a transcript, an LLM request, or an export. | Read another user's token, or obtain a refresh token. | UCP-1 and UCP-5. "What `file` does not promise" is the full statement. |
| Prompt-injected agent, `inject` delivery | Do what the token permits through the type's hosts, except what `requestRules` refuse. A new provider operation the preset does not yet name passes. Ask a host to mint a credential through an operation the token permits and the preset does not name, and hold what comes back. | Read the token, except through a reflection the scrub misses, or send it to another host. | UCP-6, and the [egress design](egress-and-git-design.md)'s `inject` rules. |
| Compromised `netagent` | Read the token under either delivery, ignore `requestRules`, and use the token for its life. | Obtain another session's token, or a refresh token. | The pod credential API answers for the pod's own binding. |
| Tenant admin or backend holding `manage_user_credentials` | **Plant:** write a token under Alice's name, so that Alice's agent acts with an account Alice does not own, or exfiltrates to an account the writer controls. Settled under decision 4. | Read back a token already stored. Write across tenants. | Writes are one-way and audited, and Alice can list and delete them. `registeredBy` limits who writes, and she can edit that type field too. |
| Tenant admin controlling configuration | **Obtain a future token.** She edits a record, and Alice's next session asks Alice to relink. If Alice proceeds past the page, Alice grants a new token to a host Carol runs. She can also re-point an automated backend, whose next write sends a value to a host she added. Make every stored token unusable by editing a record. **Read tokens on a name she also operates,** through its DNS, CA, or network, which the record cannot see. | **Read a token stored before an edit, below a delivery ceiling of `file`,** under either source, through an endpoint, host, route, CA, `attach`, template, client, `kind`, `source`, or delivery change, or by deleting and recreating a type. | UCP-5. The future grant has no per-field fix, because she configures the tenant. The page and the sealed client secret reduce it, and the enforcing control is `tokenTrust.allowedRecords`. Decision 4 records the owner's settlement. |
| Compromised gateway replica | See a `registered` value and a `clientSecret` in transit on a write, and plant a token in any tenant through the pass-through route, supplying `written_by` itself, so a plant can name Alice and her `GET` shows her as the writer. Forge session rows and bindings, which `pkg/binding` reads, so it can use any stored token at its record's hosts through a pod it drives, and read any value whose delivery ceiling is `file`, including through `leakScan`. **Resurrect a row** after `DELETE`, `revoke`, or erasure from a copied ciphertext, because the per-tenant key stays and no counter is sealed, and restore an earlier envelope. Rewrite the mutable fields: `state`, including `revoking` back to `active`, and exposure acknowledgements. Write provider rows directly, so `tokenTrust.allowedRecords`, which sits on [EG-9](egress-and-git-design.md#proposal-split)'s write path, does not hold against it for a future grant. Read LLM credentials in today's `credentials` table, which is a defect recorded under "Current state". | Point a stored token at another destination, move a ciphertext to another row, role, or environment, or move a cached value to another key, because identity, role, and digest are sealed as additional data, so an edit fails to decrypt. Rewrite a sealed field. Decrypt a stored row itself, or obtain a refresh token. | UC-1 **Storage**. The key separation holds on a cloud KMS, and the local provider mounts one master key in the gateway, the Token Service, and `lenny-ops`. |
| Tenant admin able to change a runtime or `workspaceDefaults` | Run a setup command in Alice's session that reads a token under `delivery: file`, since the runtime container can read it. Under `delivery: inject`, have a setup command use the token, including a host's mint operation, as UCP-6 states for an agent. | Read a token under `delivery: inject` directly that way. | This is inherent in `delivery: file`. `lockEgressToHosts` narrows where the value can go. |
| A user registering a token that is not hers | Have her agent act under another account. | Be stopped by Lenny on `registered`, which cannot tell whose token it holds. | Residual stated. `oauth2Link` with an identity binding addresses it. |
| Tenant registering a hostile OAuth2 endpoint | Answer token calls as it likes, for its own users. | Reach a private address, name another tenant's secret, or shadow a reserved host. | The registration check, the write-only client secret, and the mint path bounds under "UC-2". |
| Attacker who tricks Bob into completing a link flow | Send Bob an authorization URL or a start URL. | Bind Bob's GitHub account to the attacker's Lenny user, or the attacker's account to Bob's Lenny user. | The start page requires a browser login matching the flow's subject, and the callback checks a cookie bound to that login. |
| Another tenant's session | Nothing here. | Read any of this. | The egress design's tenant isolation property [P4](egress-and-git-design.md#security-properties), UCP-7, and the pod refusal. |
| An operator impersonating Alice | Read what impersonation allows, and terminate or interrupt the session. | Give an affected session input through any adapter, write to Alice's memory, or write or rotate her credential. | UCP-3. |
| A crafted request on a host with rules | Spell an existing operation differently, such as an escape or a suffix. | Have a spelling reach the host that the rule did not check. | "What every rule set forces". The matcher is **[measured]** on the stub, which proves forwarding only, with 0 denied actions reaching with the credential. |

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
| A pool's provider separates GHES from GitHub | False. `ProviderGitHub` is the one value, and §14 embeds it in `vcs.github.read`. The host separates them, as [`user-git-identity-design.md`, "Request rules on VCS hosts"](user-git-identity-design.md#request-rules-on-vcs-hosts), states. | `pkg/credential/credential.go`, §14 |
| Pool recycling is read from the current pool at release | True. `poolRecyclePolicy` reads the pool in the scrub reporter's seams. `lenny.dev/runtime-process` is stamped on a pod and immutable. | `pkg/gateway/session/recycle/scrubreporter_seams.go`, `pkg/admission/webhook/label_immutability.go` |
| Credential Secrets sit in one shared namespace, and tenant isolation holds where references enter a pool | True, for pools. Link providers have no such rule. | §4.9 **Secret references.** |
| A connector's `clientSecretRef` is resolved with no tenant check | True, and the route is `platform-admin` alone, so this is no tenant escalation. The gateway has no `get` on Secrets but the named admin token Secret, so a confidential connector's resolution fails (F-9.3.19). Both are recorded in [`user-git-identity-design.md`](user-git-identity-design.md). | `pkg/gateway/connectors/connectorsecret/connectorsecret.go`, `pkg/gateway/externalapi/admin/routes.go`, `charts/lenny/templates/credentials-namespace.yaml` |
| A delegated child inherits named bindings | True under the egress design's "Effective policy", which UC-3 narrows for this kind. | [`egress-and-git-design.md`](egress-and-git-design.md) |
| A session's `env` is checked against a blocklist | True. The default holds exact names and `*_SECRET_*`, `*_SECRET`, `*_KEY`, `*_TOKEN`, and `*_PASSWORD`, and an operator can extend it. Other names pass. | [§14](../spec/14_workspace-plan-schema.md), `pkg/gateway/provisioning/envblock/envblock.go` |
| `inlineFile` content is inspected for secrets | False. | §14 |
| Events are durable | False. §25.5 says `Emit()` skips the Redis write when Redis is unreachable. | [§25.5](../spec/25_agent-operability.md) |
| Gateway interceptor phases exist for agent output, tool results, exports, messages, delegation, connectors, and proxy-mode LLM traffic | True, with `PreLLMRequest` in proxy mode only. | [§4.8 Gateway Policy Engine](../spec/04_system-components.md#48-gateway-policy-engine) |

## UC-1: Credential types and the `registered` source

### Types

A tenant defines types through the single write path for tenant opt-ins, which the egress design's proposal [EG-9](egress-and-git-design.md#proposal-split) (egress policy objects) adds and which applies the tenant's guardrails and writes an audit event. A type is a tenant object, and environment scoping ([TS-1](tenant-scoping-identity-design.md#ts-1-environment-scoping)) can limit it to named environments.

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

**Hosts sit on the type.** Every host rule in this design and in its add-ons evaluates type hosts rather than a binding's `hosts`: the one-binding rule here, and an add-on's host policy, pin, preset, and repository rules. Type hosts are exact and canonical, and a binding's `hosts` must be a subset of them. A `registered` type can lie about its hosts, by declaring `docs.acme.com` and holding a GitHub token, and Lenny cannot tell. That is a residual, and the controls that enforce rather than check are `lockEgressToHosts` under `delivery: file` and `credentialPinned` on the host the token works on. [EG-12](egress-and-git-design.md#proposal-split) lands after these proposals, and until it does the second control is absent.

### Token trust record

Every decision about where a stored token can go and who can read it sits in one record. This replaces the separate per-field rules of the earlier revisions, each of which a review round found another way around.

| Field | `registered` type | Link provider |
| :-- | :-- | :-- |
| Identity | `typeId` and `source` | `name`, `kind`, and `clientId` |
| Endpoints | None | Authorization, token, revocation, and user info endpoints, whether the kind fixes them or its block sets them |
| Hosts | The type's `hosts` | `apiHosts` |
| Route and trust | The route value and the CA digest for each host | The same, for each endpoint host and API host |
| `attach` | On the type | Fixed by the kind, or set in its block |
| Narrowing | None | Template identity: a shipped template's name and contract version, or a tenant template's digest, covering all of it |
| Delivery ceiling | `delivery` on the type | `delivery` on the provider |
| Kind block | None | Every field of the block, except the fields the kind's add-on exempts, each with a stated reason |

- **Route value.** For a host, two ordered lists per scope, one for the tenant and one for each `environmentPolicies[E]`, in the order routes apply: one of egress routes, and one of git routes to a git endpoint, where a git route to `direct` or the mirror adds no entry, and the decision service's git list omits them too. Each entry names its matching pattern and target: kind, identifier, and the resolved target fields Lenny stores, including a registered address. Where no tenant route matches, the value names the platform default. A link has no environment, so one value has to cover them all. Each route ends at a target that can present a certificate under a CA in the record, so both lists count. A git route to the mirror does not count: `netagent` terminates the host and sends the mirror its own service account token, so no user token goes there. An egress group does not count, because its Envoy tunnels bytes with no decryption, and an upstream it chains to is a route target, which does. A periodic re-resolution of a tenant-registered public endpoint suspends it on a bad answer and writes nothing here, so it flips no digest, and an edit to the address does change it. **The cost is that a scoped route, a move between environments, a reorder, or a change to the platform default on a token's host makes tokens unusable for every environment, and for the last, across tenants.** [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) points here. A link provider endpoint or a VCS pool's mint host reachable only through an `environmentPolicies[E]` route is refused at write, because every Token Service call uses the tenant lists.
- **Digest.** The Token Service records the digest of the canonical record, which carries a format version, with each stored token, at write or at link, and keeps the record's content in `token_trust_records`. Each envelope is sealed with AEAD additional data under an injective, length-prefixed encoding: tenant, role (`access`, `refresh`, `clientSecret`, or `registered`), user where there is one, `typeId` or provider name, environment, the digest, and the fields written with the value (`expires_at`, `written_by`, `generation`, `token_id`, `encoding`, and `provider_user_id`). An edited digest, a moved ciphertext, or a swapped role then fails to decrypt, and stored content that does not hash to the sealed digest is refused.
- **Comparison and use.** The Token Service computes the current digest and compares it at each release on the pod credential API, at each `leakScan` release, mint, refresh, narrowing, code exchange, user info, check, and revocation call. **Each call then derives its URL and expected route from the record content it compared. A call that carries a user token or a client secret asserts that route on the CONNECT, and the decision service refuses a disagreement.** That is a link provider call by the Token Service, an API request by `netagent` after a `userCredential` release, and a git request from a session that `allowRuntimeHeldCredentials` applies to. An App or project token mint carries neither, sits outside this record, and keeps the egress decision it has today, on the path the fetch takes, with no route assertion. A request's class, git path or API, tries in this order, in both the assertion and the decision: its own list for the session's environment, its own list for the tenant, the other list for the environment, the other list for the tenant, then the platform default. Every Token Service call uses the tenant lists. The assertion names the list it matched, `egress`, `git`, or `default`, and carries a digest of the target fields, so an address edit is seen. A host with a git route alone and a host with an egress route alone therefore both work **[design]**. The record holds a CA digest, and each call reads the CA from the host's endpoint or outbound host record and refuses one whose digest differs. On the pod credential API, `netagent` receives the expected route and CA and asserts the same. A route edit that reaches the decision service first therefore stops a held token rather than redirecting it. An assertion is compared at CONNECT, so `netagent` keys its upstream pool on the list and the route digest, and a route write closes pooled upstream connections to the hosts it changes **[design]**. In the run, idle pooled connections carried git past an edit, with 0 new CONNECTs, on a stand-in with no per-client-CONNECT decision **[measured]**. At level 2 the proxy must pass the header to its call on the egress decision API, as the egress design's contract states, and a proxy that drops it gets no comparison, which that conformance row is there to catch. A Token Service connection is reused only within one tenant, route, and CA, an asserted call never reuses an unasserted connection, a first release closes any unasserted upstream connection `netagent` holds to that host, and a call carrying a token or secret follows no redirect.
- **Mismatch.** The stored token is unusable and is sent nowhere, neither to an old endpoint nor to a new one, for both sources. Any difference counts, including a removed host or a lowered ceiling, because a direction per field is what this record replaces.
- **State.** A row has one state for this, `user_act_required`. It is derived from a mismatch, and it is stored when a provider refuses a refresh (UC-2). The new user act is a relink for a link, and a new write under `registeredBy` for a `registered` row.
- **Revert.** A record identical to the recorded one makes the token usable again, because nothing was sent in between. A row stays until a new act replaces it, its owner deletes it, or erasure runs. An operator who changes a record because it was compromised, such as after a CA leak or an endpoint takeover, also revokes at the provider or deletes the rows, because replaying the record would restore them.
- **Deleting a provider.** Deletion puts the provider in a `deleting` state, which keeps it, its record, and its client secret until its links have drained through the durable record under "Lifecycle and revocation at the provider" or `linkProviderDrainSeconds` ends, 86,400 by default. Meanwhile it takes no new link and releases nothing, and a recreate under that name is refused. The ordinary comparison applies to each attempt, so a later route or CA change stops it. At the bound, or after a mismatch, each remaining row ends with an audit event and a user notification saying that its grant was not revoked. A deletion after a mismatch revokes nothing. Every `revoking` row, from a user's delete or §11.4, takes the same bound and notice, and a provider that a guardrail has marked `suspendedBy` still declares its endpoints for revocation.
- **Outside the record.** A shipped template's overridable fields that the template exempts stay out, and a change to one is audited and reported in posture. The GitHub add-on exempts `permissions`, because it limits what a child can do at the host within what the App holds. A rotated `clientSecret` value changes no field here. A row's `state` and exposure acknowledgements are mutable, and a replica can rewrite them, as the threat table states.
- **No CA on the binding.** A `userCredential` binding carries no CA and no `attach` of its own. The CA comes from the host's endpoint or outbound host record, which the record covers.

The guardrail `tokenTrust.allowedRecords` lists the records a `security-admin` has approved, each as its digest and content, because a person cannot review a hash, under the delay that the tenant security record ([SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)) puts on every change that weakens a guardrail. When it is set, [EG-9](egress-and-git-design.md#proposal-split)'s write path recomputes the digest of each provider and type a write touches, including a route or CA write, and refuses or suspends what is not listed. Posture lists each current record and whether it is on that list.

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

The existing defect stays refused. `provider` values with no delivery path (`aws_bedrock`, `github`, and `vault_transit`) are refused with `VALIDATION_ERROR` once UC-1 lands, since a registration that can never deliver is misleading **[source]**. A tenant-defined type on a GitHub host is a different thing, and it is allowed, subject to the host policy of [`user-git-identity-design.md`](user-git-identity-design.md).

### Who can write

`POST /v1/credentials` acts only for the authenticated user **[source]**. A tenant backend needs a route that names the user.

```
PUT /v1/admin/users/{userId}/credentials/{type}
{"secret": "...", "encoding": "text", "expiresAt": "2026-11-04T00:00:00Z", "environment": "", "tokenId": "opaque-writer-id"}
```

- **Authorization.** A new permission, `manage_user_credentials`. It sits inside `TenantAdminPermissions()`, because §10.2 limits a custom role to that ceiling, so `tenant-admin` holds it, and a custom role such as `credential-writer` can hold it alone. A backend authenticates through §10.2's client credentials path under that role. This is a new role and permission rather than a scope, because `/v1/admin` routes authorize by permission, and §25.1 scopes bound tools rather than admin routes. Decision 4 records the cost that a tenant admin can write a token under any user's name. A `platform-admin` holds no special path here: the route acts within the caller's own tenant, so an operator cannot place a token in a customer's user, and the "all tenants" reach §10.2 gives that role does not extend to it.
- **Type check.** `registeredBy` must include `service`. `POST /v1/credentials` needs `self`, and it admits only a caller whose `caller_type` is `human`. A type with `registeredBy: [service]` lets a tenant refuse user-supplied tokens.
- **Owner check.** The row's owner is the named user. `ownedBy` extends for the route above: a caller with the permission may act on rows of its own tenant, and the existing routes still need the caller to be the owner. A rotation through `PUT /v1/credentials/{credentialRef}` by the owner remains possible where `registeredBy` includes `self`, under the same `caller_type: human` check. The row records `written_by`, the subject and caller type of its last writer, and Alice's `GET /v1/credentials` returns that value, so she can see a token she did not supply.
- **Named user.** The subject is accepted as given, the same choice the user lifecycle upsert of [TS-4](tenant-scoping-identity-design.md#user-lifecycle-ts-4) makes, because a user may not have a row yet. These subjects are refused: one with an invalidation level or an Article 18 restriction, one with a completed erasure job, one holding a security role ([SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture), on the reasoning of property [SP2](security-oversight-design.md#security-properties) in [`security-oversight-design.md`](security-oversight-design.md), which keeps a tenant admin from invalidating, disabling, or erasing a security role holder), and one that is a service, by its registry row or by a service list an add-on keeps, such as a VCS pool's `serviceIdentities`.
- **Impersonation.** An impersonation bearer is refused on this route and on every `/v1/credentials` write, through `callercheck`. Otherwise `written_by` would record Alice for an operator's write, and rotation would reach her running agent.
- **Bytes.** The value is opaque. `encoding` is `text` or `base64`, for binary tokens and files. The limit is `maxBytes`, at most 64 KiB at the platform ceiling, because `netagent` holds the value in memory and the pod credential API carries it. The current handler's 1 MiB reader stays as the outer bound.
- **Expiry.** `expiresAt` is optional, and required when the type sets `requireExpiresAt`. `maxLifetimeSeconds` bounds it. The pod credential API releases nothing past it, and `netagent` removes the file at that time from the `expiresAt` it holds. The Token Service clamps `cacheUntil` to it.
- **Storage.** A new table in the Token Service's store, with row-level security, holds type rows keyed on tenant, user, `typeId`, and environment, with `expires_at`, `written_by`, `encoding`, `token_id`, `generation`, `state`, and `trust_digest`. It is envelope-encrypted under a per-tenant KMS key that only the Token Service's identity can decrypt, as §4.3 reserves, with the additional data under "Token trust record". `pkg/kms/envelope`'s `Cipher.Seal` and `Open` bind the KEK alias alone today, so they gain that input, and `Reseal` rewraps the DEK, so re-encryption is unaffected. This shares a Postgres role with the gateway, so a replica can edit rows, and the sealed form is what makes an edit fail. The gateway passes a write through to the Token Service over mTLS and sees the value in transit, as it does today. `GET /v1/credentials`, `ownedBy`, and `revoke` read a type row's metadata through the same Token Service API. The existing `credentials` table stays for LLM credentials, unchanged, because its rows are decrypted in the gateway. §4.9.1 re-encryption gains this table with the Token Service as its runner, because `pkg/kms/rekey` runs in the gateway today, and §12.8's `TokenStore` deletion gains it too. Erasure of a writer pseudonymizes `written_by` on other users' rows, and the job re-seals each through the Token Service, because `written_by` is in the additional data. Under `kms.provider: local` the chart mounts one master key in the gateway, the Token Service, and `lenny-ops`, so this separation holds on a cloud KMS alone.
- **Audit.** `credential.registered`, `credential.rotated`, and the others of §4.9.2 carry `type` and `written_by`, and the secret stays out of every log, as §4.9 already requires of these routes.

### Rotation and removal

Rotation uses the existing `PUT /v1/credentials/{credentialRef}` and the new admin route. `RotateUser` only re-caches for the LLM proxy **[source]**, so a type row takes a new propagation path:

1. The store commits the new material and increments `generation`.
2. The gateway pushes a binding change for each session of that user holding a binding of that type, as it does for a binding definition edit.
3. `netagent` flushes, asks the pod credential API, and receives the new value. Under `delivery: file` it swaps the directory atomically, with `binding.json`'s version counter incremented, as the egress design describes.

A lost push costs at most the egress design's 30-second re-read for the binding, and `cacheUntil`, 300 seconds for this kind, is the backstop if the subscription is lost. Tools that re-read the file pick up the new value, and a tool that read it once keeps the old value. The egress design tested re-reading for the tools it names **[measured]**.

`POST .../revoke` and `DELETE` both stop use on a type row. §4.9 lets `DELETE` leave running leases alone. That rule belongs to the LLM path, and it is unsafe for a token a tenant is trying to remove, so type rows behave differently, which the API documentation states.

A token never enters a shared cache keyed without its owner. The Token Service's cache key for a user credential or a verdict always includes the row or `link_ref` and its `generation`, and it never uses the egress mint cache. Each cached value is sealed with the full cache key and the record digest as additional data, through the same input on `pkg/tokenservice/cache`'s `Cipher`, so a replica copying an entry to another key produces a value that fails to open.

## UC-2: The `oauth2Link` source and account linking

This source links a Lenny user to an account at a link provider and keeps the resulting tokens in the Token Service. It owns the link flow, the stored links, refresh, and revocation at the provider, for every kind. An add-on's link kind supplies what differs by provider, under UC-7. Links are keyed on the link provider's name from the start, so an added kind reworks no stored key or route.

### Reuse of §9.3

The design reuses the `pkg/connectoroauth` library (PKCE, signed state, the Redis state store, code exchange, and the refresh call) and leaves the §9.3 connector record and handlers alone. A link is a new record owned by the Token Service. §9.3 does not fit for these reasons:

- `ConnectorDefinition` requires an MCP server. A link provider is not one.
- The §9.3 flow runs from a tool call inside a session. The VCS layer's requirement [UR-1](user-git-identity-design.md#personas-and-requirements) needs a link before a session exists, so the user sees the prompt before the session starts.
- §4.3 scopes connector tokens by environment. A link records a fact about a person at a provider, so a member of two environments would otherwise link twice for one account.
- The connector store is decrypted in the gateway (F-4.3.1). The VCS layer's property [UP-1](user-git-identity-design.md#security-properties) keeps parent tokens away from the gateway.
- Connector refresh has no caller, and no store has a refresh lease.
- The callback is open to the account-link attack in "Defects found while verifying" (F-9.3.18).

### Link providers

A link provider is a tenant object, written through [EG-9](egress-and-git-design.md#proposal-split)'s single write path, with an optional [TS-1](tenant-scoping-identity-design.md#ts-1-environment-scoping) `environments` list. Its generic fields sit at the top level, and the kind block holds what its kind defines. A type names a link provider through `source: oauth2Link`.

```yaml
userCredentialTypes:
  - name: salesforce
    source: oauth2Link
    linkProvider: salesforce-acme
linkProviders:
  - name: salesforce-acme
    kind: oauth2                   # selects the add-on that defines the block below
    delivery: inject               # the delivery ceiling: identifier | none | inject | file
    clientId: "..."
    clientSecret: "..."            # write-only
    scopes: [api, refresh_token]
    identityBinding: unverified
    allowedAccounts: []
    narrowing: null                # optional template
    oauth2:                        # the kind block of the built-in oauth2 kind
      authorizationEndpoint: https://login.salesforce.com/services/oauth2/authorize
      tokenEndpoint: https://login.salesforce.com/services/oauth2/token
      revocationEndpoint: https://login.salesforce.com/services/oauth2/revoke   # optional
      userInfoEndpoint: https://login.salesforce.com/services/oauth2/userinfo   # required
      idField: user_id
      loginField: preferred_username
      apiHosts: [acme.my.salesforce.com]
      attach: "Authorization: Bearer {{secret}}"
```

| Field | Meaning |
| :-- | :-- |
| `name` | The key, unique per tenant. Links and routes name it. |
| `kind` | The kind, which selects the add-on and the schema of the kind block. A write naming a kind no add-on registers is refused. |
| `delivery` | The delivery ceiling, in the token trust record: `identifier`, which keeps the identity and discards the token; `none`, which keeps the token for a check; `inject`; or `file`. The pages show it. |
| `clientId`, `clientSecret` | The tenant's own OAuth2 client. See **Client secret** below. |
| `scopes` | The OAuth2 scopes the flow requests. A kind can fix or default them. |
| `identityBinding` | `directory`, `claim`, `verifiedEmail`, or `unverified`. No default: a link provider write must set it. A kind states which values it supports. See "Identity binding". |
| `allowedAccounts` | The accounts, organizations, or groups a linked account must belong to, checked by the kind. Empty means any account the OAuth2 client admits. |
| `narrowing` | A narrowing template, optional unless the kind requires one. See "Narrowing templates". |
| The kind block | The fields the kind defines, keyed by the kind's name. For `oauth2`: the endpoints, `idField`, `loginField`, `apiHosts`, and `attach`. |

- **Client secret.** `clientSecret` is a write-only value. The Token Service stores it envelope-encrypted under the tenant's Token-Service-only key, in a row keyed on tenant and provider name, with the provider's record digest sealed as additional data. No route returns it, and the Token Service sends it only when that digest matches, under "What the record does not protect". At read time the Token Service compares the row's tenant with the provider's and the caller's, and it refuses a mismatch. No field names a Secret, so a tenant cannot point at another tenant's.
- **Key.** A link is keyed on tenant, user, and link provider name. Each `oauth2Link` type names its own provider, and an add-on that reads a link names the provider it reads, as the VCS layer's `userAuthorization.linkProvider` does.
- **Delivery ceiling.** `identifier` keeps the identity and discards the token, which the VCS layer's independent approval check ([UG-4](user-git-identity-design.md#ug-4-independent-approval-check)) uses, `none` keeps the token in the Token Service for a check, `inject` allows `netagent` memory, and `file` allows the agent's reach. The pages show it, and it is in the record, so a link a person made for a check does not become a delivery source when an admin adds a type.
- **Refresh token.** It never leaves the Token Service (UCP-5). `netagent` receives an access token, or a narrowed one, on the pod credential API.
- **Hosts.** The hosts a type of this source reaches under `delivery: inject` are the provider's `apiHosts`. A binding cannot name another host, and the Token Service re-checks that on the pod credential API. An add-on's host policy can refuse an `oauth2` provider whose endpoints or API hosts sit on hosts it governs, as the VCS layer does, so that kind cannot stand in for an add-on's kind. `apiHosts` on a `kind: oauth2` provider can otherwise misstate where a token works, as a `registered` type's hosts can.
- **Rotation assumption.** The Token Service treats every provider as rotating refresh tokens, which is the single-use rule under "Refresh". A refresh response with no new refresh token keeps the old one. A provider that issues no refresh token gives a link that ends at its access token's expiry and moves to `user_act_required`. A response with no `expires_in` is stored as non-expiring and reported in posture, with `cacheUntil` still applying. A kind can refuse that case at link, as the GitHub kind does.
- **Revocation.** Provider revocation runs when the kind knows a revocation endpoint and the record matches, through the durable record under "Lifecycle and revocation at the provider", and **Deleting a provider** covers deletion. Otherwise Lenny deletes its copy, and the provider's token runs to its own expiry. The tenant admin's write response states this.
- **Identity.** Every kind names a user info source and an identifier field, which the `oauth2` block's `userInfoEndpoint` and `idField` supply, so the confirmation page always names an account, and the unique index on tenant, provider, and `provider_user_id` applies. A kind can widen that index, as the VCS layer does across the providers on one host. A provider with no such endpoint cannot be linked, and a tenant uses `registered` for it. `identityBinding` takes the values under "Identity binding". On `oauth2`, `allowedAccounts` applies through a named claim in that response.
- **Callback.** The flow's callback is per provider, `GET /v1/user-links/callback/{provider}`, and the `state` names the provider. Where a provider returns `iss` (RFC 9207), the Token Service checks it, so a code from one provider cannot be exchanged at another's token endpoint.
- **Endpoints are hostile input.** A kind with fixed endpoint paths fixes them, and `kind: oauth2` cannot. Each endpoint must be HTTPS and pass the [egress design](egress-and-git-design.md)'s check for a tenant-registered public service: resolved at registration, refused on the exclusion list, a registered `ipBlock`, or the cluster's addresses, and re-resolved periodically. The authorization URL also passes the §9.2 domain check. A private provider follows [`tenant-outbound-connector-design.md`](tenant-outbound-connector-design.md): the host is an outbound host, and the calls go through the mint path to it.
- **Route.** The Token Service reaches every link provider endpoint through `lenny-egress` with the tenant asserted, under the mint path's bounds on time, response size, and redirects. The egress design's direct dial for a public host excepts these endpoints, so the decision service sees each call and re-resolution applies. At level 1 the egress design states the equivalent.

A link provider's client belongs to its tenant. An operator's App shared across tenants is out of scope: the only write path is the tenant's own, so each tenant admin would hold a client other tenants' users rely on, and the record's sealed secret would need a platform-only path. Decision 39 records this.

### Stored links

**`user_links`** (`TokenStore`, data class T4 under §12.9, row-level security, tenant guard, and §4.9.1 re-encryption):

| Column | Meaning |
| :-- | :-- |
| `tenant_id`, `user_id`, `link_provider` | The primary key. |
| `link_ref` | A random identifier, regenerated on each link, used in evidence. It carries no meaning outside this row. |
| `provider_user_id`, `provider_login`, `provider_is_admin` | From the provider's user endpoint, read through the kind. `provider_is_admin` is set when the kind can tell. Bounded in length and limited to the provider's login syntax, because a registered host is hostile input. Unique on tenant, link provider, and `provider_user_id`, and a kind can widen that, as the VCS layer does across a host's providers. |
| `trust_digest` | The digest of the provider's token trust record at link time, sealed as additional data on each envelope with tenant, role, user, provider name, and the fields written with the value, under the encoding UC-1 states. A mismatch makes the row unusable and sends it nowhere. |
| `access_token_ct`, `refresh_token_ct`, `scopes` | Envelope-encrypted tokens. |
| `access_expires_at`, `refresh_expires_at`, `generation` | Expiry, and a counter that changes on refresh and relink. |
| `state` | `active`, `user_act_required`, `suspended`, or `revoking`. A mismatch reports `user_act_required` with no write. |
| `refresh_lease_holder`, `refresh_lease_until` | The refresh lease. |
| `verified_at`, `last_used_at`, `created_at` | Times. |

A user with accounts at two providers holds two rows, and Lenny draws no connection between them. A user who wants a different account on one provider relinks, which revokes the old grant first.

Two Lenny users cannot link one provider account within a tenant. The unique index refuses the second with `409 LINK_ACCOUNT_IN_USE`, naming neither user, and the tenant admin can remove the first. A shared account makes `credential.mint` attribution false and gives one person a way around two-person review. The same account in two tenants is permitted, since the tenants are unrelated.

### Identity binding

Nothing else proves that a linked account belongs to the Lenny user. Alice can link a second or machine account and have her agent act under it. Where an add-on reads the link for a decision, such as the VCS layer's repository check and its review rules, that decision then concerns the wrong account. The unique index stops two Lenny users sharing an account and says nothing about one user holding two. A link provider therefore states how a link is tied to a person.

| `identityBinding` | How the link flow decides | Cost |
| :-- | :-- | :-- |
| `directory` | A tenant's sync worker or admin writes the expected `provider_user_id` for a user through `PUT /v1/admin/users/{userId}/provider-identities/{provider}`, under `manage_user_lifecycle`. The link commits only when the provider's numeric identifier matches. | The tenant runs a mapping. The directory must hold provider accounts. |
| `claim` | The link provider names an OIDC claim, such as `github_login`. The start page reads it from the browser login in step 3, and the provider's login must match. The broker contract, or a customer source's claim mapping, gains that mapping. | A broker mapper or a source's claim, and login renames need the mapping to follow. After the first match, the numeric identifier is the key. |
| `verifiedEmail` | The provider's verified email must equal the `email` claim of the browser login. The kind names where it reads verified email addresses, and a kind that cannot read one does not offer this value. | It needs an authoritative IdP email, and the OAuth2 client may need a permission to read addresses. A provider whose administrator can change a user's email weakens it. |
| `unverified` | No check. | UCP-11 does not hold against the user, and neither do the properties an add-on rests on it, such as the VCS layer's account-binding property [UP-9](user-git-identity-design.md#security-properties) and its authorship requirement [UR-4](user-git-identity-design.md#personas-and-requirements). It still stops a prompt-injected agent, which needs no identity claim, and posture reports the value. |

Each binding re-evaluates when its input changes: when a directory row or claim source changes, the link moves to `user_act_required`, at the next periodic check an add-on runs, such as the VCS layer's sweep, and otherwise at the link's next release **[design]**. Lenny reads the claim at the start page alone and does not store it. `Principal` gains nothing **[source]**.

Binding covers the account Lenny uses. It does not cover what the person does at the provider under another account, which the provider's own sign-on and managed-user settings address.

### Flow

The user experience follows Coder's: a named provider instance, self-hosted URLs, and an authorization step before the workspace exists.

1. Alice calls `POST /v1/sessions` with a plan that needs a link she does not have: a binding whose type uses the link provider, or an add-on's requirement, such as the VCS layer's repository check. The gateway answers `403 USER_LINK_REQUIRED` with `details.linkUrl`, before any pod claim. MCP clients receive the same URL as a URL-mode elicitation. She can also call `POST /v1/user-links` with `{"provider": "salesforce-acme"}` from the playground or `lenny-ctl`.
2. `linkUrl` is a Lenny URL holding a random one-time flow identifier, with a 10-minute lifetime, naming the subject that requested it.
3. The browser opens it. The start page requires an OIDC login at the tenant's identity source (`auth.oidc.issuerUrl` by default, and a customer's issuer under the identity source records ([TS-6](tenant-scoping-identity-design.md#identity-source-record-ts-6)) and login routing ([TS-7](tenant-scoping-identity-design.md#login-routing-ts-7)) of [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md)) through that design's "Browser login", whose shared resolver supplies the tenant and subject, and it refuses the flow when the logged-in tenant and subject differ from the flow's. This defeats an attacker who sends a victim a start URL. **Before any redirect**, the page names every endpoint host and the delivery ceiling, and after a token trust record change it states that they changed, naming the old and new hosts. The flow records the current digest, and step 6 refuses if it has moved.
4. The page sets a `__Host-` cookie, `HttpOnly`, `Secure`, and `SameSite=Lax`, holding a random nonce, and redirects to the provider with PKCE and a `state` that signs the flow identifier and the nonce's hash. A `SameSite=Lax` cookie is sent on the provider's top-level redirect back.
5. The provider redirects to `GET /v1/user-links/callback/{provider}`, a path per provider, and `iss` is checked where the provider returns it (RFC 9207). A path whose provider differs from the flow's is refused before any token request. The gateway verifies the signature, consumes the flow once, and requires the cookie's nonce to match. A missing or mismatched cookie aborts the flow. This defeats a victim's browser completing an attacker's flow, in either direction.
6. The gateway passes the code and verifier to the Token Service over mTLS and never sees a token. The Token Service compares the digest, exchanges them through the mint path, reads the provider's user endpoint, applies `identityBinding`, and applies `allowedAccounts` through the kind's check. A refused binding answers `403 LINK_IDENTITY_MISMATCH`, and the token is revoked at the provider at once.
7. The browser shows a confirmation page naming the provider login, the Lenny user, the endpoint hosts, and the delivery ceiling. Alice confirms, and the row commits with its `trust_digest`. An audit event, `user_link.created`, carries the tenant, user, provider name, `link_ref`, and provider login, and it is delivered to Alice as a notification.

`DELETE /v1/user-links/{provider}` lets Alice remove her link, and a tenant admin holding `manage_user_links` can remove anyone's. Neither route returns a token. Impersonation bearers are refused on every link route, using the same check as session create. Each kind's add-on states its endpoints, token lifetimes, and client registration.

### Refresh

A provider can invalidate the old access token and refresh token when a refresh token is used, and the GitHub and GitLab add-ons record that both do **[documented]**. Refresh therefore has one writer per link, and it is a state change rather than a cache fill.

- **Trigger.** A check or mint that finds fewer than 15 minutes on the parent, or a 401 from the host with the parent, requests a refresh. Refresh is on demand, so an idle link costs nothing. A refresh token that lapses at the provider moves the link to `user_act_required`.
- **Lease.** A conditional update sets `refresh_lease_holder` and `refresh_lease_until` (30 seconds) when the lease is free. Session-level advisory locks are unavailable through the §12 transaction-mode pooler, and a transaction open across a provider call would hold a connection, so the lease is a row rather than a lock. Other replicas wait up to 5 seconds by re-reading the row, and otherwise answer `unavailable`.
- **Commit.** The holder calls the provider with a 10-second bound and writes the new tokens and the incremented `generation` before anything else. A store failure after the provider's answer is retried from memory for the lease's remainder.
- **A lost response.** A timeout or a crash between the provider's rotation and the commit loses both tokens. A retry of the old refresh token that the provider refuses moves the link to `user_act_required`. Neither provider's page describes an idempotent refresh, so this cost cannot be designed out. Decision 32 records it, and a metric and alert report the relink rate.
- **Sessions.** Under a narrowing template a session holds a derived token, and the parent never reaches a pod, so rotating the parent cannot break a tool mid-request unless the derived token dies with its parent. If it does, `netagent` sees a 401, flushes, and asks the pod credential API once under the `reauth-on-401` handling. A 401 means the host did not act, so one retry of a buffered request is safe, including a mutation. A streamed write's 401 goes back to the client. The Token Service also caps `cacheUntil` so a derived token is re-requested before the parent's scheduled refresh. With no template, `netagent` holds the parent access token, which `cacheUntil` also caps.
- **`user_act_required`** refuses new sessions with `USER_LINK_REQUIRED` and moves running sessions as a refused verdict does, with the reason `user_act_required`.

### Hostile hosts

A link provider's host is tenant-registered input, as [`tenant-outbound-connector-design.md`](tenant-outbound-connector-design.md#security-properties) states in its property C9, which treats content from a customer network as content from a hostile upstream.

- A kind with fixed endpoint paths takes a host and no OAuth URL. The authorization redirect targets HTTPS only, and the §9.2 URL domain check applies.
- Every Token Service call to a link provider endpoint goes through `lenny-egress`, with the tenant asserted, its CA, and the mint path's bounds on time, response size, and redirects. The egress design's direct dial excepts these endpoints. An add-on declares its kind's token and revocation endpoints among the mint endpoints.
- Connector C8 holds: a registered host cannot shadow a platform-reserved one, so a tenant cannot register a public provider's host behind its own network.
- The provider login and identifier are syntax-limited before they are stored or shown, and a hostile host that answers `allowed` to everything harms only that tenant.
- Caches of derived tokens, any verdict cache an add-on keeps, and refresh leases all key on tenant and `link_ref`, following the egress design's mint cache rule.

### Lifecycle and revocation at the provider

| Event | Stops use | Revokes at the provider |
| :-- | :-- | :-- |
| User deletes the link | At once, by binding change | Yes |
| Relink | Yes | Yes, before the new flow commits |
| `soft_disable`, `hard_disable`, or `full_revoke` ([§11.4](../spec/11_policy-and-controls.md#114-user-invalidation), [TS-4](tenant-scoping-identity-design.md#user-lifecycle-ts-4)) | At once. A new step in §11.4 sets `state = revoking` and pushes binding changes for the user's sessions. | Yes. Reinstate restores no link, and the user relinks. |
| [§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces) erasure, or `DeleteByTenant` | The Article 18 restriction and the drain stop use | Yes, then the row is deleted |
| App or application removed by the user at the provider | At the next check, as a 401 that refresh cannot cure | Already done |
| Link provider deleted | At once for its links. The provider enters `deleting`, keeping it and its record until links drain or `linkProviderDrainSeconds` ends, and a recreate under that name is refused. | Yes, for each link, through the durable record below, under the ordinary comparison. At the bound, or after a mismatch, each remaining row ends with an audit event and a user notification saying its grant was not revoked. |
| A token trust record changes, by any field | At once, by digest comparison at the pod credential API and at each provider call. | **No.** Lenny sends the stored tokens to neither the old nor the new endpoint, and the audit event and the user's notification say so. A revert to the identical record restores use. |

Each "Yes" above applies when the row's digest matches, because a mismatched token is sent nowhere. Revocation runs through a durable record retried with backoff, following the egress design's pending-revocation record for GitLab project tokens, because a host can be unreachable. Erasure has a limit: T4 data must be erased within 1 hour under [§12.9](../spec/12_storage-architecture.md#129-data-classification). The job retries for up to 30 minutes, then destroys the ciphertext regardless, and the erasure receipt records `provider_revocation_failed`. Lenny then holds no copy, and the access token lapses by its own expiry at the host. The tenant admin is told, so the user can revoke the authorization at the provider.

### Narrowing templates

A narrowing template derives a per-session token from the stored one. It is data, so `netagent` and the Token Service hold no provider code for it.

| Field | Meaning |
| :-- | :-- |
| `name` | The template's name. A shipped template's name and contract version, or a tenant template's digest, enter the token trust record. |
| `followsEntryState` | Whether the template narrows, on each mint, to the set an add-on's template variable supplies, and follows changes to that set's state. |
| `request` | The method, the URL, the client authentication, and the JSON body of the call that derives a token, rendered from template variables. |
| Overridable fields | The fields a tenant may override on a shipped template, which the template names, such as the GitHub template's `permissions`. |
| `response` | Where the derived token and its expiry sit in the response. |
| `revoke` | The call that revokes a derived token. |

[`github-credential-addon-design.md`](github-credential-addon-design.md) shows `github-scoped` in full.

- Add-ons ship templates. The GitHub add-on ships `github-scoped`, and the GitLab add-on ships `gitlab-fine-grained`.
- **A kind can restrict its link providers to shipped templates**, with the tenant override limited to the fields each template names, and can require one when the delivery ceiling is `inject` or `file`. The VCS kinds do both, under [`user-git-identity-design.md`](user-git-identity-design.md). A template is optional on a link provider with `kind: oauth2`, where a tenant can define its own.
- **`followsEntryState` is honoured only on a platform-shipped template.** It says the template narrows, on each mint, to the set an add-on's template variable supplies, which for the VCS layer is the session's entries in state `granted`, as [`security-oversight-design.md`](security-oversight-design.md)'s repository rule requires. Only `github-scoped` sets it today. Any other template is treated as outside the egress design's `mint-scoped` class for property [SP11](security-oversight-design.md#security-properties) in [`security-oversight-design.md`](security-oversight-design.md), under which a repository marked for approval receives no write token until its approval exists.
- The built-in variables are `{{parentToken}}`, `{{clientId}}`, `{{permissions}}`, and `{{token}}` for a revocation. `{{apiBase}}` comes from the request layout an add-on registers for the host, and an add-on's template variables add the rest. `{{parentToken}}` is the access token alone, and no variable reaches a refresh token. The set of variables is fixed by this design and the registered add-ons, and a template cannot reference another tenant's data.
- A tenant's override of a shipped template is audited and reported in posture, and the add-on that ships the template states what an override can widen. A tenant's own template on a link provider with `kind: oauth2` is in the record whole, because it can render `{{permissions}}` as a scope.
- A template's URL must stay on origins in the record, because it carries the parent token.
- A template failure refuses the mint, and a response equal to the parent token is a failure. It never falls back to the parent.

**With no template, on a link provider with `kind: oauth2`, the delivered access token has the user's full reach on that provider.** Under `delivery: file` an agent holds all of it. Under `delivery: inject`, `netagent` holds all of it, and request rules are the only limit Lenny applies. A binding write response and the posture read state this.

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
| `secret` | `kind: userCredential` and a `type`. The [egress design](egress-and-git-design.md)'s `secret` becomes a typed union. |
| `hosts` | A subset of the type's hosts. Under `delivery: inject`, `netagent` attaches to these alone. Under `delivery: file`, a write-time check only, as "What `file` does not promise" states. |
| `delivery` | `inject` or `file`, at or below the record's ceiling. `federation` and `metadata` do not apply, and environment variables are never offered. |
| `attach` | Absent. It comes from the type or link provider, in the token trust record, so an edit cannot move the token into a header a host logs. A write of a binding with `delivery: inject` on a `registered` type with no `attach` is refused. Under `delivery: file`, `binding.json` carries it as format metadata, which the `netrc` projection reads. |
| `required` | Whether a session with no resolvable token is refused. Default `true`. |
| `scope` | `onRequest`, or `poolAttach`, which only an add-on's attach point sets, as a VCS pool's `prAuthoring: user` does ([`user-git-identity-design.md`, "The attach rule"](user-git-identity-design.md#the-attach-rule)). `always` is refused, because its cache keys on the tenant pin and it attaches to every session. |
| `requestRules` | Under `delivery: inject` only. See UC-4. A write setting it with `delivery: file` is refused with an error that says why. |
| `lockEgressToHosts` | Under `delivery: file` only, and optional. See "What `file` does not promise". |
| `leakScan` | Under `delivery: file` only, and optional. See "Leak scanning". |

### Limits under both deliveries

**Only the session's own user's token.** The Token Service reads the session row's user and `caller_type` through `pkg/binding`, and it looks up the type for that user. Nothing in the request names a user. The session's environment selects the row when the type has rows per environment, then the no-environment row. An environment row that is mismatched or suspended stops the lookup, so a session does not silently use a different token. `caller_type` is a new field on the session row, set at create from the principal, because only `Principal` carries it today **[source]**. A delegated child's row copies its parent's value, because §8.2 mints no token for a child. An empty value is treated as not `human`.

**Pods that can serve someone else are refused.** A session naming such a binding is refused with `422 USER_CREDENTIAL_UNSUPPORTED_POD` on these pods:

| Pod | Why |
| :-- | :-- |
| `maxConcurrentSessions > 1` | `netagent` cannot attribute a request to a slot, and a file is readable by every slot. |
| Service mode | One process serves several sessions, and §5.2 gives it no session binding. |
| `recycle.enabled: true`, whatever the `recycle.runtimeProcess` value | §5.2 states the scrub "is **best-effort** and is not a security boundary". On a pod whose `recycle.runtimeProcess` is `keep`, the runtime process survives, and any process keeps answering on `netagent`'s loopback port under the next user. A copied token also survives in whatever the scrub misses. |
| A pod that has served a session | The same reason. It catches a pool whose `recycle.enabled` was edited back to `false` with used pods in it. |

These read pool policy at bind. At bind, for a session naming such a binding or declaring a repository under a VCS pool with `userAuthorization: required`, the gateway writes `retireOnRelease` on the claim. **The pod credential API refuses a release unless the claim carries the mark**, so a binding that a mid-session edit turns into a `userCredential` binding delivers nothing until a new session. Each release path honours the mark whatever the pool now says, because `poolRecyclePolicy` reads the current pool **[source]**: `podsession.Binder.Release`, `Server.ReleaseSuspendedPod`, `PodExecutor.Release`, and the scrub reporter's seams. A marked pod is never recycled and never given another slot.

SDK-warm pods remain allowed, because `netagent` receives the binding at bind and no earlier agent session has run. A warm process can read the file once it exists, and that process is this session's runtime.

**Impersonation.** `requireCreatingBearerAllowed` already refuses create, derive, replay, and create-and-start **[source]**. For a session in a delegation tree where any session, read by `root_session_id`, holds a `userCredential` binding or declares a repository under a VCS pool with `userAuthorization: required`, one check, built on `callercheck`, refuses an impersonation bearer with `403 FORBIDDEN` and the reason `impersonation_user_credential`. The tree matters because a child with no lease entry can pass a message on to its parent under a principal with no JTI, which the check does not see. The check reads the tree at input time, so input to a tree before it becomes affected passes:

- **Messages, at the session input layer.** The tree has no single delivery service. `executor.Executor.Send` is the shared call, from `sessionserver`'s `restDelivery`, `mcptools`' `send_delivery.go`, and the `translator` package's `/v1/responses` and `/v1/chat/completions` handlers, and held messages enter through `sessioninbox.Inbox.Enqueue` and its DLQ. A wrapper on each reads the principal from the context, so every adapter inherits the check. `deliverBuffered` runs with no principal and passes, because what it drains passed at enqueue.
- **Other input, at the route.** `resume`, `finalize`, `start`, `upload`, `upload-archive`, `upload-to-session`, tool-use and elicitation answers, and an `inReplyTo` reply, which `inputwait.Registry.Resolve` takes before any `Send`, do not pass `Send`, so `manageSessionRoute` and the MCP session tools apply the check, by default-deny. Exempt routes are `DELETE`, `terminate`, `interrupt`, and reads.
- **Memory, unconditionally.** §9.4 memories persist across a user's sessions, so a write on an unaffected session reaches an affected one later. `handleMemoryWrite`, `handleMemoryDelete`, and `lenny/memory_write` refuse an impersonation bearer on every session.

A test enumerates every mounted mux (`httpsurface.go`, the MCP tools, and each registered `ExternalProtocolAdapter` prefix), and it fails when a route or tool declares no status.

**Sessions with no human user.** UCP-1 needs a human. A session whose `caller_type` is not `human` has no user token. When the binding's own `required` field is `true`, a session naming the binding is refused with `USER_CREDENTIAL_NOT_FOUND`. When it is `false`, the session starts: under `delivery: file` no directory exists, and under `delivery: inject` requests go out unauthenticated and the host answers 401. A human with no usable token gets the same outcomes, with `USER_LINK_REQUIRED` and `details.linkUrl` in place of the not-found code on an `oauth2Link` type, and `details.reason: trust_changed` on a mismatch. A `service` writer does not change that, because `registeredBy` governs writing and the session row governs reading.

**Delegation.** A delegated child carries its parent's user (§8.2 **Child identity.**) and, under the egress design, a child in its parent's environment inherits named bindings. A `userCredential` binding is the exception. A child receives one only when the delegation lease names it, in a new `userCredentialBindings` list in the lease, which a parent can only narrow, and a pool's `poolAttach` does not bypass that. `credentialPropagation` is unrelated, as it governs LLM credentials. A child's pod must pass the pod test, and a pool it cannot pass refuses the delegation. A detached orphan keeps what it was granted until `cascadeTimeoutSeconds`. Under [`tenant-scoping-identity-design.md` decision 14](tenant-scoping-identity-design.md#decisions), a child reached through a sharing path runs in the target environment, and a binding crosses only when the lease names it and the target can see it.

**One binding per host.** A session holds at most one `userCredential` binding per type host, under any delivery, at most one binding of any secret kind with `delivery: inject` per host, and no binding with `delivery: file` beside one with `delivery: inject` on a host. The egress design carries this rule, evaluated on type hosts.

**Audit.** Each release on the pod credential API writes a `credential.mint` record with `tokenSource`, the opaque `credentialRef` or `linkRef`, the binding, its delivery, hosts, and expiry. `tokenSource` is `app`, `userRegistered`, `userOauth2`, or `userChild` on a release, and `userCheck` on a check's use of the parent, which delivers nothing, and [`user-git-identity-design.md`](user-git-identity-design.md) uses the same values. An OAuth2 token or child records a token hash, which joins to a provider's log. A `registered` value records its `generation` and a per-tenant keyed hash, because a value of unknown entropy under a plain hash would correlate across tenants and permit offline guessing. The record carries no user identifier, so §12.8's statement for that stream holds, and the join lives in rows that erasure deletes. The first delivery to a session also writes a `user_credential.delivered` audit event to the tenant's §11.7 chain, naming the session, binding, type, and delivery. That chain is sized for low rates, so later refreshes appear in `credential.mint` alone.

**Provisional state.** On a resume rebuild, a `userCredential` binding is unavailable until `running`, as bindings with `delivery: file` and write tokens are under the egress design.

### Stopping a credential

| Event | Pod credential API | `netagent` | Under `delivery: file`, copies | At the provider |
| :-- | :-- | :-- | :-- | :-- |
| Session end | Refused, as the binding is gone | Flushes, and removes the file | Remain wherever the agent put them | Unchanged |
| `revoke` or `DELETE` | Refused | Binding change: one round trip, or 30 s when a push is lost | Remain | `oauth2Link`: revoked when an endpoint exists. `registered`: Lenny cannot. |
| `expiresAt` passes | Refused | Removed at that time | Remain, and the provider decides | Provider's rule |
| User disabled or erased ([TS-4](tenant-scoping-identity-design.md#user-lifecycle-ts-4), §12.8, [§11.4](../spec/11_policy-and-controls.md#114-user-invalidation)) | Refused | Binding change, and §11.4 ends sessions at `full_revoke` | Remain | As `revoke` |
| A token trust record changes | Refused, as the digest differs | Binding change | Remain | Unchanged, and Lenny sends nothing there |
| `quarantined` | Refused, from each instance's own read | Not relied on. See [`forensics-evidence-preservation-design.md`, "Delivered user tokens"](forensics-evidence-preservation-design.md#delivered-user-tokens). | Remain, and the volume is preserved | Unchanged unless an investigator revokes |
| `userCredentialDelivery` withdrawn, a guardrail in the tenant security record ([SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)), or a pin added on a type host | Refused, through `suspendedBy` | Removes the file | Remain | Unchanged |

## UC-4: Request rules and presets

Under owner decision D1, the provider-specific allowlist is replaced by one generic binding with optional rules held as data. `netagent` carries no provider code for this. A generic HTTP and GraphQL rule evaluator reads whatever rules the pod credential API returns.

**Request rules apply only under `delivery: inject`. They cannot apply to a token the runtime holds, because the runtime sends it to the host itself.**

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
| `default` | What a request matching no rule does. `allow` by default. `deny` selects allow-list mode, which "Allow-list mode" states. |
| `reads` | Under `default: deny` alone: `allow`, the default, admits reads that no rule denies, and `listed` makes a read need an allow rule too. A binding write that sets `reads` with `default: allow` is refused. |
| `rest` | Method and path patterns on the canonical path, below any preset prefix. `*` matches one segment, a trailing `**` matches zero or more, and `{encoded}` declares an encoded segment. A family rule with no method named applies to every method but `GET`, `HEAD`, and `OPTIONS`. Matching is case-insensitive, so a deny rule catches more spellings rather than fewer. |
| `graphql` | The endpoint paths, and top-level mutation names to deny or allow. |
| `bodyPatterns` | RE2 patterns, applied per line to every string value in a JSON body, form value, and query value of a write, and to every string and block string literal in a parsed GraphQL mutation: arguments at any depth, variable defaults, directive arguments, list values, and object values **[measured]**. `denyBodyKeys` applies to the same mutations and to REST writes. |
| Tenant additions | A tenant's rules apply on top of the preset. Deny wins. A binding that sets rules and no `preset` key keeps the default preset, and only `preset: none` in the binding's `requestRules` clears it. |

### What every rule set forces

When a binding has any rule, these apply to its requests. Rows are **[measured]** by the spike's prototype, on a stub that proves forwarding only, except as marked. 3,033 of 3,045 predicted rows matched across the flavours, and the 12 others were harness errors. Path, GraphQL path, and family matching are case-insensitive.

- **Canonical path.** Any `%` outside a declared `{encoded}` segment is refused, on every method. A backslash, `;`, dot segments, and control bytes are refused. Empty segments and trailing slashes are dropped. `netagent` forwards the canonical path, so a host never decodes what a rule did not. A deny rule on `DELETE /rest/api/*/issue/*` therefore also catches `iss%75e`, by refusing it.
- **Final segment.** For a deny rule and a refusal, the final segment is also compared with anything after its first `.` removed, and a dotted deny pattern also compares at each dot. The stem only widens a refusal: a suffixed path never matches an admit, whether an allow rule, a listed GraphQL path, or the uploads path. A path no rule denies keeps its suffix when forwarded. `merge.foo`, `merge.json.foo`, `merge.json.json`, `merge.JSON`, `reviews.x`, and GitLab `approve.foo` were refused, `POST .../issues.json` and `GET .../labels.json` were forwarded with the suffix, and a dot in a repository name is no final segment, so `POST /repos/acme/widgets.js/issues` passed. A generic Rails router sent `merge.foo` to the handler, and Grape answered 404. GitHub's stack is unknown. The direction rule held: `POST /graphql.json`, `.foo`, and `.json.foo`, and `/api/uploads/.../assets.json`, were refused with the exact paths passing, and under `default: deny` an allow rule refused `.json` and `.foo` on `GET` and `POST`. A deny on `/files/report.tar` refused `report.tar.gz` and `REPORT.TAR.GZ` and passed `x.report.tar` **[measured]**.
- **Encoded segments.** In a declared `{encoded}` segment, the value is decoded once, must contain no `%` and no dot segment, and must contain no word on that pattern's `refuseDecoded` list. The encoded form is forwarded as given. `glab`'s `acme%2Fwidgets`, nested groups, and lower-case `%2f` pass, and labels, contents, branches, and tags with escapes pass under the presets' declared segments, and `acme%2Fwidgets%2Fmerge_requests%2F7%2Fapprove` is refused on its route word.
- **The GraphQL and API families, under a preset.** A path in `/graphql*` or `/api/*` is refused unless it is the resolved flavour's GraphQL path or uploads path, or lies under a non-root `restPrefix`, and so is a path whose first segment below the `restPrefix` begins with `graphql`. Under a root prefix `/api/*` has no prefix exception. `/graphql/foo`, `/graphql-tools`, `/graphql2`, `/api/foo`, `/api/graphqlx`, and `/api/v3/graphql` were refused on each flavour. `/repos/acme/graphql-tools/...`, `/repos/acme/graphql`, `/repos/graphql/graphql-js`, `/users/graphql`, and `/search/graphql` reach on `GET`.
- **`POST .../graphql`, and suffixed GraphQL paths.** A `POST` whose final segment, or its stem, is `graphql` is refused unless it is the flavour's listed path, because a host's own routing of such a path is unknown. `POST .../widgets/graphql`, `.../graphql.json`, `.../GraphQL`, and `POST /repos/acme/graphql` were refused, and `PATCH /repos/acme/graphql`, `PUT` and `GET .../widgets/graphql`, and `POST /repos/acme/graphql/issues` passed **[measured]**. A suffixed spelling of a listed GraphQL path is also refused on any method **[design]**, because `PUT /graphql.json` with a denied mutation reached the stub on a generic rule set under the `POST`-only form **[measured]**, and a host could take a mutation in the query of `GET /graphql.json`. `PATCH` and `DELETE` on a repository named `graphql`, a label with that name, and `PUT .../contents/graphql` stay admitted. In GitHub's OpenAPI description, 1.1.4, no path contains `graphql`, and of its 197 `POST` routes 5 end in a parameter, so the one route this can refuse is `POST /orgs/{org}/artifacts/metadata/deployment-record/cluster/graphql`, for a cluster with that name. That check covers GitHub alone. On GitLab, `POST .../repository/files/graphql.json` at a repository root is refused, which is a known false refusal.
- **Writes outside a non-root prefix.** Under a preset whose prefix is not root, any write outside `restPrefix`, `graphqlPaths`, and the uploads path is refused, so a GitLab under a relative URL root has its writes refused rather than unchecked. On GHES, a write to `/repos/.../merge`, `/x`, and a relative root was refused, and `GET /x` passed, and GitLab was the same. On GitLab, `rest` patterns are rooted at `projects/*` below `/api/v4/`, and the unencoded `POST /api/v4/projects/acme/widgets/merge_requests/7/approve` still reached the stub, and Grape answered 404.
- **Method overrides, upgrades, and routing overrides.** `X-HTTP-Method-Override`, `X-HTTP-Method`, `X-Method-Override`, `_method` in a query or a form, `X-Original-URL`, `X-Rewrite-URL`, `X-Forwarded-Path`, and `X-Forwarded-Prefix` are refused, including with an empty value, on preset hosts and a generic rule set. `Upgrade`, `HTTP2-Settings`, `Sec-WebSocket-Key`, and `CONNECT` inside the tunnel are refused on the preset hosts tried. `X-Forwarded-Uri`, `X-Envoy-Original-Path`, and `X-Original-Method` are refused too, and the list is extendable as data. The [egress design](egress-and-git-design.md) refuses the routing override list on every terminated host, with or without rules **[measured]**. `_method` inside a multipart body streams on a generic rule set, which is a stated limit, because parsing it would buffer every attachment. A preset's `writeForm` refuses a multipart write.
- **GraphQL, on a path a rule lists.** It is `POST` alone, and it must parse. **Unparseable GraphQL is refused, and the printed document is forwarded.** The client's document never reaches the host. A bare CR document passed an earlier filter and reads as `addPullRequestReview` under graphql-ruby's default lexer **[captured]**, and which parser a provider runs is unknown. A lexical rule also refuses control characters, U+0085, U+2028, U+2029, and U+FEFF. One operation per document. A mutation's root selections must be fields, whose names are checked with aliases ignored. A subscription is refused. **A `query` document that defines or spreads a fragment on `Mutation`, or names a denied mutation field at any selection, is refused,** including an inline `... on Mutation`, and none of the 166 corpus documents was refused by this rule. A query is otherwise not checked further. The 166 corpus documents all pass the bounds, and the 159 from the earlier run were idempotent under printing.
- **Envelope and JSON, where a body is inspected.** The envelope is an object, `query` is a string, and `variables` is an object, null, or absent, so a top-level array or scalar and string-encoded variables are refused. Decoding is at the token level at every depth. A duplicate key, a case-variant top-level key, invalid UTF-8, a lone surrogate, nesting past the bound, and data after the object are refused. `netagent` re-encodes the decoded tree, with number text preserved.
- **Bounds.** The defaults are 512 KiB per body, 64 KiB per document, 4,096 tokens, selection depth 32 with fragment spreads followed and a cycle refused, value depth 32 counting the leaf, JSON depth 32, 16 root fields per mutation, a 30 s body read, 4 concurrent checks per pod counting inspected requests, with 429 and `Retry-After` past that, and a 100 ms check budget, with 503 past that. They are data, within platform ceilings. The size and depth bounds each passed at their limit and were refused one past it, and body read, concurrency, the check budget, and head read ran on github.com, with no one-past row. The largest corpus values are 698 tokens, selection depth 14, 1 mutation root field, value depth 2, and 7,180 document bytes. The head read deadline is the egress design's listener default, since no host is known before a head is read. Check cost and the rest of the evidence are under "Bounds and cost".
- **Buffering.** Only a request needing inspection is buffered: a listed GraphQL path, a write under `bodyPatterns` or `denyBodyKeys`, and a form write, because `_method` in a form cannot be refused otherwise. Other writes stream, unre-encoded, so a Jira attachment passes. On GitLab a REST JSON write is re-encoded, and a GitHub REST write streams unchanged. A `reauth-on-401` retry applies only to a buffered body. A streamed write's 401 goes back to the client, and `netagent` flushes the token, so the client's own retry gets a fresh one.

### Allow-list mode

A binding whose `requestRules` sets `default: deny` is in allow-list mode. A request passes only when an allow rule admits it, except a read under `reads: allow`, and every deny rule, body pattern, and preset still applies. Deny mode stays the default, under decision 1, and shipped presets are deny lists. Allow-list mode is a tenant's choice for its own binding.

```yaml
requestRules:
  default: deny
  reads: allow                  # allow | listed
  rest:
    - {action: allow, methods: [POST], path: "/rest/api/3/issue"}
    - {action: allow, methods: [POST], path: "/rest/api/3/issue/*/comment"}
```

This binding lets an agent read anything the token reads, create a Jira issue, and comment on one, and refuses every other write.

- **Reads.** A read is a `GET`, `HEAD`, or `OPTIONS` request, or a GraphQL `query` operation on a listed GraphQL path. Under `reads: allow`, the default, a read passes unless a deny rule names it, so a client's discovery and status calls keep working. Under `reads: listed`, a REST read needs an allow rule. GraphQL queries are admitted under either value, within the bounds, as decision 37 states, and a tenant that wants no GraphQL reads lists no GraphQL path, which refuses every request to that endpoint. Decision 40 records the choice of default.
- **Writes.** Every other method needs an allow rule. A listed GraphQL path and a preset's uploads path are not admits on their own in this mode: a mutation needs its root field in `allowMutations`, and an upload needs an allow rule on the uploads path. A mutation with several root fields passes only when every root field is allowed and none is denied.
- **Deny on top.** A deny rule, a `denyMutations` name, a `bodyPatterns` match, and a `denyBodyKeys` key each refuse a request an allow rule admits. Deny wins, as it does in deny mode.
- **Presets.** A preset carries no `default`, so the binding's `default` decides. The default preset on a host still resolves under allow-list mode, and its deny rules and request forms apply on top of the tenant's allow rules. `preset: none` clears it, as in deny mode.
- **Guardrails.** An allow list never satisfies `requestRules.requiredPresets`. The required preset resolves with the allow list, and deny wins, because whether an allow list is narrower than a deny preset is a statement about a host's whole API, which no write-time check can decide.
- **Matching.** "What every rule set forces" applies unchanged. A suffixed path never matches an allow rule, so under `default: deny` an allow rule refused `.json` and `.foo` spellings on `GET` and `POST` **[measured]**. The family rule, the canonical path, and the method override refusals hold.
- **Refusals.** A request refused because no allow rule admits it records the reason `not_listed` in `egress.local`, beside the decision identifier and the operation name, so a tenant can tell a client using an operation it has not listed from an operation a rule denies **[design]**. Posture reports each binding in allow-list mode.

**The trade.** Allow-list mode reverses what deny mode accepts. A provider operation added later, including one with approval or merge power, is refused until the tenant lists it. A client release that starts using an operation the tenant has not listed fails until the tenant lists it, and the tenant maintains the list. Revision 3 of [`user-git-identity-design.md`](user-git-identity-design.md) measured that cost for a shipped allowlist on `gh`, as [`github-credential-addon-design.md`, "Capture history"](github-credential-addon-design.md#capture-history) records.

### Request forms belong to a preset

These are preset properties, so a generic rule set does not inherit a preset's forms. Each add-on states its presets' values.

| Property | Meaning | A rule set with no preset |
| :-- | :-- | :-- |
| `writeForm` | Query strings and body types admitted on a write | Any |
| `encodedSegments` | Segments where `%` is admitted | Those a rule declares |
| `refuseFamilies` | Whether the GraphQL and API family rule applies | No |
| `denyBodyKeys` | JSON keys refused on a write, at any depth | None |
| `undecodableBody` | What a write `bodyPatterns` cannot decode does | Passes, with a metric |

### Headers

**The forwarded-header allowlist is replaced by stripping credential headers only.** `Time-Zone`, sent by `gh` on any image with `/etc/localtime`, made the allowlist refuse every GraphQL request **[measured]**, and a client release can add a header at any time. `netagent` overwrites the `attach` header and removes `Authorization`, `Private-Token`, `Job-Token`, `Deploy-Token`, `X-Api-Key`, `Cookie`, `Proxy-Authorization`, and the host's per-host credential list from the egress design's shipped list. `gitlab-deny` also strips `Sudo` **[measured]**. Every other header is forwarded. **A forwarded header can carry meaning at a host this design knows nothing about, and that is a residual.**

### Canonical forwarding

This rule applies to inspected requests, on any binding with rules: a listed GraphQL path, and a write under `bodyPatterns` or `denyBodyKeys`. Other writes stream, under **Buffering**. The first filter parsed the client's bytes and forwarded those same bytes. A parser difference then becomes a bypass, and one was found. This document passed the filter and reached the stub with the injected token, on both protocols **[captured]**:

```
# x\rquery{viewer(a:"""\nmutation{addPullRequestReview(...){clientMutationId}} #"""){login}}
```

gqlparser, graphql-go, graphql-js 17.0.2, and graphql-ruby's C parser read one query. graphql-ruby 2.6.14's default lexer ends a comment only at LF and reads one mutation, `addPullRequestReview` **[captured]**, because its reading of the bare CR differs. Which parser GitHub runs is **[stub cannot show]**. The corpus also shows other disagreements, including a NUL and a BOM between definitions. A design that has to know the host's parser is the failure here, because that parser is unknown and can change.

**The rule: on an inspected request, the host receives bytes that `netagent` generated from the checked structure. No byte of the client's document, JSON envelope, or JSON body reaches the host's parser.**

- **Document.** `query` is `netagent`'s own printing of the parsed tree, which removes comments and odd whitespace. `netagent` parses the printed text again and requires the same operation kind and root field names. If that fails, it refuses the request. Printed documents read the same under gqlparser and graphql-ruby on all 24 corpus documents **[captured]**, and the 159 documents of the later corpus were all idempotent under printing, with 0 differences under graphql-js and definitions reordered in 6 **[measured]**. `gh` kept the same exit codes and operations with the printer on, on GHES **[captured]**, and the attack table passed with it on **[captured]**.
- **Lexical rule.** It stays beside the printer, because it refuses a suspicious document with a clear message rather than transforming it, and it is cheap. Either one closes the bare CR **[captured]**.
- **Envelope.** It must be an object, with `query` a string and `variables` an object or null. `netagent` writes a new JSON object holding them. It drops `operationName`, which the one-operation rule makes redundant. `Content-Type` and `Content-Length` are its own.
- **Variables and REST bodies.** The input-key check described here is gone with the allowlist, and the duplicate-key rule stays, because `bodyPatterns` read those values. The prototype copied `variables` verbatim, and that left the open defect: `{"i":{"pullRequestId":"P","state":"CLOSED"},"i":{"pullRequestId":"P"}}` passed the `updatePullRequest` input check and reached the stub, because the filter read the last value **[captured]**. A first-wins origin would close the pull request. Ruby's standard JSON reads the last value **[captured]**, and GitHub's reader is **[stub cannot show]**. The effect stops at a listed mutation's fields, so it never reached approval or merge. The JSON rule now refuses a duplicate key at every depth, and `netagent` re-encodes the decoded tree, so the host sees one value whatever it would have chosen **[measured]**: `{ "a" : 1e2 , "b" : "A\/x" }` reached the stub as `{"a":1e2,"b":"A/x"}`, with number text preserved.
- **URL and query string.** `netagent` forwards the canonical path, so a host never decodes what a rule did not. Any escape outside a declared encoded segment is refused on every method, empty segments and trailing slashes are dropped, the final segment is also compared with anything after its first `.` removed for a refusal, and under a preset an unmatched `/api/*` or `/graphql*` path is refused **[measured]**. "What every rule set forces" states the rule. A preset's write form can admit no query string. On `GET` and `HEAD`, a query string is rebuilt from parsed pairs.
- **Framing.** `netagent` writes its own HTTP/1.1 or HTTP/2 upstream.

Canonical forwarding costs 1.9 µs on `pr create`, 44 µs on the largest `gh` document, and 14 ms on the token document **[captured]**. Printing is also what the first out-of-memory kill came from, so the bounds below come first.

**Check cost under the final preset [measured],** at 2 CPUs and 200 iterations, so noisier than the earlier table: `pr create`, 353 B, 19.6 µs and 11 KB; the `largest` case, 2,388 B, 120 µs and 106 KB; a 65 KB PR body, 600 µs and 584 KB; 258 KB of `<`, 2.7 ms and 1.7 MB; 4,096 tokens, 1.09 ms and 1.4 MB; a 512 KiB body, 3.65 ms and 5.2 MB; 40,000 JSON nodes in variables, 10.5 ms and 7.5 MB; and REST `GET`, 2.1 µs. An earlier run gave a 24% to 49% rise over the allowlist on `gh` documents, and paired end-to-end, a p50 difference from an unfiltered host of 0.50 ms at 209 B, 1.48 ms at 2,388 B, and 2.45 ms at 65,784 B, with `memory.peak` at 41.7 MB under a 15,000 token limit. `memory.peak` under the final bounds has not run. A separate result stands: 65,536 `<` characters print to more than 256 KiB, and 512 KiB admits them.

### Bounds and cost

Every one of these came from a defect or a measurement in the capture. The size and depth bounds each passed at their limit and were refused one past it on GHES, github.com, and GitLab, and the fragment chain ran 15 plus 15 against 20 plus 20. Body read, concurrency, the check budget, and head read ran on github.com, with no one-past row **[measured]**. They are data defaults on any binding with rules, within platform ceilings.

| Bound | Value | Why |
| :-- | :-- | :-- |
| Body, inspected requests | 512 KiB | `gh` JSON-encodes with HTML escaping, so `<` costs 6 bytes. The prototype's 256 KiB bound refused 44,000 `<` (264,352 bytes), 65,536 `<` (393,568 bytes), and 65,536 emoji (262,496 bytes) **[captured]**. 65,536 characters is GitHub's body limit as recalled, unverified (spike [S15](github-credential-addon-design.md#validation-owed) of the GitHub add-on). 512 KiB admits the worst `gh` encoding of that limit. |
| Document, within the body | 64 KiB | The largest `gh` body in the capture is 2,388 bytes **[captured]**. A PR body travels in variables, so this bound does not limit it. |
| Tokens | 4,096 | The largest of 166 corpus documents has 698 by the parser's lexer **[measured]**. The prototype had no token limit, and a 256 KiB document of tokens cost 36 ms and 40 MB. |
| GraphQL selection depth | 32, with fragment spreads followed and a cycle refused | The deepest corpus document has 14 levels **[measured]**. An 80 KB document nested 40,000 levels killed the prototype's printer at a 256 MiB limit, which stops egress for the whole pod **[captured]**. |
| GraphQL value depth | 32, counting the leaf | The deepest corpus document has 2 **[measured]**. |
| JSON depth | 32 | The same defect class, through `variables`. 33 and 40,000 were refused **[measured]**. |
| Root fields per mutation | 16 | The corpus has 1, and `gh`'s project batch sends one per project change. A query has no root bound, and 200 passed **[measured]**. |
| Head read, on `netagent`'s listeners rather than per rule set | 10 s, with a bare LF refused at once with 400 | A request with bare LF line endings got no answer, and the prototype held the connection for 20 s with no deadline **[captured]**. A partial head got 400 at 10.0 s **[measured]**. A bare LF head also waited 10.0 s, because the prototype does not implement "at once", so that part is **[design]**. |
| Body read | 30 s | A body at 2 KB/s got 408 **[measured]**. |
| Concurrent checks per pod | 4 inspected requests, then 429 with `Retry-After` | Holds buffered bytes to 2 MiB. With 6 slow bodies, 4 got 200 and 2 got 429 **[measured]**. |
| Check time | 100 ms budget, refused with 503 past it | The parser cannot be preempted, so the bounds above are what limit it, and this catches a defect. It was never reached at 100 ms, and the worst case benchmarked is 10.5 ms **[measured]**. |

**Against `netagent`'s memory.** The egress design's example request is 64 Mi. Measured `memory.peak` was 15.5 MiB after `pr create`, 17.9 after the largest `gh` document, and 18.9 after a 258 KB body **[captured]**, cumulative in that order on one process. The 256 KiB token document took it to 74.9 MiB **[captured]**, past that request, which is the case the token and document bounds remove. With a linear extrapolation from the measured 5.5 ms and 1.7 MB at 258 KB, a 512 KiB body costs about 11 ms and 3.4 MB, about double that with printing and re-encoding, and four at once stay under 30 MiB of transient allocation. Scaling the measured costs by token count puts a document at the token bound near 1 ms and under 2 MB. These figures are estimates **[design]**, and S9 measures them. The chart sets `GOMEMLIMIT` on `netagent`, so the collector works before the limit.

| Document | Check alone | End to end, paired, on 1 CPU |
| :-- | :-- | :-- |
| `pr create`, 353 B | 11 µs, 6.8 KB | Below noise |
| Largest `gh`, 2,388 B | 88 µs, 78 KB | 0.06 ms |
| 65,784 B, a full PR body | 606 µs, 559 KB | 0.94 ms |
| 258,185 B of escapes | 5.5 ms, 1.7 MB | 4.2 ms |
| 260,023 B of tokens (now refused) | 36 ms, 40 MB | 17 ms |

All of these are **[captured]** on a laptop under other load, so small differences are not resolved. The binary grew from 10.87 MB to 11.41 MB.

**What the user sees past a bound.** `gh` exits non-zero with `HTTP 413: refused by request rules: body of N bytes is over the 524288 byte bound` and nothing is written **[captured for the format, under the allowlist's wording]**. An ordinary body stays far under it. Past the host's own limit the host's error applies.

### Presets

Presets ship as data with the add-on that defines them, as the egress design's provider templates do. A preset has a name, a version, the API flavours whose request layouts give its paths, the properties above, and rules. Rules reach `netagent` resolved, on the pod credential API, so a preset update reaches a pod at its next `cacheUntil` with no image change. `handling` names the rule evaluator's minimum version, so an image older than a rule's format refuses the credential.

```yaml
- name: example-deny
  version: 1
  flavours: [example]                  # request layouts an add-on registers
  repoRoots: [/projects/*]             # optional roots that rules name as {repo}
  writeForm: {query: false, bodies: [json]}
  rest:
    - {action: deny, methods: [POST], path: "{repo}/approve"}
```

A binding names a preset in `requestRules.preset`, or an add-on's request layout point supplies a default for a host, as the [VCS layer](user-git-identity-design.md) does on a VCS pool's API host. Where neither applies, no preset applies. A named preset's family and the host select its flavour through the add-on that ships it, and no field can select another.

**Keeping presets current.** Each add-on runs a CI job that diffs its provider's published API descriptions on a schedule and raises a preset change for review when a new name matches one of its preset's families. The window in which a new operation passes is the schedule, the review, and the time to the next `cacheUntil`.

### Who can change a preset

Under the owner's decision D1, recorded as decision 1, a tenant can change it.

| Action | Who | Conditions |
| :-- | :-- | :-- |
| Set rules, or clear a preset with `preset: none` | `tenant-admin`, through [EG-9](egress-and-git-design.md#proposal-split)'s write path | An audit event names the old and new rules. Posture reports each binding's preset and any cleared preset. |
| Require a preset | `security-admin`, through a new [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) guardrail `requestRules.requiredPresets`, by host pattern with a minimum version | [EG-9](egress-and-git-design.md#proposal-split)'s check evaluates the resolved form, at a binding write and at a VCS pool write. A binding breaking it is refused, or `suspendedBy` when the guardrail arrives later, so a pin to an old version does not satisfy it. The pod credential API refuses a resolved rule set lacking a required preset recorded on the binding. Relaxing it is a weakening under [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)'s delay. |
| Ship or update a preset | The platform, in the chart | A binding that sets `version` stays there, above any guardrail minimum, and others follow the latest. |

## UC-5: `delivery: file`

### Gate

A new per-tenant platform-admin grant, `userCredentialDelivery`, is its own risk class under the [egress design](egress-and-git-design.md)'s **Gates** paragraph, separate from `agentReadableDelivery`. That class holds tenant credentials, and this one holds a person's. Granting one grants nothing in the other. A tenant admin then opts in per binding by writing `delivery: file`, and the token's record must carry a delivery ceiling of `file`, on the type or the provider. `security-admin` can forbid it through [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)'s `credentialDelivery.forbid`, in which the value `file` covers every binding with `delivery: file`, of any secret kind, and the value `userCredential.file` covers `userCredential` bindings alone.

### Mechanics

This reuses the egress design's `file` mechanics.

- `/run/netagent/credentials/<name>/value` holds the raw value with no trailing newline, and `binding.json` holds the name, hosts, `attach`, `expiresAt`, and a version counter. `LENNY_CREDENTIALS_DIR` names the directory.
- `netagent` swaps the directory atomically, and the mode is 0444 in an emptyDir that only `netagent` writes and the runtime container mounts read-only.
- `projections` work as defined: `netrc`, from an `attach` Basic template as metadata, and a deployer's file templates.
- `netagent` gets the value on the pod credential API under the current binding check: pod token, peer address, binding state, session user, and pod model, with the decision service as the second gate.
- `netagent` refreshes at `cacheUntil`, 300 seconds for this kind.

Environment variables stay never offered. That design's reasons hold: `/proc/<pid>/environ`, child inheritance, diagnostic reports, and no rotation.

### What `file` does not promise

| What it does not promise | What can be done | Status |
| :-- | :-- | :-- |
| **The token can leave through any allowed host.** An agent reads a file and sends it anywhere the policy allows, and an opaque tunnel hides that. | `lockEgressToHosts: true` limits the session's effective allow set, and every descendant's, recorded on its session row as `egressLockHosts`, to the binding's hosts and the base destinations, at the cost of everything else that session wanted to reach. It is refused on a pool with `egressProfile: internet`, whose raw sockets bypass it, including at delegation, and a binding with `delivery: file` on such a pool is reported in posture. It is also refused on a pod with `maxConcurrentSessions > 1` or in service mode, including at delegation, because `netagent` cannot apply it to one slot. **It covers egress alone:** a connector tool argument, a message to a child, and a workspace export leave through gateway channels, which `leakScan` alone reads. Bytes-out alerts under [EG-12](egress-and-git-design.md#proposal-split) detect volume and not a 40-byte token. | **[design]** |
| **`hosts` is enforceable only as a check against the allow set at write time.** `netagent` attaches nothing, so it never decides where the value goes. | The write rejects a host outside the type's hosts or the tenant's effective allow set, and `binding.json` carries the rest as a statement of intent. `lockEgressToHosts` is the enforcing form. | **[design]** |
| **Removing the file does not invalidate copies.** | Prefer a short `expiresAt`, a narrowing template, and `oauth2Link` with revocation. Rotation after exposure is the tenant's action. | **[design]** |
| **Lenny cannot revoke a `registered` token at the provider.** It knows nothing about the provider. | See "Exposure records". The tenant's backend is the party that can. | **[design]** |
| **The agent can do whatever the token permits.** Rules do not apply. | Choose a token whose own scope fits. The type's documentation states this. | Stated |
| **The token can reach transcripts, LLM traffic, workspace exports, checkpoints, and forensic evidence.** Anything the agent prints or stores carries it. | "Leak scanning" below narrows it. Nothing removes it. A session holding a binding with `delivery: file` is marked `agentHeldCredential`, which posture reports, and which a tenant can use to shorten that session's retention. | **[design]** |

### Exposure records

§25.5's events are best effort **[source]**, so they cannot carry this. Each generation of a value released under `delivery: file`, of either source, writes a durable exposure row in the Token Service's store, with the `credentialRef` or `linkRef`, `generation`, the writer's `tokenId` when there is one, the type, and the session. The row is opened at release, and it becomes actionable at the session's end or at the next rotation. A §25.5 `user_credential.exposure_ended` event notifies the tenant, and `GET /v1/admin/user-credential-exposures` lists rows not yet acknowledged, so a missed event is recoverable. `POST .../{id}/ack` closes one. An alert fires on rows left open past a tenant-set age. A backend can also register one short-lived token per session, from its handling of session events. Erasure pseudonymizes an open row and keeps it until it is acknowledged, because a person's departure does not end a live token.

### Leak scanning (UC-6)

This is optional, per binding (`leakScan: on`), and absent under `delivery: inject`, whose value the pod does not hold.

- The Token Service gives the gateway replica coordinating a session that session's current value over mTLS, checked against the session's coordinator record, the record digest, and a delivery ceiling of `file`, with an audit event per release. It is held in memory for the session. **A replica can acquire any session's lease through REG-COORDLEASE, so that check does not bound its reach:** a compromised replica can obtain the value of any `leakScan` session in the deployment, across tenants. That is the cost of the option.
- A new built-in interceptor scans `PostAgentOutput`, `PreToolResult`, `PreExportMaterialization`, `PreMessageDelivery`, `PreDelegation`, `PreConnectorRequest`, and proxy-mode `PreLLMRequest` for the value and its base64 and URL-encoded forms, and it redacts and raises an event.
- The collector and adapter of the agent activity streams ([FE-6](forensics-evidence-preservation-design.md#fe-6-agent-activity-streams)) do not receive these values, because the collector is a node-level process shared across tenants. Its structural and entropy layers apply as stated there.

Its limits are stated: a direct-mode LLM call, an opaque tunnel, a re-encoded or split value, and checkpoints are not scanned. A checkpoint can hold a copy, which is why §12.9 applies and why `agentHeldCredential` exists.

## UC-7: Add-ons

An add-on adapts this mechanism to a provider family without changing it. It is a package compiled into the components that run its code, the Token Service, the gateway, and `netagent` where it needs code there, registered under one or more kind names, and shipped with its data: templates, presets, and request layouts. No add-on loads at run time. The `oauth2` kind is built into this design and serves any OAuth2 provider that needs no code of its own. [`user-git-identity-design.md`](user-git-identity-design.md) defines the VCS layer that the GitHub and GitLab add-ons share, [`github-credential-addon-design.md`](github-credential-addon-design.md) defines the `github` and `github-enterprise` kinds, and [`gitlab-credential-addon-design.md`](gitlab-credential-addon-design.md) defines the `gitlab` kind.

### Kind blocks

A link provider's `kind` selects an add-on, and its configuration block, keyed by the kind's name, holds the fields that kind defines:

```yaml
linkProviders:
  - name: github-acme
    kind: github
    delivery: inject
    clientId: "..."
    clientSecret: "..."
    identityBinding: directory
    allowedAccounts: [acme]
    narrowing: {name: github-scoped, permissions: {pull_requests: write, contents: read}}
    github:                        # schema registered by the GitHub add-on
      host: github.com
```

- The add-on registers the block's schema. [EG-9](egress-and-git-design.md#proposal-split)'s write path validates the block against it, and refuses a block keyed by any other kind and a kind no add-on registers.
- The whole block is in the token trust record, except the fields the add-on exempts, each with a stated reason. A field an add-on adds later is in the record unless the add-on exempts it.
- A kind fixes what its block does not set. `oauth2` takes its endpoints, `apiHosts`, and `attach` from its block. A kind with fixed endpoints derives them from its block, such as from a host.
- A shipped narrowing template names its overridable fields, and the override sits in the link provider's `narrowing`. Those fields are in the record unless the template exempts them.
- Types, bindings, and presets carry no kind block today. An add-on that needs one there adds it through this design's write path, under the same rules.

### Extension points

Each extension point is a named interface this design calls at a fixed moment. An add-on implements the points it needs, and a point with no implementation takes the default below.

| Point | Called | What an add-on supplies | Default |
| :-- | :-- | :-- | :-- |
| Link kind | Link flow, refresh, user info, revocation | Endpoints, `apiHosts`, `attach`, the account fields of the user info response, the `allowedAccounts` check, the verified email source, token response rules such as refusing a non-expiring token, and the `identityBinding` values it supports | `oauth2` reads all of these from its block |
| Template variables | Narrowing, at each mint | Variables a shipped template can render, such as a session's repository set and target, and whether that set follows entry state | The built-in variables alone |
| Release check | Token Service, before its cache lookup on each release on the pod credential API and each mint for a session the add-on governs | A verdict, `allowed`, `refused`, or `unavailable`, with a reason | No check |
| Request layout and default preset | Rule resolution at the pod credential API, and binding, pool, and preset writes | For a host: the API flavour, `apiBase`, `restPrefix`, the GraphQL paths, the uploads path, and the default preset | No preset unless the binding names one |
| Host policy | Binding write, bind, and the pod credential API | Refusal or suspension of bindings whose type hosts include a host the add-on governs, refusal of an `oauth2` provider on such a host, and hosts to pin | None |
| Attach | Session bind | Bindings to attach to a session that did not name them, with `scope: poolAttach`, and the bindings each replaces | None |

These rules hold for every add-on:

- **An add-on narrows and never widens.** A release check and a host policy can refuse what this design admits, and nothing an add-on supplies admits what this design refuses. An attached binding passes every check a named binding passes.
- **Points fail closed.** A release check that errors gives `unavailable`, and a host policy that errors refuses the write or suspends the binding.
- **Data travels on the pod credential API.** Templates, presets, and request layouts reach `netagent` resolved, so an add-on's data update needs no image.
- **Testing is split.** This design tests each point against a test kind with no provider, and each add-on tests its own implementation against its provider.

## Configuration by scope

| Setting | Scope | Who sets it | Default |
| :-- | :-- | :-- | :-- |
| `userCredentialDelivery` grant | Per tenant | `platform-admin` | Absent |
| `userCredentialTypes` | Tenant, with optional [TS-1](tenant-scoping-identity-design.md#ts-1-environment-scoping) `environments` | `tenant-admin` | None |
| `hosts`, `attach`, and the delivery ceiling, on a `registered` type | Type | `tenant-admin`, and a change makes its rows unusable | None, none, and `inject`. A write must set `hosts`, and a binding with `delivery: inject` needs `attach`. |
| `linkProviderDrainSeconds` | Deployment | Platform (Helm) | 86,400 |
| `registeredBy`, `maxBytes`, `requireExpiresAt`, `maxLifetimeSeconds` | Type | `tenant-admin` | `[self, service]`, 16 KiB, `false`, unset |
| `maxBytes` ceiling | Deployment | Platform (Helm) | 64 KiB |
| `linkProviders`, with optional [TS-1](tenant-scoping-identity-design.md#ts-1-environment-scoping) environments | Tenant | `tenant-admin` | None. A write must set `kind`, `clientId`, `clientSecret`, `identityBinding`, and the kind block. |
| `delivery` on a link provider | Link provider | `tenant-admin`, and a change makes its links unusable | `none` |
| The `oauth2` kind block: endpoints, `idField`, `loginField`, `apiHosts`, and `attach` | Link provider | `tenant-admin` | None. A write must set `userInfoEndpoint`, `idField`, `apiHosts`, and `attach`. |
| Another kind's block | Link provider | `tenant-admin`, under the schema the kind's add-on registers | As that add-on states |
| `allowedAccounts` | Link provider | `tenant-admin` | Empty |
| `tokenTrust.allowedRecords` | Tenant security record | `security-admin`, under [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)'s delay | Absent |
| `identityBinding` | Link provider | `tenant-admin`, and [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) can forbid `unverified` | None. A write must set it. |
| `narrowing`, and a shipped template's overridable fields | Link provider | `tenant-admin`, within what the kind admits | None on `kind: oauth2`. A kind can require one. |
| `delivery`, `required`, `lockEgressToHosts`, `leakScan` | Binding | `tenant-admin` | `inject`, `true`, `false`, off |
| `requestRules` | Binding, under `delivery: inject` only | `tenant-admin`, and [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture) can require a preset | An add-on's default preset for the host at the pod credential API, unless `preset: none`, and otherwise none |
| Presets | Deployment | Platform (chart) | Shipped |
| Rule bounds, and the routing override header list | Helm, with data within ceilings | Platform | As under UC-4 |
| `userCredentialBindings` | Delegation lease | Client, within the parent's | Empty |
| `manage_user_credentials` | Role | `tenant-admin` | Held by `tenant-admin` |
| `credentialDelivery.forbid: userCredential.file` | Tenant security record | `security-admin` | Absent |
| `requestRules.requiredPresets`, with a minimum version | Tenant security record | `security-admin` | Absent |

Every non-spec default is operator-tunable and documented as such. Each add-on states its own settings.

## Worked examples

### A Jira token as a file

The platform admin grants `acme` `userCredentialDelivery`. Carol defines type `jira`, `source: registered`, `hosts: [acme.atlassian.net]`, a delivery ceiling of `file` (`delivery: file` on the type), `registeredBy: [service]`, with `requireExpiresAt: true`, and a binding with the owner's record and `delivery: file`.

1. `acme`'s backend, under a `credential-writer` role, calls `PUT /v1/admin/users/alice@acme.com/credentials/jira`, with a token that expires tonight and a `tokenId`. An audit event names the writer. **[design]**
2. Alice creates a session naming the binding, on a warm pool with `maxConcurrentSessions: 1` and no recycling. The gateway checks the pod and that the caller is not an impersonator, and it writes `retireOnRelease` on the claim. **[design]**
3. `netagent` asks the pod credential API. The Token Service reads Alice from the session row, checks the claim's mark, reads the row, compares its trust digest, checks expiry, asks `lenny-egress-authz` for the scope decision, and answers. `credential.mint` and `user_credential.delivered` are written, and an exposure row opens. **[design]**
4. `netagent` writes `/run/netagent/credentials/jira/value` and `binding.json`. The runtime reads it with its own client. **[design]**, on mechanics **[measured]** in the egress rounds.
5. The backend rotates at noon. The binding change reaches `netagent`, the file swaps, and the morning generation's exposure row becomes actionable. **[design]**
6. An injected agent sends the token to an allowed host it should not have. Lenny does not see this. If the binding sets `lockEgressToHosts: true`, the CONNECT is refused. At session end, `acme`'s backend receives `user_credential.exposure_ended`, or lists the open rows, and it revokes each generation at Atlassian by its `tokenId`. **[design]**

### The same token under `delivery: inject`

Carol changes the binding's `delivery` to `inject` and lowers the type's delivery ceiling to `inject`, so the backend writes again, and the type's `attach` is `Authorization: Bearer {{secret}}`. No grant is needed. The agent never reads the token, the tool sends a placeholder, and `netagent` terminates TLS for `acme.atlassian.net`, as the [egress design](egress-and-git-design.md) describes. Carol adds `requestRules` with `default: allow` and a deny rule for `DELETE /rest/api/*/issue/*`, and `netagent` refuses that one with a decision identifier. With no preset, a Jira attachment upload still streams, and a write with a query string still passes. **[design]**

### A generic OAuth2 link

Initech defines `salesforce` on a link provider with `kind: oauth2` and `delivery: inject`. A session naming the binding with no link gets `USER_LINK_REQUIRED` and a `linkUrl`, and the UC-2 flow runs, with a confirmation page naming Erin's Salesforce login and the delivery. No template exists, so `netagent` holds Erin's access token with her full reach, as the write response said. The Token Service refreshes under a row lease. An admin at `initech` edits `tokenEndpoint`, and the sealed `clientSecret` is unusable until a write re-supplies it. Every link's digest now differs, and nothing is sent to either endpoint. Erin's next session offers a relink, and the page before the redirect names every endpoint host and says they changed. Erasure deletes the row and calls `revocationEndpoint` when the record matches. **[design]**

### Refusals

- An operator calls `POST /v1/sessions` under an impersonation bearer for Alice: refused, as today.
- An operator uses `lenny/send_message`, `POST /v1/responses`, `upload`, or `start` on Alice's session under an impersonation bearer, or writes to her memory on any session: refused with `impersonation_user_credential`.
- An operator calls `PUT /v1/credentials/{ref}` under that bearer: refused.
- `acme` writes a link provider, and no field exists to name `globex`'s client secret.
- A session on a pool with `recycle.enabled: true` names the binding: refused with `USER_CREDENTIAL_UNSUPPORTED_POD`.
- A service caller names a binding whose `required` field is `true`: refused with `USER_CREDENTIAL_NOT_FOUND`.
- Alice's agent delegates to a child with no `userCredentialBindings`: the child has no file, and its requests carry no token.
- Carol deletes type `jira` and recreates it with another host: the new type has a new `typeId`, and it holds no rows.
- Carol raises a type's ceiling to `file`: stored rows are unusable until a new write.
- Carol writes a link provider with `kind: github` and a `gitlab:` block: refused at write, because the block does not match the kind.

## Changes requested of other designs

### `egress-and-git-design.md`

Applied in its revision 51, as hook rows under "Reserved for other designs" or in the text they govern: the typed `secret` union with `userCredential`, unknown kinds read as absent, the `userCredentialDelivery` gate, its exceptions to the egress security properties [P2](egress-and-git-design.md#security-properties) and [P3](egress-and-git-design.md#security-properties), which keep long-lived credentials out of pods and egress credentials away from the agent, the request inspection stage with `requestRules` and replacement, `handling`, `cacheUntil`, the one-binding rule on type hosts across deliveries, `scope: poolAttach`, the decision service honouring a link provider's endpoints on the mint path, the delegated child exception, `lockEgressToHosts` in the session formulas and on descendants, `caller_type` from `pkg/binding`, a VCS pool as a source of pins, `suspendedBy` naming a gate, a pin, or a VCS pool, a refusal of differing flavours on one host, an expected route and CA on the pod credential API with the route asserted on the CONNECT and in the level 2 contract, a `handling` value and a mint refusal for a session that `allowRuntimeHeldCredentials` applies to, and no CA or `attach` on a `userCredential` binding. Its git path text now states git path T, including what happens to a held token on covered and uncovered hosts.

### The VCS layer and its add-ons

[`user-git-identity-design.md`](user-git-identity-design.md) configures this mechanism for VCS hosts, and its revision 28 moved the link flow, the stored links, and the canonical forwarding and bounds evidence here. [`github-credential-addon-design.md`](github-credential-addon-design.md) and [`gitlab-credential-addon-design.md`](gitlab-credential-addon-design.md) hold the kinds, templates, presets, and request layouts that this document's earlier revisions held.

### Others

| Design | What it says now |
| :-- | :-- |
| [`security-oversight-design.md`](security-oversight-design.md) | The `userCredential.file` value of `credentialDelivery.forbid`, `requestRules.requiredPresets` with a minimum version, `tokenTrust.allowedRecords`, `allowRuntimeHeldCredentials`, oversight of the grant and of preset changes, `followsEntryState` on shipped templates, and posture fields. |
| [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md) | Delivered tokens in evidence, `quarantined` stopping release on the pod credential API, the limits of rotation, and volumes holding a file. |
| [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) | Types and link providers scoped by environment, delegation, `poolAttach`, and user lifecycle. |

| [`tenant-outbound-connector-design.md`](tenant-outbound-connector-design.md) | An OAuth2 link to a private host. OR-3 covers the Token Service's calls to a link provider, which follow C6, C8, C9, and C10, with no design change. |

## Dependencies

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| UC-1 (types, `registered`, the token trust record) | [EG-9](egress-and-git-design.md#proposal-split) | The single write path. It builds the digest format and `token_trust_records`, which UC-2 uses. It can land with no delivery. Closes F-4.9.46 and the `/v1/credentials` half of F-13.3.26. |
| UC-2 (`oauth2Link`, the link flow, and stored links, whose browser binding addresses F-9.3.18) | UC-1, [EG-1](egress-and-git-design.md#proposal-split), [EG-2](egress-and-git-design.md#proposal-split)'s mint path, [EG-9](egress-and-git-design.md#proposal-split), [TS-6](tenant-scoping-identity-design.md#identity-source-record-ts-6) for the login path, and [OC-1](tenant-outbound-connector-design.md#proposals) and [OC-2](tenant-outbound-connector-design.md#proposals) for a private host | The digest format and `token_trust_records`, the single write path, and the route to every link provider endpoint. It keys on a provider name, so an added kind needs no rework. |
| UC-3 (`userCredential` binding, `inject`) | UC-1, [EG-4](egress-and-git-design.md#proposal-split), [EG-2](egress-and-git-design.md#proposal-split) with the requests above, and [TS-4](tenant-scoping-identity-design.md#user-lifecycle-ts-4) for the invalidation level | Bindings, the pod credential API, and `pkg/binding`. Addresses the session input half of F-13.3.26. It also builds the session input check and `retireOnRelease`, which [UG-2](user-git-identity-design.md#ug-2-repository-authorization) uses. |
| UC-4 (request rules and presets) | UC-3, and the [EG-4](egress-and-git-design.md#proposal-split) stage | The stage. |
| UC-5 (`delivery: file`) | UC-3, and [EG-4](egress-and-git-design.md#proposal-split)'s `file` delivery | The mechanics. It needs the grant under [EG-9](egress-and-git-design.md#proposal-split). The [VCS layer](user-git-identity-design.md)'s `allowRuntimeHeldCredentials`, with its refusal and attach rule, builds on it. |
| UC-6 (leak scanning) | UC-5 | Optional. |
| UC-7 (add-ons) | UC-2, UC-3, and UC-4 | The kind block, the registry, and the extension points those proposals call. It can land with `oauth2` alone. |

No `EG-` proposal depends on a `UC-` proposal, and the hooks sit inside [EG-1](egress-and-git-design.md#proposal-split), [EG-2](egress-and-git-design.md#proposal-split), [EG-3](egress-and-git-design.md#proposal-split), [EG-4](egress-and-git-design.md#proposal-split), [EG-7](egress-and-git-design.md#proposal-split), [EG-9](egress-and-git-design.md#proposal-split), and [EG-12](egress-and-git-design.md#proposal-split). The VCS layer's UG proposals depend on UC-2, UC-3, UC-4, and UC-7, and each add-on's proposals depend on the VCS layer, as those documents state. [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)'s new guardrails depend on UC-1 for `tokenTrust`, with UC-2 for link providers, UC-4 for `requestRules`, and UC-5 for `userCredential.file`. [TS-1](tenant-scoping-identity-design.md#ts-1-environment-scoping) scopes the objects here when both have landed, and neither waits for the other.

## Specification sections affected

- [§4.2 Session Manager](../spec/04_system-components.md#42-session-manager): `caller_type` on the session row, copied to a child.
- [§4.3 Token Service](../spec/04_system-components.md#43-token-service): a user credential category with its table and key, the token trust record, and "Pods never hold downstream OAuth tokens" gains the stated exceptions, and the user link category with its table.
- [§4.9 Pre-Authorized Credential Flow](../spec/04_system-components.md#pre-authorized-credential-flow): the `type` field, the providers with no delivery refused, the admin write route, the impersonation refusal, and the different `DELETE` semantics on a type row.
- [§4.8 Gateway Policy Engine](../spec/04_system-components.md#48-gateway-policy-engine): the built-in scan interceptor.
- [§5.2 Pool Configuration and Execution Modes](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): the refused pods, and `retireOnRelease`.
- [§8.3 Delegation Policy and Lease](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease): `userCredentialBindings`.
- [§9.3 OAuth/OIDC Flow](../spec/09_mcp-integration.md#oauthoidc-flow): the browser binding on the callback. The host-overlap refusal of a connector belongs to the [VCS layer](user-git-identity-design.md).
- [§10.2 Authentication](../spec/10_gateway-internals.md#102-authentication): `manage_user_credentials`, `manage_user_links` and the link rows of the permission matrix, the session input check, and the provider identity claim in the broker contract and a customer source's claim mapping.
- §9.4: memory writes refuse an impersonation bearer.
- [§11.4 User Invalidation](../spec/11_policy-and-controls.md#114-user-invalidation): stopping a user's credentials at each level, and revoking the user's links at the provider.
- [§12.8 Compliance Interfaces](../spec/12_storage-architecture.md#128-compliance-interfaces) and [§12.9 Data Classification](../spec/12_storage-architecture.md#129-data-classification): the new columns, the exposure rows, `agentHeldCredential`, `TokenStore` deletion with provider revocation under the 30-minute limit, and `user_links` as T4.
- [§13.3 Credential Flow](../spec/13_security-model.md#133-credential-flow): "Pod never receives" gains the stated exception, and session input routes refuse an impersonation bearer on affected sessions.
- [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api): the credential and link routes, and the wire names §4.9 and the code disagree on. [§16.5](../spec/16_observability.md#165-alerting-rules-and-slos) and [§16.7](../spec/16_observability.md#167-section-25-audit-events): alerts and events.

## Defects found while verifying

These exist today and are recorded here because this document edits nothing else. [`user-git-identity-design.md`](user-git-identity-design.md) recorded them before revision 28. Each is filed in `BUILD-GAPS.md` under the identifier its entry names.

- **Account linking in §9.3 (F-9.3.18).** `handleAuthorizeConnector` returns an authorization URL and state to any authenticated caller, and `handleConnectorOAuthCallback` carries no bearer and no cookie. An attacker sends the URL to a victim, who approves, and the victim's token lands under the attacker's tenant, connector, and user. The specification's "bound to the initiating session ID and connector ID" also differs from the code, which binds tenant, user, environment, and session. Severity is High wherever a confidential connector is registered, and a public connector is exposed as well, because the gateway supplies the PKCE verifier itself. The fix is this document's browser binding.
- **Refresh absent (F-9.3.20).** `connectoroauth.RefreshAccessToken` has no caller, and `Invoker.bearerFor` returns an expired token, so a GitHub or GitLab connector stops working at its expiry.
- **User-scoped credentials decrypted in the gateway (F-4.3.1, its deferred cleanup).** `credentialpg.New(w.pgPool, w.kmsProvider)` runs in `cmd/lenny-gateway/credentialsurface.go`, against §4.3's reservation of KMS decrypt for the Token Service and §4.9's "Token Service stores it" **[source]**.
- **Inert registration (F-4.9.46).** `POST /v1/credentials` stores `provider: github`, `aws_bedrock`, or `vault_transit`, which can never be delivered.
- **Session input under impersonation (F-13.3.26).** `handleMessages`, `handleResume`, `handleMemoryWrite`, `lenny/send_message`, `lenny/memory_write`, and the `/v1/responses` and `/v1/chat/completions` translators admit an impersonation bearer, and §13.3 limits only mints. This matters beyond this design.
- **Connector client secret resolution (F-9.3.19).** A connector's `clientSecretRef` is `namespace/name`, and `connectorsecret.KubeResolver` resolves it with no tenant check **[source]**. Every `/v1/admin/connectors` route uses `requireAdmin`, which admits only `platform-admin` (`pkg/gateway/externalapi/admin/routes.go` and `tenants.go`), and the outbound connector design's tenant-admin routes are a different resource, so this is no tenant-to-tenant escalation in today's tree. **The defect is that the resolver cannot read there.** The chart gives the gateway's service account `create`, `update`, `delete`, and `deletecollection` on Secrets in the credentials namespace (`lenny-gateway-credential-writer`), and `get` on one named admin token Secret in its release namespace (`lenny-gateway-admin-token`). The resolver uses an uncached client, so under the chart's defaults a `Get` on §9.3's own example, `lenny-system/github-client-secret`, is forbidden, and a confidential connector's token exchange fails at the callback. This comes from reading the chart and has not run.

## Documentation

- An operator page on the grant, stating every row of "What `file` does not promise" in those words, for the platform admin deciding on it.
- An operator page on link providers, covering client registration for each kind, the callback URL, and the delivery ceiling.
- A tenant admin page on types, hosts, sources, bindings, request rules, and presets, stating that rules apply under `delivery: inject` alone, that no template means full reach, and that any change to a token trust record makes stored tokens unusable.
- A backend integration page: the write route, the role, expiry, rotation, and exposure records.
- A runtime author section on the file layout, and on what a refusal looks like.
- A preset reference generated from the data, and doc tests pinning error codes to their pages.
- An add-on author page on kind blocks, the extension points, and the test kind.

## Tests

Every test carries a `// spec:` annotation once the specification sections land.

### Tier 0: static

Preset and template data pass schema checks. A lint refuses a type row's secret, and any `user_links` access, outside the Token Service. A test fails when a route on any mounted mux, an MCP session tool, or a registered adapter prefix declares no impersonation status.

### Tier 1: unit

- Types: name rules, reserved names, an unknown source read as absent, exact canonical hosts, and a new `typeId` on recreate.
- The token trust record: each field changes the digest, including `attach`, a route under any `environmentPolicies` entry, a move between scopes, a reorder, and a resolved address, while a mirror route, an egress group, a shipped template's `permissions`, a re-resolution, and a rotated secret value do not. A revert restores use. An edited digest, a moved ciphertext, a swapped role, a moved environment, an encoding collision, a rewritten sealed field, a copied cache entry, and content not hashing to the sealed digest each fail.
- Registration: each permission, owner, type, size, encoding, and expiry path, and each refused subject, including a security principal, a service, and an erased subject.
- Rule evaluation: each rule kind and default, tenant additions over a preset, path patterns, and each preset property per flavour.
- Everything under "What every rule set forces", on the spike's attack rows and new rows: `%` escapes in each position, trailing slashes, format suffixes, `/graphq%6C`, `/graphql/x`, `/api/v5/...`, GraphQL literals carrying `\n/merge` in arguments at any depth, variable defaults, and directive arguments, string-encoded `variables`, REST twins under both root forms, routing override headers, and `/repos/acme/graphql-tools` passing. `/graphql.json` and an uploads path with a suffix matching no admit, `merge.foo`, `merge.json.foo`, a dotted deny pattern at each dot, a `POST` to `/repos/a/b/graphql`, any method on a suffixed listed path such as `/graphql.json`, with `PATCH` and `DELETE` on `/repos/acme/graphql`, a label named `graphql`, and `PUT .../contents/graphql` passing, a `query` with a `Mutation` fragment or a denied field, `/api/v3/repos/...` under a root flavour, and a wrong family's denied mutation names and REST writes refused. `gh secret set --env` and `gh variable set --env` pass, and `gh release upload` passes on GHES.
- A generic rule on a Jira stand-in: a multipart write and a write with a query string pass.
- Template rendering, with a URL off the recorded origins refused and a failure never falling back to the parent.
- Header stripping, with `Time-Zone` forwarded and `Cookie` removed.
- Allow-list mode: a read passing under `reads: allow` and refused under `reads: listed`, a GraphQL query passing under both, a write and a mutation refused with no allow rule and the reason `not_listed`, a multi-root mutation refused when one root is unlisted, a deny rule, body pattern, `denyBodyKeys` key, and preset each refusing an allowed request, an unlisted GraphQL path refused, an upload refused with no allow rule, `reads` refused with `default: allow`, and `requiredPresets` unmet by an allow list alone.
- Kind blocks: a block validated against its registered schema, a block keyed by another kind refused, an unregistered kind refused, and each block field changing the digest unless exempted.
- Canonical forwarding: the printed document re-parses to the same operation and fields, the variables re-encode to one value per key, and the corpus documents read the same under gqlparser and graphql-ruby after printing.
- Each bound, at its limit and one past it, including the 40,000-level document and a bare-LF head.
- A cache key always includes the row or `link_ref` and generation.

### Tier 2: component

The tests run pod model validators against envtest, `retireOnRelease` honoured after a pool edit in both directions on each release path (`podsession.Binder.Release`, `Server.ReleaseSuspendedPod`, `PodExecutor.Release`, and the scrub reporter's seams), the pod credential API refusing a release with no mark, and suspension when a pin or guardrail arrives.

### Tier 3: contract

The pod credential API with `handling`, the expected route and CA, resolved rules, and a `file` value, including an old `netagent` stand-in refusing an unknown handling. The Token Service link API. Each extension point against a test kind, including a point that errors.

### Tier 4: integration

- A backend write through to a file, with rotation, revocation, expiry, and deletion each reaching the pod.
- An impersonation bearer on every mounted route, on `lenny/send_message`, `lenny/memory_write`, `/v1/responses`, an `inReplyTo` reply, a message held for an unbound session, a child whose parent is affected, and `/v1/credentials` writes, including memory writes and deletes on an unaffected session.
- A service and an `agent` session under each value of the binding's `required` field, and a human with no token.
- Delegation with and without the lease naming the binding, including under `poolAttach`.
- An OAuth2 provider stand-in: link, refresh race, revocation, a hostile redirect, an oversized response, no refresh token, and no `expires_in`.
- Each `identityBinding` value, with a second account refused and a directory change moving a link to `user_act_required`.
- The link flow against a fake provider, with a mismatched browser login, a missing cookie, a replayed state, a second user on one account, and a refused `allowedAccounts`.
- Refresh: a race across replicas produces one provider call, a lost response moves to `user_act_required`, and a store failure after the response recovers.
- Provider revocation for each row of the lifecycle table, including a host unreachable during erasure, with the destroyed ciphertext and the receipt.
- A change to each trust record field, for both sources, including a route and a CA on an unchanged host: the token is unusable at the pod credential API and at each mint, and a stand-in on each endpoint receives no old token. A route edit after a comparison: the decision service refuses the asserted route. An App or project token mint on the same host succeeds with no assertion and no refusal. **The Initech topology, a git route alone to a registered private endpoint, and the connector topology, an egress route alone with the git route `direct`, each work** for an API request, a git request from a session that `allowRuntimeHeldCredentials` applies to, and a link provider call, each naming the list it matched. A host with both lists gives each class its own route, and a cached decision for one list does not answer the other. A route edit stops a pooled upstream connection. A provider in `deleting` drains, refuses a recreate, stops on a later route or CA, and ends each row at the bound with an audit event and a notification.
- The page before the redirect, and a sealed `clientSecret` unusable after a record change and restored by a revert.
- `tokenTrust.allowedRecords` refusing or suspending an unlisted record.
- Exposure rows across rotation, and a missed event recovered through the read route.
- Audit and `credential.mint` for each delivery, with no secret in any record.

### Tier 5: end to end on Kind

The tier covers each refused pod, a runtime reading a file across a swap, and `delivery: inject` with rules through a client binary.

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
- A leak scan with each encoded form, with the misses recorded.
- A hostile link provider host: redirects, oversized bodies, hostile logins, and an SSRF target at registration.
- Account-link attacks in both directions.

### Tier 10: conformance

The runtime battery gains reading a rotated file.

### Tier 11: documentation

The tier checks error codes, alert runbooks, and the preset reference.

## Validation owed

Measurements of this mechanism ran on a stub with stock git, and **the stub proves forwarding only**. The add-on designs hold the validation owed against their providers, including the matcher's behaviour at each host.

| Item | Question | Decides | State |
| :-- | :-- | :-- | :-- |
| V3 | Variable re-encoding on both flavours | Forwarding | Owed. Printing ran **[measured]**, and re-encoding has not. |
| S7 | Is `gqlparser` suitable? | Parser choice | **Done for the rule evaluator's parsing** **[captured]** at 2.5.33. A black-box test sending the corpus to a scratch repository on GitHub would name its parser, and the design no longer depends on the answer. |
| S9 | `netagent` memory at the rule bounds. | The concurrency cap and `GOMEMLIMIT` | **[measured]** to 41.7 MB `memory.peak`, under a 15,000 token limit. Check allocation at the final bounds is **[measured]**, and `memory.peak` under them is owed. |
| V8 | `netagent` memory at the final bounds | The concurrency cap | Partly **[measured]**: `memory.peak` reached 41.7 MB under a 15,000 token limit. Not measured under the final bounds. A 40,000 node body checks in 10.5 ms and 7.5 MB. |
| V9 | Leak scanning's cost at the listed phases' rates | Whether UC-6 is on by default anywhere | Owed |

## Residual risks

- **`delivery: file` is what it says.** Everything under "What `file` does not promise" stands after every mitigation. The opt-in exists because the alternative for those cases is no mechanism at all, as the owner stated.
- **`delivery: file` beside a runtime edit.** A tenant admin able to change a runtime or setup commands can read a token under `delivery: file` in a user's session.
- **Future grants.** A tenant admin who controls configuration can obtain a token a user grants after her edit, past the page, or a value an automated backend writes after it. Only `tokenTrust.allowedRecords` enforces against this, and it holds only when `security-admin` is a different person.
- **Names the customer operates.** Whoever controls the DNS, CA, or network of such a name can read tokens sent to it, and the record cannot see that.
- **A compromised replica.** It can forge session rows and bindings, so it can use any stored token at its record's hosts and read any value whose delivery ceiling is `file`, and it can plant a token in any tenant, or write a provider row with no guardrail check for a future grant. It can resurrect a row after `DELETE`, `revoke`, or erasure from a copied ciphertext, and rewrite `state` and exposure acknowledgements. It cannot redirect a stored token. A role for the Token Service's tables alone, on a second DSN, would narrow this, and the design does not need it for the destination rule.
- **Input before a tree is affected.** An impersonation bearer's input to a tree passes when no session in it is yet affected, and a later child that becomes affected, such as one declaring a repository under a VCS pool with `userAuthorization: required`, inherits what that input did.
- **`_method` in a multipart body** passes on a generic rule set.
- **A token with a delivery ceiling of `file`, and a setup command.** A tenant admin flipping a binding's `delivery` within a delivery ceiling of `file` can read a stored token with a setup command, which the settled position accepts.
- **A token that mints a credential.** Under `delivery: inject`, an agent can use an operation the token permits and the preset does not name to obtain a new credential, which the scrub and Lenny's revocation do not reach.
- **Full reach without a template.** With no narrowing template, a delivered token holds the user's whole reach on that provider.
- **`delivery: inject` with the request rules' `default: allow`.** A new operation with approval or merge power passes until the preset is updated. The window is the schedule, the review, and `cacheUntil`.
- **Compromised `netagent`.** It holds the token under either delivery and ignores rules. It holds no refresh token.
- **A token Lenny cannot attribute.** A `registered` token proves nothing about whose it is. A backend or tenant admin holding the permission can place a token under a user's name.
- **Hosts can lie.** A `registered` type's `hosts`, and a `kind: oauth2` provider's `apiHosts`, are statements Lenny cannot check. A type declaring an innocent host and holding a GitHub token passes an add-on's host policy, pin, preset, and approval checks ([SP11](security-oversight-design.md#security-properties) in [`security-oversight-design.md`](security-oversight-design.md)) on the label. `lockEgressToHosts` and `credentialPinned` on the host the token works on are the enforcing controls, and the second waits on the egress design's exfiltration controls ([EG-12](egress-and-git-design.md#proposal-split)).
- **A forwarded header** can carry meaning at a host this design knows nothing about.
- **Leak scanning** lets a compromised replica obtain the value of any `leakScan` session across tenants, and it misses encoded and split values, direct-mode LLM traffic, opaque tunnels, and checkpoints.
- **Key separation.** Under `kms.provider: local`, one master key is mounted in the gateway and the Token Service, so a compromised replica can decrypt type rows there.
- **A `registered` token removed from Lenny** stays valid at the provider until the tenant acts on its exposure rows. A record change leaves old OAuth2 tokens valid at the provider until they expire, because sending them anywhere is what the rule forbids.
- **Allow-list mode and client upgrades.** Under `default: deny`, a client release that uses an operation the tenant has not listed fails until the tenant lists it, and under `reads: allow` an allow list does not limit what the token reads.
- **Presets are a deny list.** They fail open on what they do not name, and this was the owner's accepted cost for new operations. A REST twin missed today is a defect in the data.
- **A cleared preset.** A tenant admin can clear a preset, audited and in posture, unless a `security-admin` requires it.
- **Parsers.** The rule under "Canonical forwarding" closes differences in how the client's bytes are read. It rests on gqlparser's printer emitting text the host reads as printed, which the corpus shows under graphql-ruby and graphql-js **[measured]** and nothing shows on a live host.
- **A link provider with `identityBinding: unverified`.** UCP-11 does not hold for its links, and a user can act under another account of their own.
- **A lost refresh response** forces the user to relink.

## Decisions

| # | Decision | Recommendation | Cost of the recommendation | Alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Replace the allowlist with generic bindings and data rules | **Settled by the owner (D1).** Generic binding, optional rules as data, and the GitHub deny preset on by default. | A new mutation with merge or approval power passes until the preset is updated, and input-key and target checks are gone. | The allowlist, which failed both of the owner's conditions. |
| 2 | First-version sources | **Settled by the owner (D2).** `registered` and `oauth2Link`, with `endpoint` reserved. | A tenant wanting Lenny to fetch from its own endpoint at session time writes rows from its backend instead. | The endpoint now, which adds a tenant-reachable outbound call per session, and an SSRF surface. |
| 3 | Known-provider restriction | Extend it with a `type` field, and refuse the providers with no delivery | Two fields in place of one, and a change for anyone registering those providers today, of which there are none in the wild. | Replace it with free strings, which sends typos and new types through the LLM router. |
| 4 | What a tenant admin can obtain | **Settled by the owner.** Stored tokens: she cannot read one, under either source, through any change to the token trust record. Future grants: she can trick a user past the page into granting a new token to a host she runs, or re-point an automated backend, and no per-field rule closes that, because she configures the tenant. Planting stays accepted: she can write a token under a user's name. A tenant needing separation sets `tokenTrust.allowedRecords` under [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture)'s delay. | The page and the sealed secret reduce the future-grant route and leave it open, and the page gives little signal on a route or CA change or under a new provider name. A name the customer operates sits outside the record. Every token breaks on any record change. The guardrail needs a separate `security-admin`, and every CA rotation needs an approval. | Hold all link provider and type writes to `security-admin` or the platform, which takes configuration away from the tenant admin. Or a permission outside the `tenant-admin` ceiling for writing tokens, which §10.2's custom-role ceiling does not admit today, and which addresses planting alone. |
| 5 | Row semantics on `DELETE` | Stops use on a type row, differing from the LLM path | Two meanings for one verb, by row kind. | One meaning: leave running sessions alone, which is unsafe for a token being removed. |
| 6 | The gate | `userCredentialDelivery` as its own class | Another grant to manage. | Reuse `agentReadableDelivery`, which conflates a tenant's token with a person's. |
| 7 | `delivery: file` near a VCS host | Moved to [`user-git-identity-design.md` decision 49](user-git-identity-design.md#decisions) in revision 19. | | |
| 8 | Runtime-held credentials on the git path | Moved to [`user-git-identity-design.md` decision 50](user-git-identity-design.md#decisions) in revision 19. | | |
| 9 | Smuggled tokens beside `userAuthorization: required` | Moved in revision 19. [`user-git-identity-design.md` decision 40](user-git-identity-design.md#decisions) holds it. | | |
| 10 | Headers | Strip credential headers, refuse named routing overrides, and forward the rest | A header carrying meaning at the host passes, which is a residual. | The allowlist, which refused all of `gh` on any image with `/etc/localtime` **[measured]**. |
| 11 | Preset clearing | Only `preset: none` clears it, by `tenant-admin`, audited and in posture, and `security-admin` can require it with a minimum version | A tenant admin can remove the protection, and a customer without a security principal has nobody above that admin. | Platform-only clearing, which does not match the owner's "a tenant can change it". |
| 12 | Preset distribution | Resolved rules on the pod credential API, so an update needs no image | Rules travel on each response on the pod credential API, and a stale chart serves a stale preset. | In the `netagent` image, which ties a preset fix to a pod roll. |
| 13 | Delegation | Only when the lease names the binding, including under `poolAttach` | A runtime that delegates must say so, and an unaware runtime's child has no token. | Inherit, as other bindings do, which hands a person's token to every descendant, including one the parent knows nothing about. |
| 14 | Sessions with no user | `caller_type` on the session row, and the binding's own `required` field | A new field, and a tenant must decide per binding. A service session cannot use a user token. | A service token per type, which is a tenant credential and already exists as a stored binding. |
| 15 | Leak scanning | Optional, per binding, with the value released to a session's coordinating replica | A compromised replica can obtain any `leakScan` session's value across tenants, because a replica can acquire any lease. It costs a scan per message, and it misses encodings. | None, which leaves detection to forensics after the fact. Or signed coordinator claims, which is a change to §10.1 for one option. |
| 16 | `lockEgressToHosts` | Optional, per binding, persisting to descendants, refused on pools with `egressProfile: internet`, on pods with `maxConcurrentSessions > 1` or in service mode, and at delegation to one | A session under it reaches almost nothing else, including package registries, and its children inherit that. It covers egress alone. | None, which leaves `hosts` advisory in every case. |
| 17 | Client secret | A write-only value, stored under a tenant-scoped key, re-checked at read, and sealed with the record digest | The admin API takes a secret on a write, with §4.9's logging exclusions, and any record change needs the secret again unless reverted. | A reference with a platform-only registration, as pools have, which needs an operator ticket per provider. |
| 18 | Spelling | Adopt the spike's strict matcher: refuse `%` outside a declared segment on every method, drop trailing slashes and empty segments, compare the final segment with and without anything after its first `.`, forward the canonical path, refuse the unmatched `/api/*` and `/graphql*` families under a preset, refuse a `POST` with a final `graphql` segment, and refuse a suffixed listed GraphQL path on any method | A tool sending an unusual but valid spelling breaks. In the runs, no `gh` command did **[measured]**. | Decode and broaden the match, which missed `.json` suffixes and `/graphql/foo` **[measured]** and depends on how each host decodes. |
| 19 | Request forms | Preset properties | A tenant's generic rule has no query or body form limit unless it writes one. | Forced on any rule, which refuses multipart Jira writes. |
| 20 | Pod eligibility after an edit | `retireOnRelease` on the claim at bind, the pod credential API refusing a release without it, and a pod with a prior session refused | A pod carrying this binding always retires, and a binding that a mid-session edit creates delivers nothing until a new session. | Stamp all pool policy on the pod, which is a larger change for one field. |
| 21 | Exposure | Durable rows for both sources and a read route, with the event as a notification | A store, a route, and an alert for tenants who may ignore them. | The event alone, which §25.5 says is best effort. |
| 22 | Delivery ceiling | On the type or provider, in the token trust record, and shown on the pages | A change of purpose interrupts every user. | No ceiling, which turns a link made for a check, or a value stored under a delivery ceiling of `inject`, into a file when an admin edits a binding. |
| 23 | One record for trust | A token trust record with a digest sealed on each stored token and cached value, compared at the pod credential API and at each provider call, with each call using the compared content and asserting its route, and one `user_act_required` state, replacing `relink_required`, `reregister_required`, origin generations, and per-field triggers | Any difference breaks every token under the record, including a narrowing, a CA rotation, a route move, a reorder, or one environment's scoped route. | Direction per field, which is more rules, and three review rounds each found a field the rules missed. |
| 24 | Preset body | Moved to [`github-credential-addon-design.md` decision 8](github-credential-addon-design.md#decisions) in revision 19. | | |
| 25 | Where `registered` rows live | A Token Service table under a key only it can decrypt, as §4.3 reserves, with row identity and digest sealed as additional data | A second store beside `credentials`, and a write passed through to the Token Service. The key separation holds on a cloud KMS alone. It shares a Postgres role with the gateway, so a replica can still forge a session or plant a token. | The `credentials` table, which every gateway replica can decrypt. Or a grant-restricted role on a second DSN as well, which narrows forgery and is a deployment change. |
| 26 | API flavour | Moved to [`user-git-identity-design.md` decision 51](user-git-identity-design.md#decisions) in revision 19. | | |
| 27 | Add-on model | A link provider's `kind` selects a compiled-in add-on, its block keyed by the kind's name holds the kind's fields under a schema the add-on registers, and named extension points supply provider behaviour. The whole block is in the token trust record unless the add-on exempts a field. | Each add-on is a code change and a release, and a tenant cannot add a kind. A new field in a block breaks stored tokens on the next write that sets it. | A free-form `extension` object, which no write path can validate and whose trust coverage is unclear. Or provider fields at the top level, which ties the generic object to its providers. |
| 28 | Where the link flow lives | In this design, as UC-2, for every kind, with the VCS layer and the add-ons supplying kinds | The VCS layer no longer reads as complete on its own, and readers follow references for linking. | In [`user-git-identity-design.md`](user-git-identity-design.md) [UG-1](user-git-identity-design.md#ug-1-vcs-link-kinds), as before revision 19, which made the generic source depend on a GitHub-oriented design. |
| 29 | §9.3 reuse (formerly [`user-git-identity-design.md` decision 2](user-git-identity-design.md#decisions)) | **Settled**, in the owner's word "ok". Reuse `pkg/connectoroauth`, with a new record in the Token Service | A second table and flow beside §9.3, and new routes. | Rows in `connector_credentials`, which brings the MCP requirement, environment key, gateway decryption, and the callback defect. |
| 30 | Link key and App (formerly [`user-git-identity-design.md` decision 4](user-git-identity-design.md#decisions)) | **Settled**, in the owner's word "yes", after leaning towards adding the App to the key. A link is keyed on tenant, user, and link provider name, and a tenant can have several link providers on one host. A VCS pool with `userAuthorization: required` names its check's link provider in `userAuthorization.linkProvider`, an `oauth2Link` type names its own, and a provider account belongs to one Lenny user across a host's providers. | A person links once per App, when an App first needs it, and pools and types name their provider. | One VCS link provider per host, which makes a tenant that wants both the check and authoring hold write power in every stored token. |
| 31 | Account sharing (formerly [`user-git-identity-design.md` decision 5](user-git-identity-design.md#decisions)) | **Settled**, in the owner's word "ok". Refuse two Lenny users on one provider account within a tenant | A team with a shared account cannot link it, and a first linker holds it until removed. | Allow it, which makes attribution false and gives a route around two-person review. |
| 32 | Refresh (formerly [`user-git-identity-design.md` decision 6](user-git-identity-design.md#decisions)) | **Settled**, in the owner's word "ok". One refresh at a time per link through a row lease, parent in the Token Service, on demand | A lost response forces a relink, and a waiting replica can answer `unavailable`. | A long transaction with a lock, which holds a pooled connection across a provider call. Neither provider offers an idempotent refresh to avoid this. |
| 33 | Impersonation (formerly [`user-git-identity-design.md` decision 19](user-git-identity-design.md#decisions)) | Refuse session input on affected sessions at the session input layer, and memory writes on every session | An operator cannot reproduce a support case by messaging the user's session or writing her memory, and must start one under its own identity. | Taint the session and stop its user tokens for the rest of its life, which leaves the session running and adds state. |
| 34 | Disables (formerly [`user-git-identity-design.md` decision 21](user-git-identity-design.md#decisions)) | Stop use and revoke at the provider at every level | A mistaken `soft_disable` forces relinking after reinstate. | Revoke only at `full_revoke`, which leaves a live grant for a disabled person. |
| 35 | Parser differences (formerly [`user-git-identity-design.md` decision 29](user-git-identity-design.md#decisions)) | Forward a document and body `netagent` generated from the checked structure, with the lexical rule beside it, for each inspected request | A printing step, a second parse, and a body that differs in bytes from what the client sent, in 6 of 159 corpus documents **[measured]**. A printer defect becomes a refusal or a wrong request, which the re-parse narrows. | Forward the client's bytes after checking, which is the design that the bare CR bypassed **[captured]**. Or identify the host's parser, which cannot be done from outside and can change. |
| 36 | Bounds (formerly [`user-git-identity-design.md` decision 30](user-git-identity-design.md#decisions)) | 512 KiB body, 64 KiB document, 4,096 tokens, depth 32, 10 s head, and 4 concurrent checks per pod | A `gh api graphql` query past these fails with a named bound. Concurrent large writes wait or get 429. | The prototype's 256 KiB body, which refuses a legal PR body. Or no token limit, which measured 74.9 MiB against a 64 Mi example request **[captured]**. |
| 37 | Query root fields (formerly [`user-git-identity-design.md` decision 32](user-git-identity-design.md#decisions)) | Admit any query, within the bounds | A query can read whatever the child reads, under any root field. | A root list, as the prototype had, which breaks `gh api graphql` on roots such as `rateLimit` and adds no write protection. |
| 38 | Identity binding (formerly [`user-git-identity-design.md` decision 37](user-git-identity-design.md#decisions)) | A required `identityBinding` on each link provider, with `directory` or `claim` recommended and `unverified` reported in posture | A tenant runs a mapping or an issuer claim, and a user cannot link until it exists. | No binding, which lets a user author under a second account and approve as themselves. A fixed email match, which needs an authoritative IdP email everywhere. |
| 39 | Shared App across tenants (formerly [`user-git-identity-design.md` decision 45](user-git-identity-design.md#decisions)) | Removed. A link provider's client belongs to one tenant. | An operator cannot offer one App to many tenants, and each tenant registers its own. | A platform-only write with fixed endpoints, which is a second write path and a record no tenant admin can touch. |
| 40 | Reads in allow-list mode | `reads: allow` by default under `default: deny`: `GET`, `HEAD`, `OPTIONS`, and GraphQL queries pass unless denied, and `reads: listed` makes a REST read need an allow rule | An allow list limits what an agent can do and leaves what the token reads to the token's scope. A host that changes state on a read is unprotected unless the tenant sets `reads: listed`. | Every read needs an allow rule, which refuses a client's discovery and status calls on every host until the tenant lists them, and most tenants that want an allow list mean one over actions. |

## Sources

Vendor pages are listed in the add-on designs that rely on them, and the capture and history data are recorded in [`github-credential-addon-design.md`](github-credential-addon-design.md). Coder's external authentication page, which UC-2's user experience follows, is listed in [`user-git-identity-design.md`](user-git-identity-design.md).

The evidence for this document's **[measured]** rows is under `evidence/user-git-identity-design/`, in `spike-gh/RESULTS.md` and `spike-gh/raw/`, and that folder's `README.md` lists what was left out. Citations in this document resolve under `evidence/user-git-identity-design/spike-gh/`: `raw/gitopt/...` as written, `t4/table.txt` at `raw/gitopt/t4/table.txt`, `deny-ghcom.txt` at `raw/gitopt/attack/deny-ghcom.txt`, and `source-headers.txt` at `raw/gitopt/review/source-headers.txt`.

## Decision history

This section records how the owner reached the generic mechanism. The decisions it led to are decisions 1 and 2 under "Decisions".

| Step | What happened | Tag |
| :-- | :-- | :-- |
| 1 | `user-git-identity-design.md` revision 2 specified an allowlist filter, `vcs-authoring`, with input-key checks, in `netagent`. | **[design]** |
| 2 | On any image with `/etc/localtime`, `gh` sends `Time-Zone`, and the forwarded-header allowlist refused every GraphQL request with 403. | **[measured]** |
| 3 | Of 68 `gh` releases since 2024-10-01, 4 changed the mutation set. 16 of the 66 mutations in `gh` 2.103.3 were on the table and 50 were off. These are names read from source rather than run, and they are a lower bound on drift. | **[source]** |
| 4 | The owner measured the filter against both conditions and chose one generic binding with optional rules held as data, with the GitHub deny preset on by default. | Owner decision D1 |
| 5 | The owner accepted these losses: a new GitHub mutation that can approve or merge passes until the preset is updated, and the input-key and target checks go away. | Owner decision D1 |
| 6 | First-version sources are `registered` and `oauth2Link`. A tenant-run endpoint comes later. | Owner decision D2 |

## Review history

This section records the reviews of earlier revisions and what each revision changed. Each finding was resolved in the revision after the one its subsection names, and the current text above supersedes any statement here that differs from it.

### Revision notes

These notes were carried in the status line of earlier revisions.

- Revisions 9, 10, 12, and 13 changed only the pointer to the companion designs' revisions. Revision 11 also removed the delegation bound, which `tenant-scoping-identity-design.md` decision 14 retired, and revision 14 also named `egressLockHosts`.
- Revision 15, dated 2026-11-03, followed the rounds of independent review recorded below, and the owner settled decisions 4 and 24 and the git path, in the words "go with T and your recommendations". The last round, recorded under "Review of revision 7", was a stop check that found no Major or higher issue. That check traced the last round's changes and was not an end-to-end read.
- Revision 16, dated 2026-11-05, allowed several link providers on one host, under `user-git-identity-design.md` decision 4.
- Revision 17, dated 2026-11-06, recorded the live GitHub spikes of `user-git-identity-design.md` under V6 and V7, and that design's per-session children under its decision 7.
- Revision 18 added "High-level design", named the object and the field behind each configuration value the text cites, and moved the decision history and the review records to the end of the document. It changed no rule.
- Revision 19 made this design stand alone as the generic mechanism. It took the link flow, the stored links, identity binding, refresh, revocation at the provider, canonical forwarding, the bounds evidence, and the defects found while verifying from `user-git-identity-design.md`, with decisions 29 to 39. It added UC-7, the add-on model, with decisions 27 and 28, and the `oauth2` kind block, and UCP-11. It moved the VCS pool interactions, the API flavour, and decisions 7, 8, and 26 to the VCS layer, and the preset bodies and decision 24 to the add-on designs. One rule is new: a link whose identity binding no sweep covers re-evaluates at its next release.
- Revision 20 added "Allow-list mode" under UC-4, the `reads` field, the `not_listed` refusal reason, and decision 40, and pointed "Bounds and cost" at this document.
- Revision 21 added a plain-language summary to "High-level design", linked every citation of another design to the file and the heading it names, added "Labels from other designs", and replaced the egress design's interface labels with the names "pod credential API" and "egress decision API". It changed no rule.

### Review of revision 1

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

### Review of revision 2

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
| N13, pin reach | Holds. | Unfiltered by environment, and `internet` pools are stated. |
| N15, buffering | Holds. | `denyBodyKeys` is inspected, and a streamed 401 is stated. |
| N16, route | Holds. | Every link provider endpoint goes through `lenny-egress`. |
| N18 to N20, N22, N30, N35, N36, N41 | Hold. | Corrected. N35's `reregister_required` no longer exists. N41's branch segment is declared, and `glab`'s use of it is owed. |
| Minors of the security review | Hold. | The pin exception follows a release, `self` needs `human`, exposure covers both sources, and `registeredBy` is no longer offered as a defence against Carol. |

None was rejected. H1's alternative, refusing routes and CAs on those hosts, was not needed. The per-field rules of revision 2 are removed.

**Spike results folded in.** V1 and V2 close on the stub, as "Validation owed" lists. The strict matcher replaces revision 2's path text where they differ, in dropping doubled slashes rather than refusing them and in stripping suffixes for matching. "Git and hosts" holds the measured columns. Not run: any GitHub or GitLab host, permission check, or quick action; `gh` on amd64, macOS, or Windows, the .NET SDK image, and drift on the Debian client; a `glab` fork flow, read after push, and git against `ghe.com` beyond the stub; a T and P hybrid, and a GitLab route inventory.

### Review of revision 3

Round 3 of the reviews read revision 3, and each finding was verified against the text and the tree. "R3" is the security review, and "M" and "m" the sync review.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| R3-1, a replica repoints a stored token | Holds. `token-service-deployment.yaml` shares the DSN, and `Cipher.Seal` binds the alias alone. | Tenant, user, identity, and digest sealed as AEAD additional data, including the client secret's. The threat row now states what a replica can forge. No second DSN, because the sealed form holds for the destination rule. |
| R3-2, the route is chosen after the comparison | Holds. | Each call uses the compared content and asserts the route, and the decision service refuses a disagreement. `netagent`'s comparison of two configuration copies is removed. |
| R3-3 and H1, routes per environment | Holds. | The route value is the set across tenant routes and every `environmentPolicies` entry. **Verified against the terminating path:** the mirror route takes `netagent`'s service account token and no user token, so it does not count, and a held credential is dropped there. An egress group tunnels with no decryption, so it does not count, and its upstream does. A git route to a git endpoint does count. |
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

### Review of revision 4

Round 4 of the reviews and the spike follow-up read revision 4, and each finding was verified against the text, the tree, and the raw output. "R4" is the security review, and "M" and "m" the sync review.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| R4-1, cached tokens | Holds. `pkg/tokenservice/cache` binds the alias alone. | Each cached value and verdict is sealed with the full key and the digest. |
| R4-2, role | Holds. | Role and environment join the additional data, under an injective encoding, with the fields written with the value. |
| R4-3 and M1, route value | Holds. | An ordered list per scope, with pattern, target, and the platform default. The asserted route is its first match, as a digest of target fields. |
| R4-4 and m15, level 2 | Holds. | A contract row in egress with a conformance test, and an unasserted Token Service CONNECT is refused. |
| R4-5, `flavour` key | Holds. | Dropped. `/api/*` has no prefix exception under a root prefix. |
| R4-6, deleted provider | Holds. | A `deleting` state replaces the snapshot, with a stated bound. |
| M4, flag owner | Holds. | UC-5 owns it in all three documents, with a `handling` value and a mint refusal. |
| m9, m13, m14, m16, m17, m19, m22, m23, m24 | Hold. | Corrected. m22: the push on an uncovered host is inferred from P, and the token reaching is measured. m23: `t4/table.txt` shows L's tunnel. m24: `gh issue develop` is in the cost. |
| Security Minors | Hold. | The client secret clearing rule is removed, because the seal makes it redundant. Mutable fields, resurrection, and the replica's future-grant reach are stated. The comparison list, assertion details, `file` wording, runtime row, suffix text, suspended pools, environment-only routes, and digest format are corrected. |

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

### Review of revision 5

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

### Review of revision 6

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

### Review of revision 7

A stop check found no Major or higher issue and 13 Minors, all of which held. That check traced the last round's changes and was not an end-to-end read. The two tag corrections are the pool key and closure, now **[design]**, where the run shows the window and depends on a per-client-CONNECT decision that did not run, and `netagent` asking I8 before a 401, also **[design]**, since the run made 0 Token Service requests. The others write out the list order, name `default`, omit `direct` and mirror routes from the decision's git list, drop `POST` from the family sentence, extend the suffixed rule to any method, state a GitLab false refusal, record qualifying hosts on the claim, and name the error code.
