---
layout: default
title: "OAuth Token Exchange"
parent: Tutorials
nav_order: 15
description: Use POST /v1/oauth/token to swap identity-provider tokens for Lenny access tokens, refresh access tokens, and rotate a caller's own token.
---

# OAuth Token Exchange

**Persona:** Client Developer | **Difficulty:** Intermediate

{: .highlight }
> **Status: planned.** The token-exchange walkthrough is scheduled for the initial tutorial set. The endpoint is canonical in the spec section below; until the walkthrough lands, consult the spec directly.

`POST /v1/oauth/token` implements the standard OAuth 2.0 token-exchange flow ([RFC 8693](https://datatracker.ietf.org/doc/html/rfc8693)) and refresh flow ([RFC 6749](https://datatracker.ietf.org/doc/html/rfc6749#section-6)). Use it in three situations:

1. **Swap an IdP token for a Lenny access token.** Your application already has a Google/Okta/Azure AD access token; exchange it for a Lenny token scoped to your tenant.
2. **Refresh an access token that is about to expire.** Use the refresh token issued alongside the access token.
3. **Rotate a caller's own token without restarting the gateway.** Call the endpoint directly with the current token as the `subject_token`. This covers self and service-principal rotation. Rotating the bootstrap admin credential uses a dedicated route instead: `lenny-ctl admin users rotate-token --user lenny-admin` calls `POST /v1/admin/users/{user}/rotate-token`, and the gateway mints the new admin token in process and patches the `lenny-admin-token` Secret (see [Installation](../operator-guide/installation.md)).

## What this walkthrough will cover

1. Request and response shapes for each of the three grant types.
2. Scope scoping (`audience`, `scope`, `requested_token_type` parameters).
3. Error codes specific to token exchange (`invalid_grant`, `unauthorized_client`, `invalid_target`).
4. How to handle refresh-token rotation with `refresh_token_rotation: true`.
5. End-to-end example: OIDC sign-in in a web app → exchange → calls to `/v1/sessions`.

## Canonical reference

- Spec §15 — external API surface, `/v1/oauth/token` endpoint definition
- Spec §13 — security model, token lifetime and rotation policy

## Related docs

- [API Reference — Authentication](../api/index.html#authentication)
- [Your First Session](first-session)
