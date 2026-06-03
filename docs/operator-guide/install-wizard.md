---
layout: default
title: "Install Wizard"
parent: "Operator Guide"
nav_order: 1.2
---

# Install Wizard

`lenny-ctl install` composes a deployment into a Helm install. It collects a
set of installation answers, renders them into a Helm values document, layers
a capacity-tier preset under that document, and runs `helm install`. This page
covers the interactive wizard, the non-interactive answer-file path, and the
capacity-tier presets.

For the full set of install methods, including `lenny up` for local
evaluation and direct Helm, see [Installation](installation.html).

---

## Interactive wizard

Run `lenny-ctl install` with no `--answers` flag to start the interactive
wizard:

```bash
lenny-ctl install
```

The wizard prompts for one answer per line on standard input. Each prompt
shows its default in brackets; press Enter to accept the default. The wizard
asks for the release name and namespace, the target environment, the capacity
tier, the gateway domain and TLS strategy, the Postgres, Redis, and
object-storage connection details, the auth mode and OIDC issuer, the agent
namespaces, and the optional admission-webhook feature flags.

Some defaults depend on earlier answers. The default tier is `tier1` for the
`local` and `dev` environments and `tier2` for `staging` and `prod`. The
default auth mode is `dev` for the `local` environment and `oidc` otherwise.
The wizard prompts for the OIDC issuer URL and client ID only when the auth
mode is `oidc`.

After collecting the answers, the wizard validates them, prints the composed
Helm values and the `helm install` command it will run, and then runs Helm.
Pass `--dry-run` to print the composed values and the command without running
Helm.

### Capturing an interactive run

`--save-answers <path>` writes the resolved answers to a YAML file. The file
is a complete answer file, so a one-time interactive run can be captured and
replayed:

```bash
lenny-ctl install --save-answers ./answers.yaml
```

---

## Non-interactive answer-file path

`--answers <path>` reads a YAML answer file instead of prompting. This path
performs no terminal interaction, so it is repeatable for CI and
infrastructure-as-code pipelines:

```bash
lenny-ctl install --answers ./answers.yaml
```

`--non-interactive` requires `--answers` and never prompts; the install
fails fast when no answer file is supplied. The values composition and the
`helm install` command are identical to the interactive path. Only answer
collection differs.

The chart ships an answer file per tier under `charts/lenny/answers/`. Each one
pairs with the tier preset of the same tier.

| Answer file | Environment | Tier | Notes |
|---|---|---|---|
| `tier1-local.yaml` | `local` | `tier1` | Local or development install. Uses the `dev` auth mode and the chart's in-memory store fallbacks. |
| `tier2-prod.yaml` | `prod` | `tier2` | Mid-range production install. Uses OIDC auth and `${VAR}` references for the data-store connection strings. |
| `tier3-prod.yaml` | `prod` | `tier3` | Largest production install. Enables the optional admission webhooks and a Kata agent namespace. |

### Answer-file schema

An answer file is a YAML mapping of installation answers. Every field is
optional; an unset field falls back to a wizard default. Unknown keys are
rejected so a typo fails the install rather than being silently ignored.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `release.name` | string | `lenny` | Helm release name. The `--release` flag overrides it. |
| `release.namespace` | string | `lenny-system` | Release namespace. The `--namespace` flag overrides it. |
| `environment` | string | `local` | Target environment: `local`, `dev`, `staging`, or `prod`. |
| `tier` | string | `tier1` for `local` and `dev`, `tier2` for `staging` and `prod` | Capacity tier: `tier1`, `tier2`, or `tier3`. Selects the preset. |
| `profile` | string | empty | Advisory answer-file base name recorded from cluster detection. The wizard does not load it as a second values file. |
| `domain` | string | empty | Gateway external DNS name. Empty keeps the chart default. |
| `tls` | string | empty | TLS strategy: `cert-manager` or `bring-your-own`. |
| `postgres.dsn` | string | empty | Postgres connection string. Empty keeps the chart default. |
| `redis.url` | string | empty | Redis connection URL. Empty keeps the chart default. |
| `objectStorage.endpoint` | string | empty | Object-store endpoint. Empty keeps the chart default. |
| `objectStorage.bucket` | string | empty | Object-store bucket name. |
| `auth.mode` | string | `dev` for `local`, `oidc` otherwise | Platform auth mode: `oidc` or `dev`. The `dev` mode is valid only for the `local` environment and sets `global.devMode`. |
| `auth.oidcIssuer` | string | empty | OIDC issuer URL. Required when `auth.mode` is `oidc` and the environment is not `local`. |
| `auth.oidcClientId` | string | empty | OIDC client ID. Required when `auth.mode` is `oidc` and the environment is not `local`. |
| `agentNamespaces` | list of strings | empty | Namespaces that hold agent pods. Empty keeps the chart default of `lenny-agents` and `lenny-agents-kata`. |
| `features.llmProxy` | bool | `false` | Enables the LLM-proxy admission webhook. |
| `features.drainReadiness` | bool | `false` | Enables the drain-readiness admission webhook. |
| `features.compliance` | bool | `false` | Enables the compliance admission webhooks. |
| `devMode` | bool | `false` | Sets `global.devMode`. Valid only for the `local` environment. Set automatically when `auth.mode` is `dev`. |

The full schema, including the validation rules, is in
`charts/lenny/answers/README.md`.

### Environment-variable references

A value of the form `${VAR}` is replaced with the value of the environment
variable `VAR` when the answer file is read. An unset variable expands to the
empty string. Use this for secret material such as data-store connection
strings, so the answer file holds no plaintext credentials:

```yaml
postgres:
  dsn: "${LENNY_PG_DSN}"
redis:
  url: "${LENNY_REDIS_URL}"
```

The `tier2-prod.yaml` answer file uses this form. Export the referenced
variables before running the install:

```bash
export LENNY_PG_DSN="postgres://lenny:...@pg.acme.com:5432/lenny"
export LENNY_REDIS_URL="rediss://:...@redis.acme.com:6380"
lenny-ctl install --answers charts/lenny/answers/tier2-prod.yaml
```

---

## Capacity-tier presets

The wizard selects a tier preset from the `tier` answer and layers it under the
composed per-question values. Helm receives the preset as the first `-f`
argument and the composed values as the second, so a per-question override
wins on any overlapping key. The presets live under `charts/lenny/presets/`.

Each preset sets the component replica counts, the capacity-planning tier, and
the backup retention policy for one capacity envelope.

| Preset | Envelope | Gateway replicas | Controller replicas | Token service replicas | `lenny-ops` replicas | Backup retention |
|---|---|---|---|---|---|---|
| `values-tier1.yaml` | Local clusters, development, and small single-team production installs. | 2 | 2 | 2 | 2 | 7 days, 3 backups, 2 full. |
| `values-tier2.yaml` | Multi-team production installs with steady traffic. | 3 | 2 | 2 | 2 | 30 days, 10 backups, 3 full. |
| `values-tier3.yaml` | High-traffic production installs. Assumes a KEDA-driven gateway autoscaler. | 5 | 3 | 4 | 3 | 90 days, 30 backups, 7 full. |

Tier 2 and Tier 3 set `ops.production: true`, which turns on the confirmation
requirement for full backups. Tier 3 also enables the periodic test-restore
CronJob, which restores the latest full backup on the first day of each month.
Tier 1 leaves both off.

The preset overrides only the keys listed in its file header. Every other base
`values.yaml` key keeps its default. See [Scaling](scaling.html) for sizing
guidance and [Disaster Recovery](disaster-recovery.html) for the backup and
restore model.

---

## Command flags

| Flag | Effect |
|---|---|
| `--answers <path>` | Read answers from a YAML file. |
| `--non-interactive` | Require `--answers`; never prompt. |
| `--save-answers <path>` | Write the resolved answers back to a YAML file. |
| `--output-values <path>` | Write the composed Helm values to a file. |
| `--chart <path>` | Chart directory. The default is `charts/lenny`. |
| `--release <name>` | Override the release name from the answer file. |
| `--namespace <ns>` | Override the release namespace from the answer file. |
| `--offline` | Skip cluster-reachability detection probes. |
| `--skip-smoke-test` | Skip the post-install smoke test against the `chat` reference runtime. |
| `--dry-run` | Print the composed values and the `helm install` command without running Helm. |

The `--release` and `--namespace` flags override the answer file's release
coordinates, so one answer file can be reused across releases. `helm install`
runs as a subprocess; the install fails with a clear message when `helm` is not
on the `PATH`.

---

## Smoke test

After `helm install` succeeds, the wizard runs a smoke test against the `chat`
reference runtime. It polls the gateway `/healthz` endpoint for up to 120
seconds, then issues a `lenny/create_session` MCP round-trip when an admin
token is available. The wizard resolves the gateway URL from `LENNY_API_URL`,
falling back to `https://<domain>` when the answer file sets a gateway domain,
and resolves the token from `LENNY_API_TOKEN`. When no token is set the smoke
test stops after the health probe and reports the round-trip as skipped. When
no gateway URL can be determined the smoke test is skipped entirely.

A smoke-test failure prints the rollback procedure (`helm uninstall`) and exits
non-zero so a broken install is not reported as successful. Pass
`--skip-smoke-test` to omit the phase.

---

## Validation

`lenny-ctl install` rejects an answer set when any of the following hold:

- `environment` is not one of `local`, `dev`, `staging`, or `prod`.
- `tier` is not one of `tier1`, `tier2`, or `tier3`.
- `auth.mode` is not one of `oidc` or `dev`.
- `auth.mode` is `oidc`, the environment is not `local`, and either
  `auth.oidcIssuer` or `auth.oidcClientId` is empty.
- `auth.mode` is `dev` and the environment is not `local`.
- `devMode` is true and the environment is not `local`.
- `tls` is set to a value other than `cert-manager` or `bring-your-own`.
- An entry in `agentNamespaces` is not a valid Kubernetes namespace name.

A failed validation prints one message per problem and exits without running
Helm.
