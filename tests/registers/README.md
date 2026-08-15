# Gate registers

This directory holds the registers that record what each tier-0 gate
accepts for now. A gate fails every violation its register does not
carry, so a register is the only way an exemption enters the tree.

## Which files the shared contract owns

An exception register declares `kind: exception-register`. The shared
validator in `cmd/lenny-test/cmd_validate_yaml.go` holds every file
carrying that declaration to the entry schema and the ratchet rules
below. Every other file in this directory carries a schema of its own
and is validated by the gate that reads it, so the shared validator
passes over it and imposes no key on it.

| File | Schema | Validated by |
|:--|:--|:--|
| An exception register | The shared entry schema below, declared with `kind: exception-register`. | The shared validator, on every run of `validate-maps`. |
| A residual register | A member, a class, an `in-class` or `excluded` disposition, and a reason. | The residual gate for that class. |
| A baseline | Keyed for the rewrite it drives, and rewritten downward as that rewrite proceeds. | The gate that reads it. |
| A sense map | Keyed by file and occurrence, recording the identifier a pass writes at each site. | The pass that reads it, and the gate over its output. |

The filename convention is `exceptions-<gate>.yaml` for an exception
register and `residual-<class>.yaml` for a residual register. The name
is a convention for readers, and the `kind: exception-register`
declaration is what puts a file under the shared contract.

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
  - subject: spec/04_system-components.md §4.6
    verdict: tracked
    owner: alice
    opened_at: 2026-07-31
    expiry: 2026-09-30
    blocker: R3
    reason: The citation resolves after the section split lands.
```

A subject is written in the vocabulary the gate measures, and the
validator holds it to no format. A subject that names a specification
location uses the anchored form above. The citation migration rewrites
every citation that names a file and a line number into that form, and
its gates measure every tracked file in this directory, so a register
written in the retired form would enter the population that migration
is closing.

## Ratchet rules

The shared validator applies three rules, and `validate-maps` runs it
inside the tier-0 static check:

1. A violation with no entry fails, so an exemption is written down
   before a gate lets it through.
2. An entry whose `expiry` has passed fails, so an exemption ends.
3. An entry whose `blocker` resolves to no open item fails, so an
   exemption names outstanding work.

The open-item domain the harness runs with holds two namespaces:

- The findings still marked `OPEN` in `BUILD-GAPS.md` and `TEST-GAPS.md`.
- The steps the root-level remediation plans declare, including a
  sub-step written with a lowercase suffix such as `R11a`. A step leaves
  the domain when its plan stops declaring it.

A gate lands green by seeding its register with an entry blocked on the
remediation step that retires the entry, so a step identifier resolves
in the same way a finding identifier does. A heading declared by any
other document, including a document that stages work rather than
tracking it, is not an open item and fails the third rule.

A register file that is missing, empty, or unparseable fails rather
than exempting nothing silently. A file whose entries carry the shared
schema but that declares no `kind` fails as well, so a register cannot
leave the contract by an edit that drops the declaration. The sweep
reads both the `.yaml` and the `.yml` filename extensions.

## Residual registers

A residual register records the triage of the
members a class's broad predicate matches beyond its enumeration. It
holds a member, a class, an `in-class` or `excluded` disposition, and a
reason. An exclusion is permanent and an in-class entry is retired by
the event that takes its member out of its class, so neither carries a
date on which it becomes wrong nor an open item a blocker could name,
and the expiry and blocker rules would fail every such entry. The
residual gate validates those files against that schema.

A residual register declares `kind: residual-register`, `version: 1`,
and the class it records, and the gate refuses a register whose declared
class is not the one it is read for. Each entry is written as a literal
block scalar, so a member is a verbatim copy of the text its class's
predicate reads and no escape stands between the two. A member read
across a wrap keeps that wrap: the block scalar carries one line per
line of the occurrence, and the member the gate is keyed by is those
lines joined with a space. A citation wrapped across two comment lines
and stored on one line would stand as a live citation under the
register's own path, which the ratchet counts and the resolver reports.

A residual register is an ordinary member of the read domain the gates
and the passes share, so the naming lint, the identifier-resolution
gate, the citation resolver, and the ratchet all read the member and
reason text it carries and every pass may write it. The residual scan
reads none of them: a class's own register holds a copy of each member
it already triaged, and another class's register holds a copy of every
member whose text the two predicates both select, so a class that read
either would report the copy as a further member and would keep
selecting it after the pass had rewritten every carrier site, leaving
the entry that recorded it unremovable. The pass or baseline register a
gate consumes is excluded from that gate's own class alone.

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

The registers driving the `specshift` name, identifier, and anchor
passes are held here, except for the anchor-move map, which the anchor
pass takes on the command line and which sits at
`tests/spec-anchor-moves.json`.

| File | Pass that reads it | Schema | What empties it |
|:--|:--|:--|:--|
| `reserved-phrase-senses.yaml` | The `specshift` name pass. | `kind: reserved-phrase-senses`, `version: 1`, and an `entries` list keyed by `file` and 1-based `occurrence`, each naming the canonical identifiers the site denotes and, where it names more than one, the `replacement` text they sit in. | The change that runs the pass over the whole write domain, which leaves no site for an entry to resolve. That change has run, and the list is empty. |
| `pinned-spec-literals.yaml` | The `specshift` name pass. | `kind: pinned-spec-literals`, `version: 1`, and an `entries` list keyed by `file` and by the 1-based `literal` position among every string literal that file carries in source order. | Nothing. The pass requires the register whenever the tree carries a Go carrier under `tests/tier11_docs/`, and reads it as the filter admitting the literals that pin specification prose. |
| `identifier-senses.yaml` | The `specshift` identifier pass. | `kind: identifier-senses`, `version: 1`, and an `entries` list keyed by `file` and 1-based `occurrence`, each carrying either the `channel` the occurrence denotes or `not-a-channel: true`. An entry keyed by `path: true` instead resolves the retired spelling in the carrier's own file name. | It carries the occurrences under `spec/` alone, and the occurrences over the rest of the write domain, the file-name carriers included, are outstanding. The change that appends them and runs the pass over the remainder of the tree empties it. |
| `anchor-senses.yaml` | The `specshift` anchor pass. | `kind: anchor-senses`, `version: 1`, and an `entries` list keyed by `file` and by the 1-based `occurrence` among the retired-section citations that file carries in source order, each naming the `destination` heading in the `<path>#<anchor>` spelling. | The change that runs the pass over the whole write domain, which leaves no retired-section citation for an entry to resolve. |
| `../spec-anchor-moves.json` | The `specshift` anchor pass, which takes it as `-register`. | `kind: spec-anchor-moves`, `version: 1`, and a `moves` list of `{anchor, successor: {file, anchor}}`, keyed by the retired anchor a markdown fragment link names. | The same change, which leaves no link into a retired anchor. |

A sense map is emptied when the tree carries no site of its class left,
measured over the whole write domain rather than by the sub-step
finishing, so the emptied file records a rewrite that completed. The
name pass loads its emptied register and resolves nothing from it,
because a site with no entry aborts the run whatever the register holds,
so a run over a tree that still carries a site fails closed rather than
reporting the zero work of a completed migration. The class's residual
register is what records a member its broad predicate still matches
after the pass has run.
