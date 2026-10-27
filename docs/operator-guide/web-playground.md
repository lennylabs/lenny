---
layout: default
title: "Web Playground"
parent: "Operator Guide"
nav_order: 16
---

# Web Playground

The gateway serves a browser playground at `/playground`. The playground creates and drives sessions against any registered runtime through the same public REST and MCP surface that every other client uses, so it doubles as a reference for the session API. It is disabled by default and is enabled per deployment through chart values.

The playground is described in [§27](../../spec/27_web-playground.md) of the spec.

## Enabling the playground

The playground is gated by the `playground` block in the chart values. A stock install renders no `/playground` routes; the gateway returns 404 for `/playground/*` until `playground.enabled` is set.

```yaml
playground:
  enabled: true
  authMode: oidc
  devTenantId: default
  allowedRuntimes: "*"
  maxSessionMinutes: 30
  maxIdleTimeSeconds: 300
  oidcSessionTtlSeconds: 3600
  gatewayHost: ""
auth:
  capabilityTokenTtlSeconds: 900
```

The gateway reads each value as a `--playground-*` flag. `allowedRuntimes` is a comma-separated glob list that filters the runtime picker; `"*"` offers every runtime visible to the caller. `gatewayHost` is the public host the playground UI connects to over the MCP WebSocket, and the gateway interpolates it into the `connect-src` Content-Security-Policy directive; leave it empty to omit the host.

`auth.capabilityTokenTtlSeconds` sets the lifetime of the bearer tokens that `POST /v1/playground/token` mints, bounded to 120 through 3600 seconds. The value lives outside the `playground` block because it is also the default lifetime of the tokens `POST /v1/oauth/token` issues without a requested `expires_in`. A bearer's expiry is fixed at mint and never extended, and the playground obtains a new bearer before the old one expires.

## Authentication modes

`playground.authMode` selects how the playground admits a browser and mints the session-capability token that `POST /v1/playground/token` returns.

- `oidc` exchanges a browser OIDC cookie for a session-capability bearer token. The gateway runs the `/playground/auth/login`, `/playground/auth/callback`, and `/playground/auth/logout` gatekeeper endpoints, and holds the cookie-to-bearer mapping in a Redis-backed session record. This is the mode for a shared or internet-reachable deployment. The gateway signs the browser in at the identity provider that `auth.oidc.issuerUrl` names, as the `auth.oidc.clientId` client, and reads the provider's authorization and token endpoints from the issuer's discovery document. Register `https://<gateway host>/playground/auth/callback` as a redirect URI of that client, and allow it as a public client with PKCE, because the gateway sends no client secret. The tenant comes from the `auth.tenantIdClaim` claim of the returned ID token. Until both `auth.oidc` values are set, or while the discovery document is unreachable, the login endpoint answers `503 LENNY_PLAYGROUND_OIDC_UNAVAILABLE`.
- `apiKey` admits a caller that presents an `Authorization: Bearer` header. The mode accepts any caller-supplied bearer, so it is appropriate only behind an existing trusted gateway or inside a private network.
- `dev` mints a token with no admission material and binds it to the `playground.devTenantId` tenant. Run `dev` mode only on a trusted local install such as `lenny up`.

Every minted token carries the `origin: playground` claim. The gateway uses that claim, rather than the auth mode, to apply the session caps below.

## Session limits

The playground binds two limits on every session it starts, identified by the `origin: playground` claim.

- The duration cap is `min(sandboxTemplate.maxSessionMinutes, playground.maxSessionMinutes)`. The cap counts active time, as `maxSessionAgeSeconds` does: it pauses while the session is `suspended`, with or without its pod, and resumes when the session returns to `running`.
- The idle timeout is `min(maxClientIdleSeconds, playground.maxIdleTimeSeconds)`, where `maxClientIdleSeconds` is the pool's effective `sessionPolicy.maxClientIdleSeconds`. The playground value is a hard override that only tightens a looser platform limit. When the timeout elapses, the gateway idle-suspends the session and releases its pod, or expires a session that cannot be idle-suspended (a `type: mcp` runtime or an `executionMode: service` pool). A session whose runtime adapter observed activity within the window, whose suspension attempt the adapter refuses or does not answer, or whose attempt fails because the workspace checkpoint fails or the session changed before the suspend write, stays `running`.

A browser that navigates away sends a best-effort `session.cancel`; a dropped connection that cannot send it falls back to the idle timeout, which fires within the 5-minute playground default rather than after the platform `maxClientIdleSeconds` default of 15 minutes.

## Security posture

The gateway applies a Content-Security-Policy to every `/playground/*` response that restricts script and connection sources to the gateway origin, sets `frame-ancestors 'none'`, and adds `X-Content-Type-Options: nosniff` and `Referrer-Policy: same-origin`. Static assets carry a long immutable cache lifetime, and `index.html` carries `no-store` so a new release propagates immediately.

Logout, user invalidation, idle timeout, and admin revocation all drive the same revocation path: the gateway deletes the Redis session record, writes a per-bearer deny-list entry, and publishes the revocation to peer replicas. A logout endpoint returns success only after the revocation writes commit.

Revocation covers the playground bearer itself and leaves alone any token already exchanged from it. A caller that holds a playground bearer and also holds another Lenny token in the same tenant can present the bearer as the `subject_token` of `POST /v1/oauth/token`. The Token Service refuses a playground bearer only as the caller token, so the exchange succeeds, before or after logout, and yields a child token that stays valid until the bearer's original expiry. Logout does not revoke that child token. The `auth.capabilityTokenTtlSeconds` lifetime bounds this exposure.

Enable `apiKey` mode only when an upstream component authenticates the caller, because the mode trusts the supplied bearer. Restrict `dev` mode to local installs, because it mints tokens without admission material.
