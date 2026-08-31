// Runner for the workflow test suite.
//
// These tests exercise the agent workflow drivers under .claude/workflows/.
// They are deliberately NOT part of the product suite and nothing outside
// .claude/ references them: no Makefile target, no lenny-test tier, no CI job,
// and nothing in tests/change-graph.json reaches them. A red workflow test
// never blocks a product build, and a red product build never hides a workflow
// regression. See §11.0 of .claude/proposal-pipeline-rework-plan.md.
//
// Run:  node .claude/tests/run.mjs           everything
//       node .claude/tests/run.mjs --lint    layer 1 only
//
// Each *.test.mjs is spawned as its own process so it stays independently
// runnable with `node .claude/tests/<file>` and so one file's crash cannot take
// the rest of the suite with it.

import { readdirSync } from "fs";
import { spawnSync } from "child_process";
import { fileURLToPath } from "url";
import { dirname, resolve, basename } from "path";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO = resolve(HERE, "..", "..");
const argv = process.argv.slice(2);
const LINT_ONLY = argv.includes("--lint");
const STRICT = argv.includes("--strict");

const run = (label, cmd, args) => {
  const r = spawnSync(cmd, args, { cwd: REPO, stdio: "inherit" });
  const ok = r.status === 0;
  if (!ok) console.log("\n>>> " + label + " FAILED (exit " + r.status + ")");
  return ok;
};

let ok = true;

ok = run("lint", "node", [resolve(HERE, "lint-workflows.mjs"), ...(STRICT ? ["--strict"] : [])]) && ok;

if (!LINT_ONLY) {
  const tests = readdirSync(HERE)
    .filter((f) => f.endsWith(".test.mjs"))
    .sort();
  for (const t of tests) {
    ok = run(basename(t), "node", [resolve(HERE, t)]) && ok;
  }
  const shell = readdirSync(HERE)
    .filter((f) => f.endsWith(".test.sh"))
    .sort();
  for (const t of shell) {
    ok = run(basename(t), "bash", [resolve(HERE, t)]) && ok;
  }
}

console.log(ok ? "\n=== workflow tests: all green ===" : "\n=== workflow tests: FAILURES ===");
process.exit(ok ? 0 : 1);
