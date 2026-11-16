# Issues

This folder holds every known discrepancy that no change has finished: code or tests that disagree with the specification, a specification that contradicts itself or is silent where it has to decide, and questions for the owner. Planned work is a step of a change under `changes/`, and it is not filed here.

Draft, 2026-11-16, from the specification structure spike. It becomes `issues/README.md` when the structure lands.

## Layout

```text
issues/
  README.md                                 this file
  INDEX.md                                  generated: open issues by area, issues no change claims, and issues per change
  POOL/
    POOL-I12-pod-set-label-not-immutable.md
  closed/
    POOL/
      POOL-I3-claim-ignores-revision.md
```

An issue lives under the code of the specification page that owns the requirement in question. Page codes survive any reorganization of the specification, so the folders never move. An issue is named `<CODE>-I<n>-<slug>.md`, and numbers are never reused within a code.

## Front matter

```yaml
---
id: POOL-I12
status: open            # open | planned | closed
kind: build-gap         # build-gap | test-gap | spec-defect | question
severity: medium        # critical | high | medium | low
requirements: [POOL-R5, KLABEL-R2]
change: 0113.S4         # the change step that will close it; set when status is planned
closed: fixed           # fixed | obsolete | duplicate | declined; set when status is closed
closed-by: 0113.S4      # the step, the design, or the issue that closed it
legacy: F-4.6.1.12      # the identifier in BUILD-GAPS.md or TEST-GAPS.md, for a migrated finding
---
```

The body has four headings: Specification (the requirement text, quoted), Evidence (file references), Gap, and Suggested resolution.

## Lifecycle

- An issue is filed `open`.
- It becomes `planned` when a change step lists it under `closes`. The step sets the `change` field.
- It becomes `closed` when that step lands, or when triage finds it obsolete or a duplicate, or when the owner declines it. The commit that closes it sets `closed` and `closed-by`, and moves the file to `closed/` under the same code.

## Migration from the gap records

`BUILD-GAPS.md` and `TEST-GAPS.md` stay frozen at the root as records. Each open finding is triaged against the designs as its area's specification pages land. A finding that a design removes or settles is filed directly in `closed/` as `obsolete`, with `closed-by` naming the design's change. A finding that survives is filed `open` under the code that owns its requirement, with `legacy` set.
