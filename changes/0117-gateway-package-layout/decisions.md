# Decisions: 0117 Gateway package layout

## Open decisions

None.

## Decisions

None yet.

Settled by the owner on 2026-11-17 ("go with all your recommendations, except ..."); the questions as framed are in `review-history.md`.

1. **Domain packages.** All 124 domain packages leave `pkg/gateway`.
2. **Directory level.** Concern directories under `pkg/`, extending the existing ones.
3. **REST handlers.** Each part registers its own routes with the API front through the registration interface in `pkg/gateway/seam`.
4. **`sessionserver`.** The seam, plumbing, and domain files move out, and the rest, about 19,400 lines, stays as one session core package.
5. **Packages a member change removes.** They stay in place, listed in the layout gate's register with the change that removes it. In the owner's words: "keep in place but remember to move at the end if for whatever reason they weren't deleted (fully or partially)". Step 0117.S13 runs before v1 closes and moves into the layout whatever a removal left, whole packages or parts, so the register is empty when v1 closes.
6. **Package names.** Directories change, and each move step settles the names of the packages it moves: a package keeps its name where the name fits its component page or concern, and is renamed in the same step where it does not, so that every package is named appropriately when v1 closes. In the owner's words: "let's make sure packages are named appropriately at the end of v1 (done at each step and not at the end)". No rename is deferred to a final pass.
7. **`cmd/lenny-gateway`.** Each move step also moves the wiring of the part it moves into that part's package: its constructor, the flags only it reads, and its route registration under decision 3. Shared startup (stores, telemetry, and shutdown order) stays in `cmd/lenny-gateway`, which thins with every step and ends v1 as a composition root. Step 0117.S1 first adds two guard tests: one that fixes the set of flag names, defaults, and help text, and one that fixes the startup and shutdown order; every later step keeps both green. Settled by the owner on 2026-11-17 ("I'm ok with 0117's decision 7"), after an elaboration that recommended this option over moving only import paths, consistent with decisions 5 and 6.

