# Proposal: Spec-file creation probe, to be deleted after use

- **Status:** **Applied to spec (2026-08-03).** Approved (2026-08-03) by jaf sign-off.
- **Date:** 2026-08-03.
- **Scope:** A throwaway probe that answers one question the authoring landing depends on: can the
  implementation pipeline create a new specification file, as opposed to editing an existing one? Every
  proposal the pipeline has applied so far has edited files that already existed. It stages the creation of
  `spec/28_communication-channels.md` carrying a heading and a single true sentence, together with its
  table-of-contents row, and is reverted as soon as the run returns. It stages no normative content, and
  the section it creates is a stub rather than the channel contract the approved proposal 0064 stages for
  the same path.

This document stages the proposed specification change. It does not modify any spec file. Apply the change
in the "Proposed changes" section after sign-off.

## 1. Problem

The authoring landing that unblocks the channel migration creates `spec/28_communication-channels.md`,
which does not exist today. The pipeline's apply phase locates an anchor in a target file and writes staged
text at it, and every proposal it has handled has named a file that already existed. Whether it can create
a file has never been exercised. Learning that it cannot, after the section's content is authored and
converged, would waste the most expensive part of that work; learning it now costs one run.

## 2. Proposed changes

### SPEC-1. Create the communication-channels section as a stub

**Target:** `spec/28_communication-channels.md` (new), and `spec/README.md`.

**Rationale:** A new section is appended at the end of its level and numbered as the next ordinal. The
highest existing section is 27, so 28 is the next ordinal and the file takes the name the index row points
at. The sentence below is true of the section and asserts nothing about any component.

**Change (staged description).** Create `spec/28_communication-channels.md` with exactly this content:

```
## 28. Communication Channels

This section states the communication channels between the gateway, the agent pod, the adapter, and the
runtime.
```

Then append one row to the table of contents in `spec/README.md`, after the last existing entry, which is
the `27.10 Roll-forward notes` row:

```
- [28. Communication Channels](28_communication-channels.md)
```

## 3. Non-goals

- **Any normative content.** The staged section is a stub. It states what the section is for and
  constrains no component.
- **Any permanent record.** This proposal and its spec edits are reverted as soon as the probe run returns.
  The section that eventually occupies this path is staged by proposal 0064 and its successors.

## 4. Testing

None. The proposal exists to exercise the pipeline rather than to change behavior, and its spec edits are
reverted before any tier runs against them.

## 5. Files touched on application

- `spec/28_communication-channels.md`, new, a heading and one sentence.
- `spec/README.md`, one appended table-of-contents row.
