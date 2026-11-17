---
id: LOCALDEV-I1
status: open
kind: question
severity: medium
requirements: []
---

# No way to obtain an end user's token in a development install

## Specification

§17.4 (Local Development Mode (`lenny-dev`)) states that "The CLI mints the dev bearer from a persisted dev key and the in-cluster gateway trusts it through its dev-mode bearer-trust hook", and that `lenny token print` "emits a short-lived bearer for the embedded built-in user". The section names no way to obtain a bearer for another user, such as an end user that a tenant admin created with a non-admin role.

## Evidence

- Change 0157 decision 11: the end-user pilot journey (`tests/journeys/end-user-first-session.yaml` on the 0157.S1 branch) authenticates with the bootstrap `lenny-admin` token, whose roles are platform-admin, because the Kind development install offers no way to issue a token for a named user.
- `lenny-ctl` has no command to create a user or issue a user's token (0157.S1 build report, 2026-11-17).

## Gap

A development install cannot exercise an end user's permissions: every local session, test, or journey that needs a non-admin caller either runs as platform-admin or needs an external OIDC provider. The persona journeys therefore test the end-user documentation path without the end user's role.

## Suggested resolution

Decide whether the development install mints a dev bearer for a named user and role (for example `lenny token print --user <id> --tenant <id> --role <role>`, signed by the same dev key and accepted only under the dev-mode bearer-trust hook), and state it in the local-development page when that page lands. The end-user journey then switches to that token.
