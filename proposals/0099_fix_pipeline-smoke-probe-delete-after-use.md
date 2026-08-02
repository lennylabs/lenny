# Proposal: Pipeline smoke probe, to be deleted after use

- **Status:** **Applied to spec (2026-08-02).** Approved (2026-08-02) by jaf sign-off.
- **Date:** 2026-08-02.
- **Scope:** A throwaway probe that exercises the spec-apply phase of
  `implement-proposal` end to end. It stages one small, true, self-contained sentence into an existing
  specification section so that the phase's sub-step loop runs, its verification rounds converge, and the
  status line that reads the convergence flag after the loop is reached. It exists to prove a scope defect
  in the workflow is fixed and is deleted, together with its spec edit, as soon as the run returns.

This document stages the proposed specification change. It does not modify any spec file. Apply the change
in the "Proposed changes" section after sign-off.

## 1. Problem

The spec-apply phase declared its per-sub-step convergence flag inside the sub-step loop while reading it
after the loop, so every run that reached the read threw a `ReferenceError`. The defect is a scope error
rather than a parse error, so `node --check` accepts the file in both states and cannot distinguish them.
Only an actual run through the phase distinguishes them, and no proposal has taken that path since the
restructure: the last one to reach the apply phase stopped earlier for an unrelated reason, and the one
before it staged no specification edit at all and took the no-edits branch.

## 2. Proposed changes

### SPEC-1. Record what the reference-runtime catalog is for

**Target:** `spec/26_reference-runtime-catalog.md`, the appendix preamble.

**Rationale:** The preamble states what the appendix catalogs and who maintains the entries, and the
sentence below states what a reader is expected to take from it. The statement is true of the section as it
stands and adds no obligation on any component.

**Change (staged description).** In `spec/26_reference-runtime-catalog.md`, in the paragraph beginning
"This appendix catalogs the **reference runtimes**", append one sentence to the end of that paragraph:

```
Each entry is descriptive of a shipped runtime rather than normative on the platform.
```

## 3. Non-goals

- **Any behavioral change.** The staged sentence describes the appendix and constrains no component.
- **Any permanent record.** This proposal and its spec edit are reverted as soon as the probe run returns.

## 4. Testing

None. The proposal exists to exercise the workflow rather than to change behavior, and its spec edit is
reverted before any tier runs against it.

## 5. Files touched on application

- `spec/26_reference-runtime-catalog.md`, one appended sentence in the appendix preamble.
