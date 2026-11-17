---
code: CREDAREA
type: index
area: credentials
summary: >-
  Lists the capabilities that hold credentials and deliver them to sessions and pods.
---

# Credentials

Lists the capabilities that hold credentials and deliver them to sessions and pods.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Account links](account-links.md) | `LINK` | States how a user links an account through OAuth, the link prompt, and the link check before a session needs the account. |
| [Credential add-ons](credential-add-ons.md) | `ADDON` | Defines the credential add-ons for GitHub and GitLab, their kinds, and their deny presets. |
| [Credential delivery](credential-delivery.md) | `CDELIV` | States how credentials reach a runtime: delivery modes, file delivery, and what a runtime may read. |
| [Credential pools](credential-pools.md) | `CPOOL` | Defines credential pools for VCS providers, their secret topology, leases, governance boundaries, emergency revocation, and secret lifecycle. |
| [Credential request rules](credential-request-rules.md) | `CREQ` | States the request rules and presets that decide which credentials a session may request and receive. |
| [Pull request authoring](pull-request-authoring.md) | `PRAUTH` | States how a session authors pull and merge requests under a user's identity and the independent approval check. |
| [Repository authorization](repository-authorization.md) | `REPOAUTH` | States which repositories a session may reach under a user's VCS identity and how that authorization is decided. |
| [Service account credentials](service-account-credentials.md) | `SACRED` | Lets a service account own credentials through the client credentials and issuer exchange sources. |
| [User credentials](user-credentials.md) | `UCRED` | Defines user credential types, the registered source, the user credential binding, and request rules and presets. |
