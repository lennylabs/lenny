# Review history: 0130-gitlab-credential-addon GitLab credential add-on

# Revision notes

- Revision 1, 2026-11-06, took its content from [`0152-user-git-identity`](../0152-user-git-identity/change.md) revision 27 and [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) revision 18.
- Revision 2 followed a pass for a reader new to the project. It added a tag table and an evidence note, glosses for GitLab vocabulary in "Terms", "Terms from other designs and the platform", "Labels from other designs", a plain-language opening and a diagram in "High-level design", and links to the other designs and their labels. It names the pod credential API in place of I8, moves "Terms" before "High-level design", and moves revision history out of the status line, and it changed no rule.
- Revision 3 corrected a pointer that a validation pass found, in which the V5 row of "Validation owed" placed S13 and S17 in `0152-user-git-identity` although they are rows of this document's table, and changed no rule.
- Revision 4 updated the "Covered host" entry in "Terms from other designs and the platform" after a re-read of `0151-user-credential-delivery` and `0152-user-git-identity` at their current revisions, and changed no rule. A covered host is now a git host of a VCS pool's repository prefixes or of a git route, with a VCS kind's API hosts named separately, as `0152-user-git-identity` revision 29 corrected it, and the entry states that a GitLab host serves git and the API on one host.
- Revision 5, dated 2026-11-09, applies the design sync review of 2026-11-08, and changes no rule. The glosses of `netagent` and the covered host match the egress design's "Terms", and the term "credential classes" becomes the egress design's "provider capability levels". It defines `gitlab-fine-grained` under "Terms", repairs the gloss of the preset properties, cites the withdrawal of option F by the finding that recorded it, and renames the header of the family table under "Preset body".
- Revision 6, dated 2026-11-13, applies the cross-design review of that day and changes no rule of this add-on. The `netagent` term states per-slot scopes under the egress design's decision 60, settled on 2026-11-13. "Documentation" is new and lists every setting, default, provider requirement, limit, failure mode, and runtime-facing rule, under the owner's extension of decision D13 to every design. The fix pass of the same day updated the revisions this design reads against, `0151-user-credential-delivery` revision 29 and `0127-egress-and-git` revision 77, and changed no rule. The check pass of the same review changes no rule and updates the revisions this design reads against, `0151-user-credential-delivery` revision 30 and `0127-egress-and-git` revision 78.
  - The last round of the same review, which changes no rule, cites the designs this one reads against at their current revisions.
  - The owner's answers of 2026-11-13 to the consolidated list of open items change no rule of this add-on, and the status line cites the designs at their current revisions.

## Move into changes (2026-11-16)

Change 0113 moved this design from `designs/gitlab-credential-addon-design.md` into this folder. Its review history moved into this file, its proposal plan became "Steps", its evidence moved into `evidence/`, its status became `Draft` pending the owner's final cross-review, and every path to a design or its evidence, in links and in prose, now names its new location.

## Status line before the split (2026-11-16)

Change 0113 shortened the status line of `change.md` to the status, revision, date, and program, and moved the rest here as it stood:

> Nothing in this document is built or staged. Revision 6 applies the cross-design review of 2026-11-13: it states per-slot `netagent` scopes in the `netagent` term and adds "Documentation" under the owner's extension of decision D13 of [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/decisions.md#decisions) to every design, and changes no rule of this add-on. It reads against [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) revision 40, [`0152-user-git-identity`](../0152-user-git-identity/change.md) revision 45, and [`0127-egress-and-git`](../0127-egress-and-git/change.md) revision 92. Decisions 1 and 2 carry recommendations for owner review. No live GitLab write, permission check, or quick action has run. The measurements cited ran against a recording stub, and **the stub proves forwarding only**. Statements about GitLab come from the pages under "Sources". Proposals carry the label GL-n. "Review history" at the end records how the document reached this form. The owner's final cross-review of the moved designs is pending, so the status is `Draft`.

## Revision 7 (2026-11-19)

- Date: 2026-11-19. Trigger: review. Name: M3 readiness review (`scratchpad/m3-readiness/reviews/0130.md`, the skeptic's verdict in `scratchpad/m3-readiness/verified/0130.md`, and the consistency report credentials-git; the program report names 0130 only in its layer and milestone tables). The skeptic downgraded the one High finding, 0130-F1, to Medium and corrected its fix.
- Counts: 22 applied (17 findings and this change's side of 5 conflicts), 6 raised as open decisions, 0 returned as issues, 0 dropped. Decisions 1 and 2, which carried no owner answer, moved to "Open decisions", so 8 decisions are open.
- Applied:
  - 0130-F1 (Medium after verification) and credentials-git-C1: 0130.S2 depends on 0130.S3, and 0130.S3 on 0130.S1. The request layout table moved from GL-3 to GL-1 as "Request layout", registered with the kind in 0130.S1 under the skeptic's correction; GL-3's "Default preset" registers `gitlab-deny` as the flavour's default preset; the "Request layout and API flavour" term states the split.
  - 0130-F2: decisions 1 and 2 moved from "Decisions" to "Open decisions" with their recommendation, cost, and alternatives restated as options, so the board counts them.
  - 0130-F3 (the structure): GL-2 gains "Fine-grained token per session", with the template's fields, the template variables, the mint, the empty set and its 403 with `no_granted_repositories`, the cache, single mint on concurrent first requests, the revocation record written before the mint, 401 handling, failed mints, and notifications, each S13-dependent part marked **[pending]** S13. The overridable fields are open decision 6.
  - 0130-F5: GL-2 states that `prAuthoring: user` on a host below 19.2 is refused at the VCS pool write with `400 VALIDATION_ERROR` and at bind with `gitlab_version_unsupported`, with no fallback to option D; the worked example of GitLab 18.x follows.
  - 0130-F8 and credentials-git-C15: GL-3 "Headers." states that `netagent` removes `Sudo` on every GitLab host through the per-host credential list of the egress design's host data; GLR-10, a `security` test row, and 0130.S3 carry it.
  - 0130-F9: GL-1 states the interim write check (the higher of `project_access` and `group_access`, refused when neither is present, **[pending]** S11), and 0130.S1 and the S11 row state that S11 runs before 0130.S1 starts.
  - 0130-F10: "Requirements" (GLR-1 to GLR-11) and "Specification and non-specification changes" (ADDON, PRAUTH, `spec/catalogs/settings.md`, `spec/catalogs/errors.md`, the egress host data, the add-on package, and the CI job) are new.
  - 0130-F11: "Tests" gains `integration` and `security` sections and rows for GL-1, the template, the revocation record, 401, the version refusal, `Sudo`, `refuseDecoded`, `denyBodyKeys` at depth, undecodable and multipart bodies, and every line separator, each naming the requirement it verifies.
  - 0130-F12: "Documentation" names each page and its reader: the GitLab sections of 0152's tenant admin page and runtime author section, `docs/reference/configuration.md`, `docs/reference/error-catalog.md`, `docs/reference/glossary.md`, and 0152's security page; the failure modes list the new refusals.
  - 0130-F13: `refuseDecoded` lists the prototype's ten words in full, and states that 0151 refuses a decoded dot segment.
  - 0130-F15: the test tiers use their names (`unit`, `contract`, `integration`, `e2e_cloud`, and `security`), and the Token Service term links `spec/components/token-service.md` (TOKSVC).
  - 0130-F16: the Review family states that no GraphQL approval mutation has been identified, and S17 owes a read of the GraphQL schema.
  - 0130-F18 and credentials-git-C14: the project token term and the `netagent` residual state reuse under the `storedTokenPush` grant with its 7-day rotation.
  - 0130-F19: GL-2 and the runtime-facing rules state that Lenny sets `GLAB_CHECK_UPDATE` and `GLAB_SEND_TELEMETRY` in the convention variables of 0127.
  - 0130-F20: GL-2 states the GitLab.com Free case, **[pending]** S13, and S13 asks whether GitLab.com Free can create a project access token.
  - 0130-F21: "Documentation" names the GitLab forms of assignment-triggered and label-triggered merge bots under 0152 decision 35.
  - 0130-F22: GL-3 "Keeping the preset current." and 0130.S3 add the scheduled CI job.
  - credentials-git-C9: the pending-revocation record term cites the single definition that 0151 lands under "Lifecycle and revocation at the provider", and GL-2 sets the GitLab deadline at the token's `expires_at`.
  - credentials-git-C10 (the text): GL-2 states the template in full and resolves the contradiction between "narrowed to `granted` entries" and "does not follow entry state": the token is narrowed at each mint and does not follow a later change; the flag's value is open decision 4.
- Raised as open decisions:
  - Open decision 3, the parent rows S13 can select and the `scopes` a `gitlab` link provider admits (0130-F4; names 0151 and 0152).
  - Open decision 4, `followsEntryState` on `gitlab-fine-grained` (0130-F6, credentials-git-C10; names 0152 and 0142).
  - Open decision 5, `pushIdentity` on GitLab, settled with 0152 decision 47 (0130-F7, credentials-git-C2; names 0152).
  - Open decision 6, the overridable fields of `gitlab-fine-grained` (0130-F3).
  - Open decision 7, the REST equivalents of `/target_branch` and `/run_pipeline` (0130-F14; names 0151).
  - Open decision 8, the Policy family under a Maintainer-role project token (0130-F17; names 0127).
- Returned as issues: none. The tree holds no GitLab code, and the placeholder pages ADDON and PRAUTH already name this change.
- Dropped: none.
- Second pass. The second pass closed the edits that 0127, 0129, 0151, and 0152 requested of this change, which hold 8 items. Counts: 6 already done, 0 applied as new edits, 2 already open as decisions, 0 raised, and 0 dropped. One pointer edit was applied inside an item already open as a decision.
  - Already done: the `Sudo` removal in GL-3 "Headers." (0127 and 0151, credentials-git-C15); the request layout registration in 0130.S1 and 0130.S3 in the depends-on of 0130.S2 (0129 and 0152, credentials-git-C1); the pending-revocation record term and GL-2's revocation citing UC-2's "Lifecycle and revocation at the provider" in 0151 (0151, credentials-git-C9); and project token reuse under `storedTokenPush` in "Terms" (0152, credentials-git-C14).
  - Already open as decisions: `followsEntryState` on `gitlab-fine-grained` is open decision 4, and GL-2 states the rest of the template in full (0152, credentials-git-C10); the GitLab `pushIdentity` value is open decision 5 (0152, credentials-git-C2).
  - Applied: open decision 5 and GL-2 name [`0152-user-git-identity` decision 57](../0152-user-git-identity/decisions.md#open-decisions) as the matching decision, and GL-2 states that it gives the value once those decisions are settled.
