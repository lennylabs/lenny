---
code: CREDAREA
type: index
area: credentials
summary: >-
  Lists the capabilities that hold credentials and deliver them to sessions and pods.
---

# Credentials

The capabilities in this area hold credentials and deliver them to sessions. Credential pools hold platform credentials for VCS providers, users and service accounts own credentials of their own, account links obtain them through OAuth, and request rules decide what a session receives. A user's VCS identity authorizes repository access and pull request authoring.

## Reading paths

- To change what credentials a session receives, read [Credential request rules](credential-request-rules.md), then [User credentials](user-credentials.md), then [Credential delivery](credential-delivery.md).
- To change how a user links an account, read [Account links](account-links.md).
- To change pull request authoring, read [Pull request authoring](pull-request-authoring.md), then [Repository authorization](repository-authorization.md), then [Credential add ons](credential-add-ons.md).

## Capabilities

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Credential pools](credential-pools.md) | CPOOL | Defines credential pools for VCS providers, their secret topology, leases, governance boundaries, emergency revocation, and secret lifecycle. |
| [User credentials](user-credentials.md) | UCRED | Defines user credential types, the registered source, the user credential binding, and request rules and presets. |
| [Account links](account-links.md) | LINK | States how a user links an account through OAuth, the link prompt, and the link check before a session needs the account. |
| [Service account credentials](service-account-credentials.md) | SACRED | Lets a service account own credentials through the client credentials and issuer exchange sources. |
| [Credential delivery](credential-delivery.md) | CDELIV | States how credentials reach a runtime: delivery modes, file delivery, and what a runtime may read. |
| [Repository authorization](repository-authorization.md) | REPOAUTH | States which repositories a session may reach under a user's VCS identity and how that authorization is decided. |
| [Pull request authoring](pull-request-authoring.md) | PRAUTH | States how a session authors pull and merge requests under a user's identity and the independent approval check. |
| [Credential add ons](credential-add-ons.md) | ADDON | Defines the credential add-ons for GitHub and GitLab, their kinds, and their deny presets. |
| [Credential request rules](credential-request-rules.md) | CREQ | States the request rules and presets that decide which credentials a session may request and receive. |

## Components that carry out this area

- [Token service](../../components/token-service.md): The Token Service Deployment: its KMS access, secret reads, signing, upgrade and key rotation, and metrics.
- [netagent](../../components/agent-pod/netagent.md): The netagent sidecar: the local egress endpoint, per-slot attribution, git on covered hosts, and per-slot credential files.
- [KMS](../../components/external/kms.md): The KMS: keys Lenny uses, who may call them, rotation, and failure behavior.

## Interfaces this area exposes

- [Credentials and links](../../interfaces/client-api/credentials-and-links.md): User credential and account link operations.
- [Credentials and upstreams](../../interfaces/admin-api/credentials-and-upstreams.md): Credential pool, LLM upstream, and revocation operations.
