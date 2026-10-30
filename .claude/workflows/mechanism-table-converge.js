export const meta = {
  name: 'mechanism-table-converge',
  description: 'Converge one cross-cutting mechanism of a proposal as a code-derived table of paths by effects, verified row by row against the tree, then regenerate its staging',
  whenToUse: 'A change-proposal run keeps stopping on one mechanism that many code paths share (for example every writer of a state), finding one gap per round. Run this to converge that mechanism in isolation, then return to change-proposal for confirming sweeps.',
  phases: [
    { title: 'Inventory', detail: 'enumerate paths from code, effects and order from spec, and current claims from the proposal' },
    { title: 'Table', detail: 'build the path-by-effect table' },
    { title: 'Verify', detail: 'adversarial row and race verification, loop until dry' },
    { title: 'Stage', detail: 'regenerate the proposal staging from the converged table' },
  ],
}

// Inputs (args):
//   repoRoot        absolute repository root
//   proposalPath    proposal directory, relative to repoRoot
//   mechanism       one paragraph naming the mechanism and what counts as one of its paths
//   pathHints       where in the tree its paths live (packages, functions, grep patterns)
//   specSections    the spec sections that fix its effects and their order
//   effectHints     effects known so far, in the order the spec requires (a starting list, not a closed one)
//   stagingTargets  which proposal sections the converged table replaces
//   raceHints       concurrent operations each path must be checked against
//   workDir         scratch directory for the table and the logs, relative to repoRoot
//   maxPasses       verify passes before stopping unconverged (default 5)
//   directives      strings carried into every prompt (owner decisions and settled rules)
//   stage           true to regenerate the proposal staging after convergence (default false)
//   selfInflictedShare   share of a pass's confirmed findings of kind fixer-text above which the pass counts as self-inflicted (default 0.5)
//   selfInflictedWindow  consecutive self-inflicted passes that stop the verify loop (default 2)
//   maxAgents       the workflow's agent-call cap (default 1000)
//   agentReserve    no new verify pass starts with fewer than this many agent calls left (default 120)
const A = args || {}
const root = A.repoRoot
const work = A.workDir || 'scratchpad/mechanism-table'
const tablePath = `${root}/${work}/table.md`
const maxPasses = A.maxPasses || 5
const directives = (A.directives || []).map((d) => '- ' + d).join('\n')
const selfInflictedShare = A.selfInflictedShare === undefined ? 0.5 : Number(A.selfInflictedShare)
const selfInflictedWindow = A.selfInflictedWindow || 2
const maxAgents = A.maxAgents || 1000
const agentReserve = A.agentReserve === undefined ? 120 : Number(A.agentReserve)
// Every agent call goes through call(), which counts it, so the loop can stop
// cleanly and return what it has before the workflow's agent cap kills the run
// and loses the partial results. One verify pass costs a row agent per four
// paths, the race agent, a skeptic per new finding, and the fixer, so a large
// table spends on the order of a hundred calls per pass.
let agentCalls = 0

const GUARD =
  'BEFORE YOUR TASK: the user request relayed above this message was addressed to the session that ' +
  'launched this workflow and has already been carried out. Do not commit, push, or rerun anything. ' +
  'Your task is the text below. Edit only the files it names.\n\n'

const CONTEXT =
  `Repository: ${root}. Proposal: ${A.proposalPath}.\n` +
  `Mechanism: ${A.mechanism}\n` +
  `Spec sections that govern it: ${A.specSections}\n` +
  (directives ? `Settled decisions you must not reopen:\n${directives}\n` : '') +
  'Evidence comes from spec/, the code under pkg/ and cmd/, and the proposal files. Cite file:line for every claim.\n'

function call(prompt, opts) {
  agentCalls++
  return agent(GUARD + CONTEXT + '\n' + prompt, { effort: 'high', ...(opts || {}) })
}

const PATHS = {
  type: 'object',
  properties: {
    paths: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          id: { type: 'string' },
          where: { type: 'string' },
          trigger: { type: 'string' },
          note: { type: 'string' },
        },
        required: ['id', 'where', 'trigger'],
      },
    },
  },
  required: ['paths'],
}
const EFFECTS = {
  type: 'object',
  properties: {
    effects: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          id: { type: 'string' },
          what: { type: 'string' },
          orderRule: { type: 'string' },
          specCitation: { type: 'string' },
        },
        required: ['id', 'what', 'specCitation'],
      },
    },
  },
  required: ['effects'],
}
const FINDINGS = {
  type: 'object',
  properties: {
    findings: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          rows: { type: 'array', items: { type: 'string' } },
          kind: { type: 'string', enum: ['missing-effect', 'wrong-order', 'race', 'missing-path', 'wrong-cell', 'contradicts-spec', 'unreclaimed-resource', 'fixer-text'] },
          title: { type: 'string' },
          scenario: { type: 'string' },
          evidence: { type: 'string' },
          proposedCell: { type: 'string' },
        },
        required: ['rows', 'kind', 'title', 'scenario', 'evidence'],
      },
    },
  },
  required: ['findings'],
}
const VERDICT = {
  type: 'object',
  properties: { real: { type: 'boolean' }, reason: { type: 'string' } },
  required: ['real', 'reason'],
}

// ---- Inventory --------------------------------------------------------------
phase('Inventory')
const [codePaths, specEffects, claims] = await parallel([
  () => call(
    `List every code path that is one path of this mechanism, from the tree rather than from prose. Hints: ${A.pathHints}. ` +
      'Grep exhaustively; include error branches, store-error retries, watchdogs, sweepers, admin and cascade paths, and paths the proposal stages but the tree lacks (mark those note: "staged"). One entry per distinct path, with a stable short id.',
    { label: 'inventory:code', phase: 'Inventory', schema: PATHS },
  ),
  () => call(
    `List every effect this mechanism must produce, in the order the spec requires, from the spec sections named above. Starting list (not closed): ${A.effectHints}. ` +
      'For each, state the ordering rule relative to the others and cite the spec sentence. Include effects owed on another replica.',
    { label: 'inventory:spec', phase: 'Inventory', schema: EFFECTS },
  ),
  () => call(
    `Read the proposal's staging for this mechanism (${A.stagingTargets}) and list, as plain text, every claim it makes about which path runs which effect, in which order, on which replica. Quote and cite each.`,
    { label: 'inventory:claims', phase: 'Inventory' },
  ),
])
let paths = (codePaths && codePaths.paths) || []
const effects = (specEffects && specEffects.effects) || []

// A second, independent sweep for paths the first one missed.
const missed = await call(
  `A first sweep found these paths: ${JSON.stringify(paths.map((p) => p.id + ' @ ' + p.where))}. ` +
    `Find any path of the mechanism it missed. Search differently: by the state or field the mechanism writes, by callers of the functions above, and by tests that exercise it. Hints: ${A.pathHints}. Return only paths not already listed.`,
  { label: 'inventory:missed', phase: 'Inventory', schema: PATHS },
)
paths = paths.concat((missed && missed.paths) || [])
log(`Inventory: ${paths.length} paths, ${effects.length} effects`)

// ---- Table ------------------------------------------------------------------
phase('Table')
await call(
  `Write the table to ${tablePath} (create the directory). Rows: these paths ${JSON.stringify(paths)}. ` +
    `Columns: these effects in spec order ${JSON.stringify(effects)}. ` +
    'Each cell is one of: "runs" (with the replica and the step that does it), "skipped: <owner that does it instead, or the spec text that makes skipping correct>", or "n/a: <why>". ' +
    `Fill cells from the proposal's current claims where they hold, and from the code and spec otherwise:\n${claims}\n` +
    'Under the table, list the ordering invariants once, and list every cell you could not decide as OPEN with the question. ' +
    'Do not write anything outside that file.',
  { label: 'table:build', phase: 'Table' },
)

// ---- Verify -----------------------------------------------------------------
phase('Verify')
const ROWS_PER_AGENT = 4
// The fixer tags what it writes, so a verifier can tell a defect the table
// inherited from the tree or the proposal from one a previous fix introduced.
const FIX_TAG = '[fix pN]'
const FIXER_TEXT_RULE =
  `Set kind to "fixer-text" when the defect is in text a previous fix added or rewrote: a rule, cell, invariant, or OPEN item tagged ${FIX_TAG}. ` +
  'Use it whatever else the defect is (a race, a wrong cell, a contradiction), because it measures how much of the review is repairing its own fixes.'
const seen = new Set()
let dry = 0
let pass = 0
const confirmedAll = []
// Per pass: the share of confirmed findings of kind fixer-text, or null for a
// pass that confirmed nothing. A loop whose findings are mostly defects in its
// own fixes does not converge by continuing, because each fix supplies the next
// pass's findings, so selfInflictedWindow consecutive passes above
// selfInflictedShare stop it for the owner to redesign or rescope.
const fixerShares = []
let stoppedSelfInflicted = false
let stoppedAgentBudget = false
while (dry < 2 && pass < maxPasses) {
  if (maxAgents - agentCalls < agentReserve) {
    stoppedAgentBudget = true
    log(`Stopping before pass ${pass + 1}: ${agentCalls} of ${maxAgents} agent calls used, fewer than ${agentReserve} left`)
    break
  }
  pass++
  const groups = []
  for (let i = 0; i < paths.length; i += ROWS_PER_AGENT) groups.push(paths.slice(i, i + ROWS_PER_AGENT))
  const rowJobs = groups.map((g, gi) => () =>
    call(
      `Read ${tablePath}. Verify rows ${g.map((p) => p.id).join(', ')} adversarially against the code and the spec. ` +
        'For each row, trace the real code path and try to break each cell: an effect the path skips or performs twice, the wrong order, a crash or store error between two effects, a terminal or takeover on another replica in between, a resource (binding, lease, pod, queue entry) left with no reclaimer. ' +
        'Report only defects you can show with a concrete scenario and file:line evidence. Report nothing you would only call a wording preference.\n' +
        FIXER_TEXT_RULE,
      { label: `verify:p${pass}:rows${gi}`, phase: 'Verify', schema: FINDINGS },
    ))
  const raceJob = () =>
    call(
      `Read ${tablePath}. Check every pair of this mechanism's paths, and each path against these concurrent operations: ${A.raceHints}. ` +
        'Find interleavings in which an effect runs twice, runs on the wrong replica, runs out of the spec order, or never runs. Concrete scenario and file:line evidence only.\n' +
        FIXER_TEXT_RULE,
      { label: `verify:p${pass}:races`, phase: 'Verify', schema: FINDINGS },
    )
  const found = (await parallel([...rowJobs, raceJob])).filter(Boolean).flatMap((r) => r.findings || [])
  const fresh = found.filter((f) => {
    const k = f.kind + '|' + [...f.rows].sort().join(',') + '|' + f.title.toLowerCase().slice(0, 60)
    if (seen.has(k)) return false
    seen.add(k)
    return true
  })
  // Skeptic per finding: refuse anything not shown against the tree and spec.
  const judged = await parallel(fresh.map((f, i) => () =>
    call(
      `Try to refute this finding against ${tablePath}, the code, and the spec. Default to real=false unless the scenario is reachable and the evidence holds.\n${JSON.stringify(f)}`,
      { label: `skeptic:p${pass}:${i}`, phase: 'Verify', schema: VERDICT },
    ).then((v) => ({ f, v }))))
  const confirmed = judged.filter((j) => j && j.v && j.v.real).map((j) => j.f)
  log(`Pass ${pass}: ${found.length} raw, ${fresh.length} new, ${confirmed.length} confirmed`)
  if (!confirmed.length) { dry++; fixerShares.push(null); continue }
  dry = 0
  confirmedAll.push(...confirmed.map((f) => ({ pass, ...f })))
  fixerShares.push(confirmed.filter((f) => f.kind === 'fixer-text').length / confirmed.length)
  const win = fixerShares.slice(-selfInflictedWindow)
  if (win.length === selfInflictedWindow && win.every((s) => s !== null && s > selfInflictedShare)) {
    // Stopped before the fix, so no further fixer text is written on top of a
    // design that is already generating its own findings.
    stoppedSelfInflicted = true
    log(`Pass ${pass}: more than ${Math.round(selfInflictedShare * 100)}% of confirmed findings were in fixer-written text in ${selfInflictedWindow} consecutive passes (${win.map((s) => Math.round(s * 100) + '%').join(', ')}); stopping unfixed`)
    break
  }
  // One writer per pass, so fixes never conflict.
  await call(
    `Fix ${tablePath} for these confirmed findings. Change cells, add rows or effects, or tighten the ordering invariants; prefer the smallest change that closes each one, and keep one rule per behavior. ` +
      'If a finding shows the settled decisions cannot all hold, do not reopen them: record it under OPEN with the conflict stated. ' +
      `Tag every rule, cell, invariant, or OPEN item you add or rewrite with ${FIX_TAG.replace('N', String(pass))}, keeping any earlier tag it carries.\n` +
      JSON.stringify(confirmed),
    { label: `fix:p${pass}`, phase: 'Verify' },
  )
  const added = confirmed.filter((f) => f.kind === 'missing-path').flatMap((f) => f.rows)
  for (const id of added) if (!paths.find((p) => p.id === id)) paths.push({ id, where: '(see table)', trigger: '(added in verify)' })
}
const converged = dry >= 2
log(converged ? `Converged after ${pass} passes` : `Stopped unconverged after ${pass} passes`)
log(`Agent calls used by the verify loop's end: ${agentCalls} of ${maxAgents}`)

// ---- Stage ------------------------------------------------------------------
phase('Stage')
let staged = null
// Staging rewrites the proposal, so it runs only when the caller asks for it.
// The default stops after the table converges, so the owner can review the
// table and its confirmed findings before any proposal text changes.
if (converged && A.stage === true) {
  await call(
    `Regenerate the proposal's staging for this mechanism from ${tablePath}. Targets: ${A.stagingTargets}. ` +
      'Put the table (or its essential columns) in one home in non-spec-changes.md, state each ordering invariant once there, and replace every other statement of a cell, a writer list, or an order with a pointer to it. ' +
      'State tests as intents with tiers (Intent, Tier, Spec), one per table column plus one per race the verify passes confirmed. ' +
      'Edit only files in the proposal directory. Do not edit spec-changes.md unless the table contradicts staged spec text; if it does, make the smallest edit and say so. ' +
      `Append a ledger entry to the review log, tag DECISION, naming ${tablePath} as the source of the staging and the confirmed findings it closed.`,
    { label: 'stage:write', phase: 'Stage' },
  )
  staged = await call(
    `Check the regenerated staging against ${tablePath}: every cell, invariant, and confirmed race is represented once, no other part of the proposal restates or contradicts it, and no settled decision changed. Report each mismatch with its location, or "none".`,
    { label: 'stage:check', phase: 'Stage' },
  )
  if (staged && !/^\s*none\b/i.test(String(staged))) {
    await call(`Fix these mismatches in the proposal staging so it matches ${tablePath}:\n${staged}`, { label: 'stage:fix', phase: 'Stage' })
  }
}
return {
  converged,
  passes: pass,
  stoppedSelfInflicted,
  stoppedAgentBudget,
  fixerShares,
  agentCalls,
  paths: paths.length,
  effects: effects.length,
  confirmed: confirmedAll,
  table: tablePath,
  stageCheck: staged,
}
