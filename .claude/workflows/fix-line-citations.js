// fix-line-citations: hand-correct the line-citation sites the line pass refuses.
//
// WHY THIS EXISTS. `specshift -pass line` is fail-closed. Over the code phase it
// aborts with the tree unchanged on 80 sites across 63 files that it cannot
// convert mechanically, and its own message for each is that the site "is left
// for hand correction". Those 80 sites block the rest of proposal 0064: SPEC-3's
// reduction of spec/04 §4.7 and spec/15 §15.4 is one atomic sub-step with the
// line pass over the reduced files, and that pass cannot run while the code phase
// aborts.
//
// NO SPEC EDIT IS INVOLVED. Every one of the 80 sites is in pkg/, tests/, cmd/,
// charts/ or docs/. None is under spec/. The fix is a comment or literal
// correction that brings the tree into line with a rule already in force:
// code-best-practices.md requires `// spec: §X.Y` citations and forbids line
// numbers in them because they shift.
//
// THE UNIT is a group of files, and no two groups share a file, so the agents
// never write the same file concurrently. Partitioning by failure class instead
// would have put two agents in one file, since several files carry sites of more
// than one class.
//
//   Workflow({ scriptPath: ".claude/workflows/fix-line-citations.js", args: {
//     repoRoot: "/abs/path",
//     groups: [ ["path/a.go", "path/b.go"], ... ]
//   }})

export const meta = {
  name: "fix-line-citations",
  description:
    "Hand-correct the line-citation sites the migration's line pass refuses to convert, one file group per agent, then verify the pass runs clean",
  phases: [
    { title: "Correct", detail: "one agent per file group" },
    { title: "Verify", detail: "re-run the line pass dry run over the code phase" },
  ],
};

let input = args;
if (typeof input === "string") input = JSON.parse(input);
if (!input || !input.repoRoot || !Array.isArray(input.groups)) {
  throw new Error("args.repoRoot and args.groups are required");
}
const repo = input.repoRoot;
const groups = input.groups;

const DRY_RUN =
  "go run ./scripts/specshift -pass line -register tests/registers/line-citations.yaml -except spec/";

const SCHEMA = {
  type: "object",
  additionalProperties: false,
  required: ["files", "sitesFound", "corrections"],
  properties: {
    files: { type: "array", items: { type: "string" } },
    sitesFound: { type: "integer" },
    corrections: {
      type: "array",
      items: {
        type: "object",
        additionalProperties: false,
        required: ["file", "class", "before", "after", "why"],
        properties: {
          file: { type: "string" },
          class: {
            type: "string",
            enum: ["recomposes", "straddles", "unresolvable-path", "would-delete-text", "test-predicate", "other"],
          },
          before: { type: "string", description: "The citation text as it stood, quoted verbatim." },
          after: { type: "string", description: "The citation text as written now." },
          why: { type: "string" },
        },
      },
    },
    residualEntries: {
      type: "array",
      description:
        "Sites left for the residual register rather than corrected, with the disposition and reason each entry needs.",
      items: {
        type: "object",
        additionalProperties: false,
        required: ["file", "member", "disposition", "reason"],
        properties: {
          file: { type: "string" },
          member: { type: "string" },
          disposition: { type: "string", enum: ["in-class", "excluded"] },
          reason: { type: "string" },
        },
      },
    },
    specGaps: {
      type: "array",
      description:
        "Sites where correcting the citation revealed that the specification content it points at is wrong or absent. These are NOT corrected. They go to a human because a specification change needs the proposal pipeline.",
      items: {
        type: "object",
        additionalProperties: false,
        required: ["file", "citation", "problem"],
        properties: {
          file: { type: "string" },
          citation: { type: "string" },
          problem: { type: "string" },
        },
      },
    },
    notes: { type: "string" },
  },
};

const RULES = `
You are hand-correcting spec citations that a migration pass refuses to convert
mechanically. The repository is ${repo}. Work there.

YOU OWN THE FILES LISTED BELOW AND NO OTHERS. Another agent owns every other file,
so never edit a file outside your list. Never edit anything under spec/: this task
involves no specification change, and a specification change would need a proposal.

HOW TO FIND YOUR SITES. Run, from the repository root:

    ${DRY_RUN}

It aborts and prints every refused site as \`path:line: reason\`. Read the reasons
for your own files. Do not rely on a stale list; the tree moves.

THE TARGET FORM. code-best-practices.md requires spec citations of the form
\`// spec: §X.Y\` and forbids line numbers in them, because line numbers shift.
Every site you are fixing carries a line number today. The corrected citation
names the section, and where a subsection carries the claim it names the
subsection. It never carries a line number, a line range, or a "line N" phrase.

THE FAILURE CLASSES AND WHAT EACH NEEDS.

  recomposes — converting the citation would rebuild the retired form out of the
  emitted anchor plus the prose sitting beside it, for example an anchor followed
  by "(line 5093)" already in the sentence, or a comment whose next line begins
  with a bare number. Reword the surrounding prose so the anchor and its
  neighbours cannot recombine into a section-plus-line form. Keep the sentence
  saying what it said.

  straddles — the cited range spans a section boundary, so no single anchor serves
  it. Determine which section actually carries the claim the citation is
  supporting and cite that one. When the sentence genuinely rests on two sections,
  cite both.

  unresolvable-path — the citation names a specification file that does not
  resolve. Some name a file that was renamed and some name one that never existed.
  Find the file that carries the content and cite it by section. Confirm the
  content is really there before repointing; do not guess from the filename.

  would-delete-text — stripping the served citation would remove text the sentence
  needs. Reword so the citation and the sentence are separable, then cite.

  test-predicate — BE CAREFUL HERE. Some sites are not citations at all: they are
  test predicates, regular-expression literals, or golden values that assert
  something ABOUT citations, and they legitimately contain the retired form
  because that is what they match on. Rewriting one of those silently breaks the
  gate it implements. When a site is a predicate rather than a citation, do not
  rewrite it to the target form: leave the file's behaviour intact and record it
  as a residual entry with disposition "excluded" and a reason saying it is a
  matcher rather than a citation.

PRESERVE BEHAVIOUR. These are comments, string literals and documentation. Change
no logic, no control flow, no test assertion semantics, and no chart value that is
consumed at render time. When a literal is consumed rather than merely read by a
human, say so and treat it as a predicate.

WHEN THE SPECIFICATION IS THE PROBLEM. If correcting a citation reveals that the
specification content it points at is wrong, missing, or says something other than
what the citing code claims, DO NOT invent a citation and do not correct the
specification. Record it under specGaps and leave the site alone. A specification
change goes through the proposal pipeline, and inventing a plausible target is the
failure this project has paid for repeatedly.

VERIFY YOUR OWN WORK. After editing, re-run the dry run above and confirm none of
your files still appears among the refused sites. The command still aborts while
other agents' files remain, which is expected; what matters is that yours are
gone. Then run \`gofumpt -l\` and \`goimports -l -local github.com/lennylabs/lenny\`
over any Go file you touched and fix what they report.

Report every correction with the before and after text quoted. Report honestly: a
site you could not correct is a residual entry or a spec gap, not a silent skip.
`;

phase("Correct");

const results = await parallel(
  groups.map((g, i) => () =>
    agent(
      `${RULES}

THE FILES YOU OWN (${g.length}):
${g.map((f) => "  " + f).join("\n")}`,
      { label: `fix:group-${i + 1}`, phase: "Correct", schema: SCHEMA },
    ),
  ),
);

const ok = results.filter(Boolean);
const corrections = ok.flatMap((r) => r.corrections || []);
const residual = ok.flatMap((r) => r.residualEntries || []);
const gaps = ok.flatMap((r) => r.specGaps || []);

log(
  `${ok.length}/${groups.length} groups done, ${corrections.length} corrections, ${residual.length} residual entries, ${gaps.length} spec gaps`,
);

phase("Verify");

const verdict = await agent(
  `The line-citation hand corrections are applied across the tree in ${repo}.

Run, from the repository root:

    ${DRY_RUN}

Report exactly what happens. If it still aborts, list every remaining refused site
as path:line with its reason, and say for each whether the file was one this round
was meant to cover. Do not fix anything: this is a verification step and another
round will handle what remains.

Then, whatever the outcome, run the spec-confined direction too and report it:

    go run ./scripts/specshift -pass line -register tests/registers/line-citations.yaml -only spec/

Finally run tier 0 and report its verdict:

    ./bin/lenny-test --tier static

with PATH including $HOME/bin. Note that tier 0 has a known pre-existing failure
on scripts/specshift/testdata/idpass/unparseable/, a deliberately unparseable
fixture; report whether that is the only failure or whether there are others.

Return a plain report. Be precise about exit codes and counts.`,
  { label: "verify", phase: "Verify" },
);

return {
  groups: ok.length,
  corrections,
  residualEntries: residual,
  specGaps: gaps,
  verdict,
};
