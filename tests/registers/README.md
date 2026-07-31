# Gate registers

This directory holds the registers that record what each tier-0 gate
accepts for now. A gate fails every violation its register does not
carry, so a register is the only way an exemption enters the tree.

## Entry schema

Each register is a YAML document with `version: 1` and an `entries`
list. Every entry carries:

| Field | Meaning |
|:--|:--|
| `subject` | The violation the entry exempts, in the vocabulary the gate measures. |
| `verdict` | The outcome the gate would report for the subject if the entry did not exist. `FAIL` or `UNVERIFIED`. |
| `owner` | The person accountable for closing the entry. |
| `opened_at` | The date the entry was written, in `YYYY-MM-DD`. |
| `expiry` | The date the entry stops holding, in `YYYY-MM-DD`. |
| `blocker` | The open item whose closure retires the entry. |
| `reason` | Why the violation is accepted for now. |

Example:

```yaml
version: 1
entries:
  - subject: spec/04_system-components.md line 437
    verdict: FAIL
    owner: alice
    opened_at: 2026-07-31
    expiry: 2026-09-30
    blocker: F-4.6.3
    reason: The citation resolves after the section split lands.
```

## Ratchet rules

The shared validator in `cmd/lenny-test/cmd_validate_yaml.go` applies
three rules, and `validate-maps` runs it inside the tier-0 static
check:

1. A violation with no entry fails, so an exemption is written down
   before a gate lets it through.
2. An entry whose `expiry` has passed fails, so an exemption ends.
3. An entry whose `blocker` resolves to no open item fails, so an
   exemption names outstanding work. The open-item domain the harness
   runs with is the findings still marked `OPEN` in `BUILD-GAPS.md` and
   `TEST-GAPS.md`.

A register file that is missing or does not parse fails rather than
exempting nothing silently.

## Residual registers

A file named `residual-<class>.yaml` records the triage of the members a
class's broad predicate matches beyond its enumeration. Those files
carry a different schema, holding a member, a class, an `in-class` or
`excluded` disposition, and a reason. An exclusion is permanent and an
in-class entry is retired by the event that takes its member out of its
class, so neither carries a date on which it becomes wrong nor an open
item a blocker could name. The residual gate validates them against
that schema, and the shared contract's validator skips them.
