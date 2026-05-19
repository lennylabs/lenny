# Installation answer files

This directory holds answer files for `lenny-ctl install --answer-file`
and the documented schema those files follow.

An answer file is a YAML mapping of installation question IDs to
answers. `lenny-ctl install --answer-file <path>` reads the file,
composes a Helm values document from it, layers the matching capacity
tier preset under that document, and runs `helm install`. The same
flow runs interactively when `--answer-file` is omitted; an interactive
run can be captured with `--save-answers <path>` and replayed later, so
answer files make installs repeatable in CI and IaC pipelines.

## Catalog

| Answer file | Environment | Tier | Notes |
|---|---|---|---|
| `tier1-local.yaml` | `local` | `tier1` | Local or development install. Uses `dev` auth mode and the chart's in-memory store fallbacks. |
| `tier2-prod.yaml` | `prod` | `tier2` | Mid-range production install. Uses OIDC auth and `${VAR}` references for the data-store DSNs. |
| `tier3-prod.yaml` | `prod` | `tier3` | Largest production envelope. Enables the optional admission webhooks and a Kata agent namespace. |

Each answer file pairs with the tier preset of the same tier under
`charts/lenny/presets/`. The wizard selects the preset from the `tier`
field, so the answer file and the preset stay consistent.

## Schema

Every field below is optional. Unset fields fall back to the wizard
default listed in the Default column. Unknown keys are rejected so a
typo fails the install rather than being silently ignored.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `release.name` | string | `lenny` | Helm release name. The `--release` flag overrides it. |
| `release.namespace` | string | `lenny-system` | Release namespace. The `--namespace` flag overrides it. |
| `environment` | string | `local` | Target environment: `local`, `dev`, `staging`, or `prod`. |
| `tier` | string | `tier1` for `local` and `dev`, `tier2` for `staging` and `prod` | Capacity tier: `tier1`, `tier2`, or `tier3`. Selects the preset under `charts/lenny/presets/`. |
| `profile` | string | empty | Advisory answer-file base name recorded from cluster detection. Not loaded as a second values file. |
| `domain` | string | empty | Gateway external DNS name. Empty keeps the chart default. |
| `tls` | string | empty | TLS strategy: `cert-manager` or `bring-your-own`. |
| `postgres.dsn` | string | empty | Postgres DSN. Empty keeps the chart default. |
| `redis.url` | string | empty | Redis connection URL. Empty keeps the chart default. |
| `objectStorage.endpoint` | string | empty | Object-store endpoint. Empty keeps the chart default. |
| `objectStorage.bucket` | string | empty | Object-store bucket name. |
| `auth.mode` | string | `dev` for `local`, `oidc` otherwise | Platform auth mode: `oidc` or `dev`. `dev` is valid only for the `local` environment and sets `global.devMode`. |
| `auth.oidcIssuer` | string | empty | OIDC issuer URL. Required when `auth.mode` is `oidc` and the environment is not `local`. |
| `auth.oidcClientId` | string | empty | OIDC client ID. Required when `auth.mode` is `oidc` and the environment is not `local`. |
| `agentNamespaces` | list of strings | empty | Namespaces that hold agent pods. Empty keeps the chart default (`lenny-agents`, `lenny-agents-kata`). |
| `features.llmProxy` | bool | `false` | Enables the LLM-proxy admission webhook. |
| `features.drainReadiness` | bool | `false` | Enables the drain-readiness admission webhook. |
| `features.compliance` | bool | `false` | Enables the compliance admission webhooks. |
| `devMode` | bool | `false` | Sets `global.devMode`. Valid only for the `local` environment. Set automatically when `auth.mode` is `dev`. |

## Environment-variable references

A value of the form `${VAR}` is replaced with the value of the
environment variable `VAR` when the answer file is read. An unset
variable expands to the empty string. Use this for secret material such
as data-store DSNs so the answer file holds no plaintext credentials.

```yaml
postgres:
  dsn: "${LENNY_PG_DSN}"
redis:
  url: "${LENNY_REDIS_URL}"
```

## Validation rules

`lenny-ctl install` rejects an answer file when any of the following
hold:

- `environment` is not one of `local`, `dev`, `staging`, or `prod`.
- `tier` is not one of `tier1`, `tier2`, or `tier3`.
- `auth.mode` is not one of `oidc` or `dev`.
- `auth.mode` is `oidc`, the environment is not `local`, and either
  `auth.oidcIssuer` or `auth.oidcClientId` is empty.
- `auth.mode` is `dev` and the environment is not `local`.
- `devMode` is true and the environment is not `local`.
- `tls` is set to a value other than `cert-manager` or `bring-your-own`.
- an entry in `agentNamespaces` is not a valid Kubernetes namespace name.
