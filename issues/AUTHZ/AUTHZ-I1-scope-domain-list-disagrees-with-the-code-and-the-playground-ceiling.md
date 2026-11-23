---
id: AUTHZ-I1
status: open
kind: spec-defect
severity: low
requirements: []
---

# The scope-domain lists disagree with each other, with the playground ceiling, and with the code

## Specification

§25.1 (Design Philosophy and Agent Model) **Scope value syntax.**: `<domain>` is the tool family, from a closed list (`pool`, `health`, `diagnostics`, `recommendations`, `runbooks`, `events`, `audit`, `drift`, `backup`, `restore`, `upgrade`, `locks`, `escalation`, `logs`, `me`, `operations`, `tenant`, `credential_pool`, `credential`, `runtime`, `quota`, `config`, `circuit_breaker`), and the paragraph says it mirrors the §15.1 scope taxonomy. §15.1 (REST API) **Scope taxonomy.** lists those domains and further ones, such as `sessions`, `impersonation`, `issued_token`, and `rbac`, and says it is the source of truth. §10.2 (Authentication) "Playground mint invariants" invariant 2 and §25.1 pin `playground_allowed_scope` to `{tools:sessions:*, tools:me:read, tools:runtimes:read, tools:pools:read, tools:operations:read, tools:events:read}`.

## Evidence

- The §25.1 list, which says it mirrors §15.1, leaves out the domains §15.1 lists after `circuit_breaker`, from `artifact_replication` to `user`.
- The playground ceiling names the domains `runtimes` and `pools`, which neither list contains, and `sessions`, which only the §15.1 list contains.
- `pkg/common/scopes/scopes.go`, `Domains`: the map holds every §15.1 domain and also `runtimes` and `pools`. Its comment says those two sit outside the §15.1 taxonomy so the playground ceiling parses.
- `pkg/tokenexchange/tokenexchange.go`, `expandAllTools`: the strictly-narrower comparison of §13.3 counts `tools:*` as the wildcard of every domain in `scopes.Domains`, so the set of domains decides which requests are strictly narrower than an unscoped subject.
- Gap G6 of change 0165 (a formal model of the token rules), on 2026-11-20.

## Gap

Three statements of the closed domain set disagree: §25.1 names fewer domains than §15.1, §15.1 names fewer than the code accepts, and the playground ceiling uses two domains no list names. A scope value whose domain the specification does not list parses as canonical in the code.

## Suggested resolution

Keep one list authoritative, the §15.1 taxonomy as both sections say, have §25.1 link to it rather than restate it, add `runtimes` and `pools` to it or change the playground ceiling to listed domains, and check `scopes.Domains` against the list. Decided by the owner (change 0167 decision 11 files it without a fix).
