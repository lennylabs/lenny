# Decisions: 0117 Gateway package layout

## Open decisions

8. **Seam operations that member changes remove, reshape, or add.** "SP-2" derives the operations from today's import edges, and 0117.S1 states them on `spec/interfaces/gateway-seams.md` as today's calls, confirmed by an inventory of the call sites behind each edge and, after the review of 0117.S1 and 0117.S2, of the crossings `cmd/lenny-gateway` wires through interfaces. Several member changes of the program change the mechanisms behind those operations, and none of them names an edit to the seam interface page:
   - Removed: 0138 RO-11 and RO-12 remove the client MCP tools, which are the only callers of `GWSEAM-CreateSession` and `GWSEAM-InvokeClientAPI` and of the `send_message` and `cancel_session` forwards of `GWSEAM-ForwardToolCall`. 0135 removes `lenny/request_input` (`GWSEAM-PendingInputWaits`, `GWSEAM-ResolveInputWait`, and `GWSEAM-ForgetInputWaits`) and replaces the executor's `Send` with `Deliver` (`GWSEAM-SendMessages`). 0133 LU-4 removes the pre-claim credential check (`GWSEAM-CheckChildCredentials`). 0141 removes the `ExperimentRouter` (`GWSEAM-ApplyExperimentRouting`). 0153 WM-9 moves archive extraction out of the gateway (`GWSEAM-RecordExtractionAbort`).
   - Reshaped: the held-delivery operations and `GWSEAM-EnqueueInbox` (0135 MD-3 and MD-5, 0146 SL-5 and SL-10), the delegation budget operations (0133 LU-6, 0146 SL-10), the metering proxy's budget extension and its budget state (`GWSEAM-ExtendForBudget`, `GWSEAM-RaiseBudget`, and `GWSEAM-TerminateForBudget`, 0133 LU-6), `GWSEAM-Orphaned` (0146 SL-9), the tool approvals, `GWSEAM-AwaitApproval` among them (0135 MD-19, 0142 SO-5a), the pod recycle operations (0145 D37 to D39, 0118 PL-8), `GWSEAM-WriteWorkspace` (0153 WM-9), `GWSEAM-AuthorizeSession` (0151 UCP-3, and the root client predicate of 0135 and 0146), route registration (0138 RO-4 and R7), and `GWSEAM-Publish`, which 0138 R1 needs to return the assigned sequence number.
   - Added crossings with no operation yet: child events into the parent's inbox (0135 MD-22, 0146 SL-8 and SL-12), `import_child_session_file` (0135 MD-26), `watch_child_session` (0135 MD-25), per-session tool availability (0126 DA-4, 0139 RP-4), the pending elicitation and approval lists (0138 R4), and webhook subscriptions to the event bus (0138 R6).

   Options:
   - (a) The seam interface page states today's operations, as "SP-2" says. Each member change that removes, reshapes, or adds a mechanism behind a seam operation edits the page and `pkg/gateway/seam` in its own step, and each of those changes gains a line naming the operations it edits.
   - (b) The page states the target state's operations now. The move steps 0117.S5 to 0117.S11 then implement interfaces whose mechanisms do not exist yet, or carry adapters from today's calls.
   - (c) The page states only the operations that survive into the target state, and the move steps keep the other crossings as registered violations until the member changes remove them.

   Recommendation: (a). The move steps run in milestone M2, before the areas, so they need the operations today's code makes, and a member change is where its mechanism changes. The conflict between members is raised on both sides under "Reading the program": the affected changes are 0118, 0122, 0126, 0133, 0135, 0138, 0139, 0142, 0145, 0146, 0151, and 0153. Raised by the build of 0117.S1 and 0117.S2 on 2026-11-17.

9. **Crossings at the wiring level.** The layout gate reads imports. A seam and the core also reach each other through an interface that one part declares and `cmd/lenny-gateway` satisfies with a value of another part, such as `mcptools.Terminal`, which the session server satisfies, or the activity stamper of `mcptools` and `llmproxy`, which `sessionidle.Stamper` satisfies. No import crosses there, so the gate does not see them, and GW-R1 as written covers them. The review of 0117.S1 and 0117.S2 found such crossings that the import inventory had missed, and the seam interface page now lists them.
   - (a) Add a gate over the wiring: a static check of `cmd/lenny-gateway` that every value of one part passed where another part expects an interface satisfies an interface declared in `pkg/gateway/seam`, with today's crossings listed in a register that only shrinks, as each move step routes its crossings through the seam interface.
   - (b) Narrow GW-R1 to the import rule, and leave the wiring to review and to the move steps, which route each crossing through `pkg/gateway/seam` as they split the parts.

   Recommendation: (a). An extraction puts a transport under the seam interface, and a crossing outside it would break at extraction with nothing in the tree reporting it. The check reads the same composition root the order guard of decision 7 reads, and the register keeps the move steps honest the way the layout register does. Raised by the review of 0117.S1 and 0117.S2 on 2026-11-17.

10. **Credential and admission packages.** The seam interface carries caller, tenant, and session scope as today's calls do, and the layout gate lets any part import a domain package. Nothing confines the packages that hold credential material or decide admission (`credentialstore`, `credleasestore`, the authentication middleware, `quota`, `policy`, and the tenant store) to the parts that use them today, the session core and the metering proxy, so a seam could read a credential store or bypass admission without crossing a part boundary the gate checks.
    - (a) Add a rule to the layout gate: only the session core, the metering proxy, the API front for authentication, and `cmd/lenny-gateway` import those packages, with today's other importers listed in the register.
    - (b) Leave them unconfined, as domain packages any component may import, and rely on review.

    Recommendation: (a). A seam extracted into its own service would take every credential and admission import with it, and the rule names the packages whose reach matters for the security model. The list of packages and the parts allowed to import each are part of the decision. Raised by the review of 0117.S1 and 0117.S2 on 2026-11-17.

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

