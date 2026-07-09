export const meta = {
  name: 'close-test-gaps',
  description: 'Close one bounded, supervised batch of OPEN TEST-GAPS.md findings: select by theme/section and severity, implement each gated by an independent green-and-quality check (with a careful test-vs-code-fix decision tree), then batch-verify',
  whenToUse: 'Invoked by the close-test-gaps skill to drive TEST-GAPS.md toward closure, one reviewable batch at a time.',
  phases: [
    { title: 'Select', detail: 'pick up to batchSize still-open findings in scope, severity-first; resolve any found already-satisfied in passing' },
    { title: 'Close', detail: 'sequential per-finding loop: write a spec-derived test; if it fails, second-guess the test before the code, then fix only when spec-alignment is unambiguous with full blast-radius verification, else escalate to a human; independently verify; mark resolved' },
    { title: 'Verify', detail: 'batch-level lenny-test run --since <merge-base>, coverage --diff, validate-maps, pre-existing-vs-introduced triage on any failure' },
  ],
}

// args: { scope: string, batchSize?: number, severityOrder?: string[], maxAttemptsPerFinding?: number, branch?: string }
// scope forms: "theme:T-STD" | "section:11.7" | "section:25" (whole §25) | "all"
// args sometimes arrives JSON-encoded as a string rather than parsed into an
// object; parse defensively so a stringified call site still works.
const parsedArgs = typeof args === 'string' ? JSON.parse(args) : (args || {})
const scope = parsedArgs.scope || 'all'
const batchSize = parsedArgs.batchSize || 6
const severityOrder = parsedArgs.severityOrder || ['High', 'Medium', 'Low', 'Info']
const maxAttemptsPerFinding = parsedArgs.maxAttemptsPerFinding || 4
// branch: reuse an existing branch across several batches of the same
// category (checked out if it exists, created off the current branch if
// not) instead of always minting a fresh test-gaps/<scope>-<n> branch.
// Selecting against a branch that already carries prior batches' resolved
// findings is what makes this safe to call repeatedly; selecting against a
// stale base (e.g. re-running with no branch, or a branch behind the
// category's real progress) re-does already-closed work from scratch.
const explicitBranch = parsedArgs.branch || null

const SELECT_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['branch', 'selected', 'resolvedInPassing', 'remainingOpenInScope', 'alreadyEscalatedInScope'],
  properties: {
    branch: { type: 'string' },
    selected: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['id', 'section', 'severity', 'title', 'specDoc', 'gap', 'dependencies', 'suggestedTest', 'targetTier'],
        properties: {
          id: { type: 'string' },
          section: { type: 'string' },
          severity: { type: 'string' },
          title: { type: 'string' },
          specDoc: { type: 'string' },
          gap: { type: 'string' },
          dependencies: { type: 'string' },
          suggestedTest: { type: 'string' },
          targetTier: { type: 'string' },
        },
      },
    },
    resolvedInPassing: { type: 'array', items: { type: 'string' } },
    remainingOpenInScope: { type: 'integer' },
    alreadyEscalatedInScope: { type: 'integer' },
  },
}

function scopeSlugPlaceholder() {
  return scope.replace(/[^a-zA-Z0-9]+/g, '-').toLowerCase().replace(/^-+|-+$/g, '')
}

// This workflow's agents run Bash/Edit against the SAME checkout as whoever
// launches it (no worktree — one branch, worked sequentially, is all a
// single batch needs). That means the checkout's current branch changes to
// the batch branch for the duration of the run. Whoever launches this
// workflow must not run other git-mutating commands against this checkout
// until the run completes (see close-test-gaps SKILL.md); this is an
// operational rule for the launcher, not something the workflow can enforce
// on its own.

phase('Select')
const selectPrompt = [
  'ROLE. You are selecting the next batch of work from TEST-GAPS.md, the spec-vs-test coverage audit at the repo root.',
  '',
  'SCOPE for this run: ' + scope + '. Interpret it as: `theme:T-XXX` means the `## Theme —` section whose anchor is `t-theme-...` matching that prefix (grep TEST-GAPS.md for the T-XXX findings\' parent theme heading); `section:X.Y` means the `## §X.Y` section (or, for a bare section number like `section:25`, every `## §25.*` heading); `all` means the whole file.',
  '',
  explicitBranch
    ? ('1. Check out the branch this category is already accumulating on: `git checkout ' + explicitBranch + '` (it must already exist — this call is a continuation batch on that branch, not the first one). Report it as branch.')
    : ('1. Create and check out a fresh git branch off the current branch: `git checkout -b test-gaps/' + scopeSlugPlaceholder() + '-<n>` where `<n>` is one more than the highest existing `test-gaps/*` branch suffix (`git branch --list \'test-gaps/*\'`), or 1 if none exist. Report the exact branch name you created.'),
  '2. Within scope, grep for `^### - \\[ \\] T-.*— OPEN` to list candidate findings. SKIP any finding whose body already contains a `**Needs human input` field — a prior batch already escalated it and re-investigating it again without new information would waste effort; it stays excluded from selection until a human answer removes that field or the finding is otherwise updated. For each remaining candidate, read its full body (Spec/Doc, Existing tests, Gap, Dependencies, Suggested test).',
  '3. Re-verify each candidate is still real before selecting it (this repo may have moved since TEST-GAPS.md was last reconciled): check whether its Suggested-test file now exists on disk, and whether `tests/spec-map.json` now lists a covering test for its section. If a finding is already satisfied, resolve it right now: edit TEST-GAPS.md in place, flip it to `- [x] ... RESOLVED <sha>` (cite the actual landing commit via `git log --oneline --all -- <test file>`) with a **Resolution:** field, and record its id under resolvedInPassing. Commit this housekeeping edit by itself (`git commit -m "TEST-GAPS: resolve <ids> found already satisfied"`) before continuing.',
  '4. From the genuinely still-open, not-already-escalated candidates, select up to ' + batchSize + ', ordered by severity ' + severityOrder.join(' > ') + ' (highest first), preferring findings that cluster on a shared spec subsection or test file so one implementation session can close several efficiently.',
  '5. For each selected finding, extract targetTier from its Suggested-test field (the tier it names, e.g. "tier4", "tier2", "tier9"; if more than one tier is named, use the lowest-numbered one as targetTier and mention the rest in gap).',
  '6. Count and report remainingOpenInScope: how many OPEN findings remain in scope after removing resolvedInPassing and the selected batch (this excludes already-escalated needs-human findings, which are done for the purposes of this loop even though their checkbox stays open) — report separately as alreadyEscalatedInScope how many OPEN findings in scope already carry a Needs human input field and were skipped for that reason.',
  '',
  'Do not implement anything yet. Do not touch findings outside scope.',
  '',
  'RETURN the schema fields: branch (the exact branch name from step 1), selected (array as specified), resolvedInPassing (array of ids), remainingOpenInScope (integer, excluding already-escalated findings per step 6), alreadyEscalatedInScope (integer).',
].join('\n')

const selectResult = await agent(selectPrompt, { label: 'select', phase: 'Select', schema: SELECT_SCHEMA, effort: 'medium' })
log('Selected ' + selectResult.selected.length + ' findings on branch ' + selectResult.branch +
  ' (resolved ' + selectResult.resolvedInPassing.length + ' in passing, ' + selectResult.remainingOpenInScope + ' closable remain, ' +
  selectResult.alreadyEscalatedInScope + ' already escalated to needs-human and excluded).')

// ---------------------------------------------------------------------------
// Close: sequential per-finding loop (findings share the working tree and a
// common tests/spec-map.json / tests/change-graph.json, so — same rationale
// as implement-proposal-build's Build phase — they are gated and committed
// one at a time rather than run in parallel/pipeline).
//
// DECISION TREE per finding (see implementPrompt for the full instructions):
//   route="test"      a spec-derived test now exists and is green. Either it
//                      passed against the unmodified code (gap really was
//                      just missing coverage), or the first draft of the
//                      test was itself wrong and got corrected to match the
//                      verbatim spec, with the code left untouched.
//   route="code-fix"  the spec-derived test failed against current code, the
//                      test itself was re-confirmed correct, the defect's
//                      blast radius was fully enumerated, the fix is
//                      unambiguously spec-aligned, and a FULL regression pass
//                      (not just the finding's own tier) came back green.
//   route="moot"      the gap no longer exists; cite the covering evidence.
//   route="needs-human"  a real gap was confirmed but spec-alignment of any
//                      fix is not 100% clear (silent/contradictory spec,
//                      uncertain blast radius, a foundational surface with
//                      unclear wider effect). NO product code is touched.
//                      The finding stays OPEN, annotated in place with the
//                      specific question for a human to answer.
// ---------------------------------------------------------------------------
phase('Close')

const IMPLEMENT_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['id', 'route', 'attempts'],
  properties: {
    id: { type: 'string' },
    route: { type: 'string', enum: ['test', 'code-fix', 'moot', 'needs-human'] },
    attempts: { type: 'integer' },
    testFirstFailed: { type: 'boolean' },
    commitSha: { type: 'string' },
    tiersRun: { type: 'array', items: { type: 'string' } },
    fullRegressionCommand: { type: 'string' },
    specCitation: { type: 'string' },
    blastRadiusNotes: { type: 'string' },
    resolutionNote: { type: 'string' },
    humanQuestion: { type: 'string' },
    notes: { type: 'string' },
  },
}

const VERIFY_FINDING_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['approved', 'issues'],
  properties: {
    approved: { type: 'boolean' },
    issues: { type: 'array', items: { type: 'string' } },
    forceRoute: { type: 'string', enum: ['needs-human'] },
  },
}

function implementPrompt(f, priorIssues) {
  return [
    'ROLE. You are closing ONE test-coverage gap from TEST-GAPS.md. Follow .claude/rules/code-best-practices.md, .claude/rules/test-coverage.md, and .claude/rules/spec-driven-development.md.',
    '',
    'FINDING ' + f.id + ' — ' + f.title + ' [' + f.severity + '] (section §' + f.section + ')',
    '- Spec/Doc: ' + f.specDoc,
    '- Gap: ' + f.gap,
    '- Dependencies: ' + f.dependencies,
    '- Suggested test: ' + f.suggestedTest,
    '- Target tier: ' + f.targetTier,
    priorIssues && priorIssues.length ? ('\nA prior attempt on this same finding was rejected for:\n- ' + priorIssues.join('\n- ') + '\nFix these specifically. If the rejection reason itself shows this is not a clean, unambiguous fix, route="needs-human" instead of trying again.') : '',
    '',
    'DECISION TREE. Work through this in order; do not skip straight to writing a code fix.',
    '',
    'STEP 0 — record `git rev-parse HEAD` before touching anything, as this finding\'s startSha. STEP 5a\'s full regression pass is scoped against it (`lenny-test run --since <startSha>`, not `--changed`, which only diffs the uncommitted working tree and sees nothing once earlier findings in this batch are already committed).',
    '',
    'STEP 1 — write the test from the spec, not the finding\'s paraphrase. Re-read the exact spec section this finding cites (it may have drifted since the finding was written) and quote the precise sentence(s) you are deriving the assertion from as specCitation. Write the test at the target tier (or the lowest tier that can genuinely exercise the behavior), with a `// spec: §X.Y` citation and, for tier2+, a `// diagnosis:` comment. Reuse existing tests/testinfra building blocks; do not hand-roll ad hoc infra when a helper already exists.',
    '',
    'STEP 2 — run it against the CURRENT, unmodified product code, before changing anything else.',
    '  - If it passes immediately: route="test". Register it in tests/spec-map.json (and tests/change-graph.json if it introduces a new package/schema/migration/chart-template mapping), commit, done.',
    '  - If it fails: set testFirstFailed=true and go to STEP 3. Do NOT touch product code yet.',
    '',
    'STEP 3 — second-guess the TEST before concluding the product is wrong. Re-read the exact spec sentence again, character by character. Ask: does my assertion match what the spec actually requires, not a stricter or looser reading, not a detail misremembered from a different section, not this section confused with a neighboring one? Common mistakes: asserting an exact value the spec leaves as a MAY/SHOULD range, asserting an ordering the spec does not actually mandate, or testing an internal implementation detail instead of the observable contract.',
    '  - If the test itself was wrong: fix the TEST only (not the code), re-run against the still-unmodified code. If it now passes, route="test" with resolutionNote explaining the original mis-derivation and what you corrected. Do not let this become a way to quietly water down a real bug into a passing test — the corrected assertion must still be a faithful, if-anything-stricter reading of the spec; if you are not genuinely confident the original failure was a test bug rather than a product bug, treat it as STEP 4 instead.',
    '  - If the test is confirmed correct and the failure is real: go to STEP 4.',
    '',
    'STEP 4 — this may be a real product defect. Before writing any code fix:',
    '  a. Search the codebase for every call site, test, and doc that depends on the CURRENT (spec-violating) behavior. List each file:line you find as blastRadiusNotes.',
    '  b. Re-read the surrounding spec subsection (not just the cited line) for a conflicting requirement, and grep `proposals/` for any prior decision that may have intentionally chosen the current behavior for a reason not obvious from this one line.',
    '  c. Confidence checkpoint — is it unambiguous that (i) the spec requires the new behavior, (ii) no other spec section or approved proposal contradicts it, and (iii) you have fully enumerated the blast radius? Answer honestly; a plausible-sounding fix is not the same as an unambiguous one.',
    '',
    'STEP 5a — route="code-fix" (only when STEP 4c is a clean yes on all three). Apply the minimal, targeted fix; update every call site/test the blast-radius search found that assumed the old behavior; cite `// spec: §X.Y` on the fix itself, not just the test. Then run a FULL regression pass across the WHOLE reached surface, not just this finding\'s own tier or package: `lenny-test run --since <startSha from STEP 0>` (check the dry-run plan first with `--dry-run` if unsure which tiers that resolves to), so every tier the change graph reaches for the touched packages runs — not `lenny-test --changed`, which diffs the uncommitted working tree and sees nothing once your own change is committed. Record the exact command as fullRegressionCommand. If the change touches a shared or foundational package, also run tier1+tier2 for its known reverse-dependents. This must be green before you report route="code-fix"; if it is not, keep fixing within this same attempt or, if you lose confidence the fix is clean, fall back to STEP 5b instead of forcing it through. A gating failure that is unrelated to this finding\'s own diff (check by running the same failing test against startSha) is not this finding\'s to fix — note it in notes and do not let it block route="code-fix", but do not silently ignore it either.',
    '',
    'STEP 5b — route="needs-human" (when STEP 4c is not a clean yes, or a code-fix attempt could not get the full regression pass green without further changes you are not confident about). Do NOT modify product code — if you made exploratory edits while investigating, revert them (`git checkout -- <files>` or `git reset --hard` scoped to just this finding\'s changes, being careful not to discard other commits already on this branch). Write the exact question a human needs to answer (spec ambiguity, contested blast radius, uncertain wider effect) as humanQuestion; the batch driver records it directly on this finding in TEST-GAPS.md afterward (there is no separate issue tracker for this skill). Keep the spec-derived test from STEP 1 if you can mark it non-blocking (`t.Skip("<one-line reason tied to the still-open TEST-GAPS.md finding, not an id>")`) rather than discard the spec-faithful assertion — and if you keep it, COMMIT it (with any new schema/testdata fixtures it needs) before returning; a kept-but-uncommitted test is invisible to review and easy to lose, and no other step in this workflow will commit it for you. Register it in tests/spec-map.json/tests/change-graph.json like any other new test so `lenny-test validate-maps` stays clean. Otherwise, if you discard the test, note in notes why. Do NOT edit TEST-GAPS.md yourself for this route; the finding stays OPEN and is annotated by a later single-writer step.',
    '',
    'route="moot": re-verifying (independent of the above) shows the gap no longer exists — already covered by since-landed work you can point to. Cite the covering test/commit as resolutionNote.',
    '',
    'GENERAL. Do not defer any route for "missing infrastructure" — Kind, envtest, and the compose stack (Postgres/Redis/MinIO) are available locally (`lenny-test infra up --profile compose|kind`); that is never a reason to pick needs-human. Commit message form for test/code-fix/moot: "test-gaps: close <one-line description of the behavior now covered/fixed>" (describe the behavior; do not embed the TEST-GAPS finding id or any other internal tracking id in the message). Do NOT put a TEST-GAPS finding id, or any other id that is not a durable spec-section reference, into test code, test names, or comments anywhere — cite only `// spec: §X.Y (...)` or describe the behavior in prose, the same rule `implement-proposal` applies to proposal-internal labels. Do NOT edit TEST-GAPS.md yourself for any route; the batch driver marks or annotates it after an independent check.',
    '',
    'RETURN: id="' + f.id + '", route, attempts (how many internal fix-and-recheck cycles you needed), testFirstFailed (boolean), commitSha (if you committed), tiersRun (array actually executed), fullRegressionCommand (the exact command, required for route=code-fix), specCitation (the verbatim quote backing the test and, for code-fix, the fix), blastRadiusNotes (required for route=code-fix and route=needs-human), resolutionNote (for test/code-fix/moot, one or two sentences for the TEST-GAPS.md Resolution field, written as if the reader has no other context), humanQuestion (required for route=needs-human), notes.',
  ].filter(Boolean).join('\n')
}

function verifyFindingPrompt(f, implResult) {
  return [
    'ROLE. You are an independent verifier. Do not trust the implementer\'s self-report; re-derive everything material.',
    '',
    'FINDING ' + f.id + ' — ' + f.title + ' (target tier ' + f.targetTier + ').',
    'Implementer reported: route=' + implResult.route + ', commit=' + (implResult.commitSha || 'none') + ', tiersRun=' + JSON.stringify(implResult.tiersRun || []) +
      ', testFirstFailed=' + !!implResult.testFirstFailed + '.',
    'specCitation: ' + (implResult.specCitation || '(none given)'),
    'blastRadiusNotes: ' + (implResult.blastRadiusNotes || '(none given)'),
    '',
    'CHECK, by route.',
    '',
    'route="test": `git show --stat ' + (implResult.commitSha || 'HEAD') + '` and read the diff. Re-run the tier(s) yourself and confirm green. Independently re-read the spec section yourself (do not just accept the quoted specCitation) and confirm the test\'s assertion genuinely matches what the spec requires — no stricter, no looser. If testFirstFailed was true, this check matters most: verify the implementer did not quietly water down the assertion to match whatever the code currently does instead of what the spec actually requires — that would be a real bug hiding behind a passing test. Confirm the test would FAIL against the pre-fix code (not a vacuous or happy-path-only assertion when the finding named a failure, adversarial, or boundary path). Confirm the `// spec:` (and `// diagnosis:` for tier2+) annotations and the tests/spec-map.json update.',
    '',
    'route="code-fix": all of the "test" checks above, PLUS: independently re-read the spec text yourself and confirm the fix is unambiguously required, not merely plausible. Spot-check the blast-radius claim yourself — grep for the old behavior\'s signature elsewhere in the codebase; if you find a call site or test the implementer\'s blastRadiusNotes missed, REJECT. Independently re-run the fullRegressionCommand yourself (do not accept the implementer\'s report of it) and confirm it is actually green. If you yourself are not fully convinced the fix is unambiguously spec-aligned, REJECT with forceRoute="needs-human" in your issues rather than approving a shaky fix — this is a deliberate escape hatch: an autonomous batch must never land a product-behavior change it cannot independently justify against the spec text.',
    '',
    'route="moot": confirm the cited covering test/commit genuinely covers the gap.',
    '',
    'route="needs-human": confirm no product code was modified by this attempt (git diff against the pre-attempt commit for files outside test/tests/spec-map/change-graph paths should be empty) — if product code WAS changed under this route, REJECT, that is a hard invariant violation. Confirm humanQuestion is a specific, answerable question (not a vague "investigate this"). Confirm TEST-GAPS.md was not edited for this finding.',
    '',
    'ALL ROUTES. Check for leaked internal identifiers: test code, test names, and comments must not reference this batch\'s scratch labels, the TEST-GAPS finding id, or any other id that is not a durable spec-section reference — only a `// spec: §X.Y` citation or a prose description of the behavior belongs there. Commit messages must not embed the TEST-GAPS finding id either; they describe the behavior. REJECT if you find one.',
    '',
    'RETURN approved (boolean), issues (array of strings; empty if approved), and forceRoute="needs-human" when you are rejecting a code-fix specifically because you are not convinced of its spec alignment (as opposed to a fixable procedural gap like a missing annotation).',
  ].join('\n')
}

const closedResults = []
let abortedOnTerminalError = false
for (const f of selectResult.selected) {
  let attempt = 0
  let implResult = null
  let verifyResult = { approved: false, issues: [] }
  let priorIssues = []
  let forcedHuman = false
  while (attempt < maxAttemptsPerFinding) {
    attempt++
    implResult = await agent(implementPrompt(f, priorIssues), {
      label: f.id + ':impl:' + attempt, phase: 'Close', schema: IMPLEMENT_SCHEMA, effort: 'high',
    })
    if (!implResult) {
      // agent() returns null on a terminal API error (auth expiry, rate/usage
      // limit) after retries, not just for this finding but almost certainly
      // for every subsequent agent() call in this run too. Stop cleanly here
      // rather than dereference a null implResult (which crashed the whole
      // workflow the first two times this happened) — closedResults keeps
      // whatever this batch already finished, so Mark-resolved/Annotate/
      // Verify below still run on that partial progress instead of losing
      // it. The unresolved finding itself is left OPEN, untouched, for the
      // next batch (Select will just pick it up again next time).
      verifyResult = { approved: false, issues: ['implementer agent returned no result (terminal API error); not retried within this run'] }
      abortedOnTerminalError = true
      break
    }
    verifyResult = await agent(verifyFindingPrompt(f, implResult), {
      label: f.id + ':verify:' + attempt, phase: 'Close', schema: VERIFY_FINDING_SCHEMA, effort: 'medium',
    })
    if (!verifyResult) {
      verifyResult = { approved: false, issues: ['verifier agent returned no result (terminal API error); not retried within this run'] }
      abortedOnTerminalError = true
      break
    }
    if (implResult.route === 'moot' || implResult.route === 'needs-human') {
      // Single-pass sanity check only; nothing to iterate on (moot needs no
      // fix, needs-human deliberately touches no code to iterate against).
      break
    }
    if (verifyResult.approved) break
    if (verifyResult.forceRoute === 'needs-human') {
      forcedHuman = true
      log(f.id + ' attempt ' + attempt + ': verifier rejected the code-fix as not unambiguously spec-aligned; forcing needs-human instead of retrying the fix.')
      break
    }
    priorIssues = verifyResult.issues
    log(f.id + ' attempt ' + attempt + ' rejected: ' + verifyResult.issues.join('; '))
  }
  if (forcedHuman && implResult && implResult.route === 'code-fix') {
    // The verifier vetoed an implementer-claimed fix it could not independently
    // justify; require an explicit needs-human handoff (revert the fix) rather
    // than leaving an unverified fix on the branch. No external issue tracker
    // is used here; the question is recorded directly on the still-open
    // TEST-GAPS.md finding by the Annotate step below.
    const revertPrompt = [
      'ROLE. An independent verifier rejected your code-fix for finding ' + f.id + ' as not unambiguously spec-aligned: ' + (priorIssues.length ? priorIssues.join('; ') : verifyResult.issues.join('; ')) + '.',
      '',
      'Revert the product-code changes you made for this finding on this branch (keep any other findings\' already-committed work intact — scope the revert to just this finding\'s commit(s), e.g. `git revert <commitSha>` or a scoped `git checkout <parent-sha> -- <files>` plus commit). Write the exact open question a human needs to answer (cite the verifier\'s rejection reason and your own blast-radius findings) as humanQuestion; it will be recorded directly on this finding in TEST-GAPS.md afterward. Keep the spec-derived test if it can be marked non-blocking (`t.Skip("<one-line reason>")`, with no id embedded), otherwise discard it and say so.',
      '',
      'RETURN humanQuestion, commitSha (the revert commit).',
    ].join('\n')
    const revertResult = await agent(revertPrompt, {
      label: f.id + ':revert-to-human', phase: 'Close', effort: 'medium',
      schema: { type: 'object', additionalProperties: false, required: ['humanQuestion'], properties: { humanQuestion: { type: 'string' }, commitSha: { type: 'string' } } },
    })
    implResult = { id: f.id, route: 'needs-human', attempts: attempt, humanQuestion: revertResult.humanQuestion, notes: 'code-fix reverted after verifier veto' }
    verifyResult = { approved: true, issues: [] }
  }
  closedResults.push({ finding: f, impl: implResult, verify: verifyResult, attempts: attempt })
  log(f.id + ' -> route=' + (implResult ? implResult.route : 'none') + ' approved=' + verifyResult.approved + ' after ' + attempt + ' attempt(s).')
  if (abortedOnTerminalError) {
    // Don't keep looping over the rest of the batch's findings — a terminal
    // API error almost certainly recurs on every further agent() call in
    // this run, so trying the next finding would just fail the same way.
    // Stop the Close loop here; Mark-resolved/Annotate/Verify below still
    // process whatever finished before this point.
    log('stopping the Close loop early after a terminal API error; ' + (selectResult.selected.length - closedResults.length) + ' selected finding(s) left untouched this run.')
    break
  }
}

// ---------------------------------------------------------------------------
// Mark resolved: one agent, sequential edits to TEST-GAPS.md (safe, no
// concurrent writers since Close already finished). Only test/code-fix/moot
// close a finding; needs-human and anything unapproved stay OPEN.
// ---------------------------------------------------------------------------
const toResolve = closedResults.filter(function (r) {
  return r.verify.approved && r.impl && (r.impl.route === 'test' || r.impl.route === 'code-fix' || r.impl.route === 'moot')
})
const needsHuman = closedResults.filter(function (r) { return r.impl && r.impl.route === 'needs-human' })
// abortedFindings: impl is null because the implementer or verifier agent
// hit a terminal API error (rate/usage limit, auth expiry) and never
// produced a real result — not a genuine quality disagreement. These stay
// completely untouched (no annotation, no exclusion from future Select) so
// the very next batch just retries them normally once the underlying
// constraint clears.
const abortedFindings = closedResults.filter(function (r) { return !r.impl && !r.verify.approved })
// stuck: a genuine quality rejection — the implementer produced a real
// result but the independent verifier rejected every attempt within
// maxAttemptsPerFinding. This is what should be annotated and excluded.
const stuck = closedResults.filter(function (r) { return !r.verify.approved && r.impl && r.impl.route !== 'needs-human' })

let markResult = { markedCount: 0 }
if (toResolve.length) {
  const markPrompt = [
    'ROLE. Mark the following TEST-GAPS.md findings RESOLVED. This batch\'s implementation and independent verification already passed; you are only updating the checklist.',
    '',
    'FINDINGS TO RESOLVE:',
    JSON.stringify(toResolve.map(function (r) {
      return { id: r.finding.id, commitSha: r.impl.commitSha, resolutionNote: r.impl.resolutionNote, route: r.impl.route, specCitation: r.impl.specCitation }
    })),
    '',
    'For each, locate its heading (`grep -n \'^### - \\[ \\] <id> \'` — use the literal id) in TEST-GAPS.md, flip `- [ ]` to `- [x]` and `OPEN` to `RESOLVED <commitSha>`, and replace the five original fields (Spec/Doc, Existing tests, Gap, Dependencies, Suggested test) with a single `- **Resolution:** <resolutionNote>` field, mirroring the existing worked example at T-10.1.11 (grep it in TEST-GAPS.md for the exact tone). For route="moot" findings, phrase the Resolution as "already covered by <what>, found during batch review" rather than implying new work landed. For route="code-fix" findings, the Resolution must state both what the test now pins and what product-code defect was corrected, citing the spec section that made the fix unambiguous.',
    '',
    'Commit the TEST-GAPS.md edit by itself: `git add TEST-GAPS.md && git commit -m "test-gaps: mark <ids> resolved"`.',
    '',
    'RETURN markedCount (integer) and commitSha (the TEST-GAPS.md commit).',
  ].join('\n')
  markResult = await agent(markPrompt, {
    label: 'mark-resolved', phase: 'Close', effort: 'medium',
    schema: { type: 'object', additionalProperties: false, required: ['markedCount'], properties: { markedCount: { type: 'integer' }, commitSha: { type: 'string' } } },
  })
}

// ---------------------------------------------------------------------------
// Annotate needs-human AND stuck findings: one agent, sequential edits,
// records why each is still open directly on its still-OPEN finding. No
// external issue tracker is used by this skill/workflow. A stuck finding
// (the implementer and verifier went through maxAttemptsPerFinding rounds
// without landing on an approved test/code-fix/moot) is functionally the
// same as needs-human from a human's perspective — something needs a
// decision before another batch retries it — even though the implementer
// never explicitly declared route="needs-human"; both get the same
// **Needs human input:** field and both are excluded from future Select
// rounds by it (see the Select step above).
// ---------------------------------------------------------------------------
const toAnnotate = needsHuman.map(function (r) { return { id: r.finding.id, humanQuestion: r.impl.humanQuestion } })
  .concat(stuck.map(function (r) {
    return {
      id: r.finding.id,
      humanQuestion: 'This finding was attempted ' + r.attempts + ' time(s) and the independent verifier rejected every attempt: ' +
        (r.verify && r.verify.issues && r.verify.issues.length ? r.verify.issues.join(' ') : 'no specific issues were recorded.') +
        ' A human should review the attempt history before a future batch retries this finding, since repeating the same approach is likely to hit the same rejection.',
    }
  }))
let annotateResult = { annotatedCount: 0 }
if (toAnnotate.length) {
  const annotatePrompt = [
    'ROLE. Annotate the following TEST-GAPS.md findings with an open question or blocker note for a human. Do NOT flip their checkbox or OPEN marker; they stay exactly as open findings, just with one more field.',
    '',
    'FINDINGS TO ANNOTATE:',
    JSON.stringify(toAnnotate),
    '',
    'For each, locate its heading (`grep -n \'^### - \\[ \\] <id> \'`) in TEST-GAPS.md and append one new field after the existing Suggested test field, before the next heading: `- **Needs human input (as of ' + '<today\'s date via `date +%F`>' + '):** <humanQuestion>`. Do not alter any of the finding\'s other fields.',
    '',
    'Commit the TEST-GAPS.md edit by itself: `git add TEST-GAPS.md && git commit -m "test-gaps: record open questions for human review"`.',
    '',
    'RETURN annotatedCount (integer) and commitSha.',
  ].join('\n')
  annotateResult = await agent(annotatePrompt, {
    label: 'annotate-needs-human', phase: 'Close', effort: 'medium',
    schema: { type: 'object', additionalProperties: false, required: ['annotatedCount'], properties: { annotatedCount: { type: 'integer' }, commitSha: { type: 'string' } } },
  })
  if (!annotateResult) {
    // agent() returned null on a terminal API error: the needs-human
    // question(s) this batch produced were NEVER WRITTEN to TEST-GAPS.md
    // (observed directly — a batch's own summary claimed a finding was
    // annotated when the write had in fact failed silently here). Flag it
    // explicitly rather than let a bare `null` in the final report hide a
    // real gap a human must close by hand.
    annotateResult = { annotatedCount: 0, abortedOnTerminalError: true, pendingQuestions: toAnnotate }
    abortedOnTerminalError = true
  }
}

// ---------------------------------------------------------------------------
// Verify: batch-level.
// ---------------------------------------------------------------------------
// The full `lenny-test run --since <base>` regression pass reliably takes
// longer than one agent turn has patience for (observed truncated/forced
// across three straight batches, whether foregrounded, backgrounded, or
// nudged) — an in-workflow agent either forces a false result before the
// run finishes or the run gets orphaned by turn-boundary cleanup. This
// phase therefore only runs the FAST, reliably-completing checks
// (validate-maps, coverage --diff, and a dry-run of what the full pass
// would cover) and fixes small drift; the full regression pass itself is
// the orchestrating session's job afterward (documented in the
// close-test-gaps SKILL.md procedure), run with run_in_background and
// awaited via its own completion notification rather than inside an
// agent's turn.
phase('Verify')
const codeFixCount = closedResults.filter(function (r) { return r.impl && r.impl.route === 'code-fix' }).length
const verifyBatchPrompt = [
  'ROLE. Fast batch-level checks for the test-gaps closure batch just implemented on the current branch. This batch includes ' + codeFixCount + ' product-code fix(es) (route=code-fix). Do NOT attempt the full `lenny-test run` regression suite here — it does not reliably complete within one turn; the orchestrating session runs it separately afterward. Stick to the checks below, which complete quickly.',
  '',
  '1. Find the commit this branch was cut from: `baseSha=$(git merge-base HEAD <the branch it was cut from, typically the branch this run started on>)`. Run `lenny-test run --since "$baseSha" --dry-run` (dry-run only, do not run it for real) and report which tiers it resolves to, so a human knows what the follow-up full pass needs to cover.',
  '2. Run `lenny-test coverage --diff "$baseSha"` and report the changed-line coverage percentage against the 80% floor.',
  '3. Run `lenny-test validate-maps` and fix any spec-map/change-graph drift this batch introduced (small fixes only, e.g. registering a new test file under the right spec-map section — when editing tests/spec-map.json by script rather than by hand, preserve its existing formatting, for example Python\'s json.dump needs ensure_ascii=False or every non-ASCII spec character gets escaped and the diff balloons; commit the fix separately from anything else).',
  '4. Do NOT merge this branch into anything and do NOT push. This batch is reviewed by a human before merge, per this repo\'s git workflow rules.',
  '',
  'RETURN: dryRunTiers (array of tier names the full pass needs to cover), coveragePct (number or null if not computed), validateMapsClean (boolean), notes (string).',
].join('\n')
verifyResult = await agent(verifyBatchPrompt, {
  label: 'batch-verify', phase: 'Verify', effort: 'medium',
  schema: {
    type: 'object', additionalProperties: false,
    required: ['dryRunTiers', 'validateMapsClean'],
    properties: { dryRunTiers: { type: 'array', items: { type: 'string' } }, coveragePct: { type: ['number', 'null'] }, validateMapsClean: { type: 'boolean' }, notes: { type: 'string' } },
  },
})
if (!verifyResult) {
  // Same terminal-error shape as the Annotate step above: the fast checks
  // (validate-maps, coverage --diff, dry-run tiers) never ran for this
  // batch. Flag it explicitly rather than report a bare `null`.
  verifyResult = { dryRunTiers: [], validateMapsClean: false, abortedOnTerminalError: true, notes: 'batch-verify agent returned no result (terminal API error); fast checks not run this batch — run them manually.' }
  abortedOnTerminalError = true
}

return {
  branch: selectResult.branch,
  scope: scope,
  resolvedInPassing: selectResult.resolvedInPassing,
  remainingOpenInScope: selectResult.remainingOpenInScope,
  abortedOnTerminalError: abortedOnTerminalError,
  batch: {
    selectedCount: selectResult.selected.length,
    processedCount: closedResults.length,
    resolvedCount: toResolve.length,
    codeFixCount: codeFixCount,
    needsHumanCount: needsHuman.length,
    stuckCount: stuck.length,
    abortedCount: abortedFindings.length,
    resolved: toResolve.map(function (r) { return { id: r.finding.id, route: r.impl.route } }),
    needsHuman: needsHuman.map(function (r) { return { id: r.finding.id, question: r.impl.humanQuestion } }),
    stuck: stuck.map(function (r) { return { id: r.finding.id, issues: r.verify.issues } }),
    aborted: abortedFindings.map(function (r) { return r.finding.id }),
  },
  markResult: markResult,
  annotateResult: annotateResult,
  verify: verifyResult,
}
