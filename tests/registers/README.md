# Gate registers

This directory holds the registers that record what each tier-0 gate
accepts for now. A gate fails every violation its register does not
carry, so a register is the only way an exemption enters the tree.

## Which files the shared contract owns

Every file in this directory declares a `kind`. The shared validator in
`cmd/lenny-test/cmd_validate_yaml.go` holds each file declaring
`exception-register` to the entry schema and the ratchet rules below,
and leaves each other recognized kind to the gate that reads it. A file
declaring no recognized kind fails `validate-maps`, so a register that
no schema claims cannot sit in the tree unchecked.

| Kind | Schema | Validated by |
|:--|:--|:--|
| `exception-register` | The shared entry schema below. | The shared validator, on every run of `validate-maps`. |
| `residual-register` | A member, a class, an `in-class` or `excluded` disposition, and a reason. | The residual gate for that class. |
| `baseline` | Keyed for the rewrite it drives, and rewritten downward as that rewrite proceeds. | The gate that reads it. |
| `sense-map` | Keyed by file and occurrence, recording the identifier a pass writes at each site. | The pass that reads it, and the gate over its output. |

The filename convention is `exceptions-<gate>.yaml` for an exception
register and `residual-<class>.yaml` for a residual register. The name
is a convention for readers, and the `kind` key is what decides which
schema a file is held to.

## Entry schema

An exception register is a YAML document with `kind: exception-register`,
`version: 1`, and an `entries` list. Every entry carries:

| Field | Meaning |
|:--|:--|
| `subject` | The violation the entry exempts, in the vocabulary the gate measures. |
| `verdict` | The disposition of the exemption: `intentional` for a deliberate carve-out, `tracked` for remediation under way, or `deferred` for remediation accepted but not started. |
| `owner` | The person accountable for closing the entry. |
| `opened_at` | The date the entry was written, in `YYYY-MM-DD`. |
| `expiry` | The date the entry stops holding, in `YYYY-MM-DD`. |
| `blocker` | The open item whose closure retires the entry. |
| `reason` | Why the violation is accepted for now. |

Example, in `exceptions-spec-citations.yaml`:

```yaml
kind: exception-register
version: 1
entries:
  - subject: spec/04_system-components.md line 437
    verdict: tracked
    owner: alice
    opened_at: 2026-07-31
    expiry: 2026-09-30
    blocker: R3
    reason: The citation resolves after the section split lands.
```

## Ratchet rules

The shared validator applies three rules, and `validate-maps` runs it
inside the tier-0 static check:

1. A violation with no entry fails, so an exemption is written down
   before a gate lets it through.
2. An entry whose `expiry` has passed fails, so an exemption ends.
3. An entry whose `blocker` resolves to no open item fails, so an
   exemption names outstanding work.

The open-item domain the harness runs with holds two namespaces: the
findings still marked `OPEN` in `BUILD-GAPS.md` and `TEST-GAPS.md`, and
the steps the root-level remediation plans declare, including a sub-step
written with a lowercase suffix such as `R11a`. A gate lands green by
seeding its register with an entry blocked on the remediation step that
retires the entry, so a step identifier resolves in the same way a
finding identifier does, and a step leaves the domain when its plan
stops declaring it. A heading in a staged proposal under `proposals/` is
outside the domain: a proposal keeps declaring its steps after they
land, so a blocker naming one would never stop resolving.

A register file that is missing or does not parse fails rather than
exempting nothing silently.

## Residual registers

A file declaring `kind: residual-register` records the triage of the
members a class's broad predicate matches beyond its enumeration. It
holds a member, a class, an `in-class` or `excluded` disposition, and a
reason. An exclusion is permanent and an in-class entry is retired by
the event that takes its member out of its class, so neither carries a
date on which it becomes wrong nor an open item a blocker could name,
and the expiry and blocker rules would fail every such entry. The
residual gate validates those files against that schema.

## Baselines and sense maps

A baseline is keyed for the rewrite it drives. The per-file
line-citation counts, the per-citation resolution baseline, the
change-graph coverage baseline keyed by path prefix, and the skip-reason
baseline keyed by file and call site are all baselines, and each is
rewritten downward as its rewrite proceeds. A path with no change-graph
glob key, and a skip that fires because a host capability is absent,
name no pending item and carry no date on which they become wrong, so
the shared entry schema does not fit them. A sense map records which
identifier a pass writes at a given file and occurrence, which is a
decision rather than an exemption.
