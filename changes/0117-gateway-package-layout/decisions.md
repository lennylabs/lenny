# Decisions: 0117 Gateway package layout

## Open decisions

7. **`cmd/lenny-gateway`.** It holds 96 files and 23,425 lines of `package main`, against the rule in `code-best-practices.md` that binaries stay thin.
   - (a) Change only import paths and the constructors the `sessionserver` split needs.
   - (b) Also move each part's wiring into a constructor in the part's package, leaving `cmd/lenny-gateway` as a composition root that calls one constructor per part.

   Recommendation: (a). Milestone M2's exit needs the layout and the moves, and moving the wiring touches startup order and flag handling, which are behavior. (b) is better done as its own change after the layout lands, when each part's package exists to receive its wiring.


## Decisions

None yet.

Settled by the owner on 2026-11-17 ("go with all your recommendations, except ..."); the questions as framed are in `review-history.md`.

1. **Domain packages.** All 124 domain packages leave `pkg/gateway`.
2. **Directory level.** Concern directories under `pkg/`, extending the existing ones.
3. **REST handlers.** Each part registers its own routes with the API front through the registration interface in `pkg/gateway/seam`.
4. **`sessionserver`.** The seam, plumbing, and domain files move out, and the rest, about 19,400 lines, stays as one session core package.
5. **Packages a member change removes.** They stay in place, listed in the layout gate's register with the change that removes it. In the owner's words: "keep in place but remember to move at the end if for whatever reason they weren't deleted (fully or partially)". Step 0117.S13 runs before v1 closes and moves into the layout whatever a removal left, whole packages or parts, so the register is empty when v1 closes.
6. **Package names.** Directories change, and each move step settles the names of the packages it moves: a package keeps its name where the name fits its component page or concern, and is renamed in the same step where it does not, so that every package is named appropriately when v1 closes. In the owner's words: "let's make sure packages are named appropriately at the end of v1 (done at each step and not at the end)". No rename is deferred to a final pass.
