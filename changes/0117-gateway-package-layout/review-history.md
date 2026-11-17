# Review history: 0117 Gateway package layout

- 2026-11-16, revision 1. Created by hand under the number the v1 program reserves for milestone M2, and framed from TAXONOMY's "Gateway seams" and item 9 of its migration-spike items, the migration spike's code findings, the gateway rows of the map, 0115's placeholder pages for the gateway, §4.1 "Gateway Internal Subsystems", changes 0126, 0133, 0138, and 0153, and measurements of `pkg/gateway`, `cmd/lenny-gateway`, and their importers at `b1248f85b` ([evidence/](evidence/README.md)). Problem, scope, terms, the package tree mapped to the component pages, the seam interface, the layout gate, the moves and their order, the open decisions, and the steps.

- 2026-11-17, revision 2. The owner settled the open decisions except decision 7, which stays open pending an elaboration the owner asked for; decisions 5 and 6 carry the owner's amendments. The questions as framed before:

  **Open decisions before this revision**

  1. **Which domain packages leave `pkg/gateway`.** Item 9 of TAXONOMY's migration-spike items settled that shared stores leave the gateway and that domain logic several components share follows the capability areas. It did not settle a store or a rule that one gateway part uses alone. Of the 124 packages "SP-1" sends out, 25 are imported by another component, 17 by more than one gateway part, 28 by one part only (most of them by the admin handlers of the API front), and 54 only by other domain packages, `cmd/lenny-gateway`, or tests, most of them Postgres implementations.
     - (a) Every domain package leaves, whatever its readers today: 124 packages and 56,511 lines.
     - (b) A package leaves when another component or more than one part imports it; a package one part uses moves into that part's directory. About 40 of the 124 stay in the gateway, most of them under the API front's admin handlers.
     - (c) Stores stay in a `pkg/gateway/stores` directory, and only the packages other components import leave.

     Recommendation: (a). A store's rules live on a capability page under 0115 and its schema on the stored-schema page, so it has no gateway page to sit under. Under (b) the admin handlers would collect the stores they alone read, and each store would move again when a second part or component reads it. (a) also gives the layout gate one rule: every package under `pkg/gateway` belongs to a part.

  2. **Directory level for the packages that leave.** Item 9 says these packages follow the capability areas. The question is whether the directory is the area or the concern.
     - (a) Concern directories under `pkg/`, extending the ones that exist (`pkg/audit`, `pkg/quota`, `pkg/credential`, `pkg/delegation`, `pkg/events`, `pkg/session`, `pkg/runtime`), with new ones where none exists (`pkg/tenancy`, `pkg/billing`, `pkg/policy`, `pkg/storage`). Each package's `doc.go` names its pages, so the area is recorded once per package.
     - (b) One directory per capability area (`pkg/sessions`, `pkg/tenancyaccess`, `pkg/runtimespools`, and so on), with the existing concern packages moved under them.

     Recommendation: (a). The owner expects the areas to move (TAXONOMY owner decision 1), and a concern directory does not move when an area does. It extends the convention `code-best-practices.md` already states and moves no package outside the gateway.

  3. **Where the REST handlers live.**
     - (a) Each part registers its own routes with the API front through a registration interface in `pkg/gateway/seam`: the session routes with the core, upload routes with the upload handler, the event stream with the stream proxy, and the admin routes with the API front. The API front holds the pipeline, the registration helper, and the admin handlers.
     - (b) The API front holds every handler and calls the core and the seams through their interfaces.

     Recommendation: (a). A seam extracted to its own service takes its routes with it, and the API front then imports no part, which the layout gate can check. Under (b) the API front would import every part's interface and the handler code of a seam would stay behind at extraction.

  4. **How far this change splits `sessionserver`.**
     - (a) Move the upload, stream, platform tools, plumbing, and domain files (10,482 lines in 45 files) to their parts and packages, and move the remaining 19,400 lines in 64 files as one session core package, `pkg/gateway/core/session`.
     - (b) As (a), and also divide the remainder by the pages that own it: creation and start, claim and bind, resume and suspend, terminal handling, and messages.
     - (c) Move `sessionserver` whole into the core, and let each area change extract its part.

     Recommendation: (a). The layout gate needs the seams out of `sessionserver` and nothing more. Dividing the remainder follows capability pages that the area changes write, so (b) would draw boundaries before those pages exist. (c) leaves the seams mixed with the core until the last area lands.

  5. **Packages a member change removes.** 42 packages with 17,587 lines are removed by 0119, 0123, 0124, 0132, 0133, 0138, and 0141.
     - (a) Leave them in place. The layout gate's register lists each with the change that removes it, and the row leaves with the package.
     - (b) Move them into the layout like any other package.

     Recommendation: (a). A move of a package that is about to be deleted costs import rewrites in 450 import sites and buys nothing.

  6. **Package names.**
     - (a) Keep every package name and change only directories, giving a package a new name only where it would collide with a package already in the target directory.
     - (b) Rename packages to the names of their component pages, such as `podclaim` to `podclaimer` and `llmproxy` to `meteringproxy`.

     Recommendation: (a). In the migration spike's move of the pod claimer, 425 of the 593 changed lines were qualifier changes from the rename and 63 were import paths. A rename can follow in an area change once the package's code is rewritten anyway.

  7. **`cmd/lenny-gateway`.** It holds 96 files and 23,425 lines of `package main`, against the rule in `code-best-practices.md` that binaries stay thin.
     - (a) Change only import paths and the constructors the `sessionserver` split needs.
     - (b) Also move each part's wiring into a constructor in the part's package, leaving `cmd/lenny-gateway` as a composition root that calls one constructor per part.

     Recommendation: (a). Milestone M2's exit needs the layout and the moves, and moving the wiring touches startup order and flag handling, which are behavior. (b) is better done as its own change after the layout lands, when each part's package exists to receive its wiring.
- 2026-11-17, same revision. The owner settled decision 7: each move step moves the wiring of the part it moves out of `cmd/lenny-gateway`, behind guard tests on flags and startup order. Status `Decided`. The out-of-scope bullet that kept `cmd/lenny-gateway` unchanged beyond import paths no longer applies.
