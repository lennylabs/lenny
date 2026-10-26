# Spec changes — Production mode has two spellings, so no single setting turns on every production gate

## Design (as the spec must state it)

SPEC-1 adds a **Production mode.** paragraph to §17.9.1 that defines production mode on the existing Environment dimension. SPEC-2 rewrites every site that spells production mode `LENNY_ENV=production` to cite that paragraph and restate none of it.

After both edits, spec/ contains no `LENNY_ENV=production`, and no spec text names a second production signal.

## Edge cases and accepted failure modes

- **§5.3.** The cosign rule names "production or staging" itself and is not edited (D9).
- **Preflight Job.** The preflight Job reads the same chart value and does not validate it. On an off-set value the long-running pods refuse to start, and the preflight warnings stay advisory.
- **Operator guidance.** Advice addressed to production deployments in general (for example, "Never run `etcdctl defrag --cluster` in production") is outside the definition, as the paragraph's closing sentence states.

## Staged edits

### SPEC-1 · spec/17_deployment-topology.md § 17.9.1 Composition Dimensions

Edit (a). Under the heading "#### 17.9.1 Composition Dimensions", in the dimensions table, in the row that begins "| **Environment** |", replace the Drives cell text "Alert thresholds, log verbosity," with:

```
Production mode (see **Production mode.** below), alert thresholds, log verbosity,
```

The row stays one physical table line.

Edit (b). Under the same heading, insert the following paragraph after the dimensions table and before the paragraph that begins "Each dimension has an independent default":

```
**Production mode.** A deployment runs in production mode when its Environment is `prod`. The Environment is carried by the `--environment` flag. The chart renders that flag from its single `environment` value on the gateway, the Token Service, lenny-ops, and the preflight Job. The gateway, the Token Service, and lenny-ops read the `LENNY_ENV` environment variable when started without the flag, and the flag takes precedence when both are set. The value is compared exactly, with no case folding and no whitespace trimming. The gateway, the Token Service, and lenny-ops refuse to start when the value is non-empty and is not one of `local`, `dev`, `staging`, or `prod`. An unset value means the deployment is not in production mode. `staging` is not production mode. Wherever this specification makes the behavior of a Lenny component depend on production mode, on running in production, or on a production deployment, that behavior applies when the Environment is `prod`. No other flag, environment variable, or chart value turns production mode on or off. Operator guidance addressed to production deployments in general does not depend on this definition.
```

### SPEC-2 · spec/11_policy-and-controls.md § 11.7; spec/12_storage-architecture.md § 12.3; spec/16_observability.md § 16.4 and § 16.5; spec/17_deployment-topology.md § 17.6

Each edit is located by heading and quoted text. Every edited table row stays one physical line, and the column padding of the row is otherwise unchanged.

Edit (a). In §11.7 Audit Logging, numbered item 1 "**Startup grant verification (hard-fail in production):**", replace "In production mode (`LENNY_ENV=production`), any unexpected grant" with:

```
In production mode ([§17.9.1](17_deployment-topology.md#1791-composition-dimensions)), any unexpected grant
```

Edit (b). In §11.7, in the bullet that begins "**Gateway startup with a regulated tenant and no SIEM is a fatal error** in production mode", replace "(`LENNY_ENV=production`)" with:

```
([§17.9.1](17_deployment-topology.md#1791-composition-dimensions))
```

Edit (c). In §11.7, in the bullet that begins "In **production mode** (`LENNY_ENV=production`) with any tenant whose `complianceProfile` is regulated", replace "(`LENNY_ENV=production`)" with:

```
([§17.9.1](17_deployment-topology.md#1791-composition-dimensions))
```

Edit (d). In §12.3 Postgres HA Requirements, in the paragraph that begins "**When no SIEM is configured (Postgres-only deployments):**", replace "In `LENNY_ENV=production` mode without SIEM" with:

```
In production mode ([§17.9.1](17_deployment-topology.md#1791-composition-dimensions)) without SIEM
```

The sentence in the same section "`auditFlushIntervalMs` is ignored for these events regardless of `LENNY_ENV` or SIEM configuration." is not edited.

Edit (e). In §16.4 Logging, in the paragraph that begins "**SIEM responsibility split.**", replace "and `LENNY_ENV=production`, the gateway emits" with:

```
and the deployment runs in production mode ([§17.9.1](17_deployment-topology.md#1791-composition-dimensions)), the gateway emits
```

Edit (f). In §16.5 Alerting Rules and SLOs, in the row that begins "| `AuditSIEMNotConfigured`", replace the leading "`LENNY_ENV=production` and" of the Condition cell with:

```
Production mode ([§17.9.1](17_deployment-topology.md#1791-composition-dimensions)) and
```

Edit (g). In §16.5, in the row that begins "| `AuditRetentionLow`", replace the leading "`LENNY_ENV=production` and" of the Condition cell with:

```
Production mode ([§17.9.1](17_deployment-topology.md#1791-composition-dimensions)) and
```

Edit (h). In §17.6 Packaging and Installation, under "#### Checks performed", in the row that begins "| etcd Secret encryption (warning)", replace "When `LENNY_ENV=production`:" with:

```
In production mode ([§17.9.1](#1791-composition-dimensions)):
```

Edit (i). In the same table, in the row that begins "| Node disk encryption (warning)", replace "When `LENNY_ENV=production`:" with:

```
In production mode ([§17.9.1](#1791-composition-dimensions)):
```

Edit (j). In the same table, in the row that begins "| SIEM endpoint (warning)", replace "When `LENNY_ENV=production` and `audit.siem.endpoint` is not set:" with:

```
When the deployment runs in production mode ([§17.9.1](#1791-composition-dimensions)) and `audit.siem.endpoint` is not set:
```

After edits (a) to (j), an exact search for `LENNY_ENV=production` over spec/ returns no match.

## Spec files touched

- `spec/17_deployment-topology.md` (SPEC-1; SPEC-2 edits (h) to (j))
- `spec/11_policy-and-controls.md` (SPEC-2 edits (a) to (c))
- `spec/12_storage-architecture.md` (SPEC-2 edit (d))
- `spec/16_observability.md` (SPEC-2 edits (e) to (g))
