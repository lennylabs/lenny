// Shared harness for the workflow behavioural tests (layer 2 of the test plan
// in .claude/proposal-pipeline-rework-plan.md).
//
// The point of these tests is that the REAL workflow body runs. Only the
// boundary to the model is replaced: `agent` is stubbed and every call it makes
// is recorded, so every ordering, gating, fallback and short-circuit decision
// under test is made by the script itself rather than by the test.
//
// The runtime wraps a workflow body in an async function with the sandbox
// globals injected, and `export const meta` is stripped first. That wrapping is
// reproduced here exactly, because a top-level `return` is legal inside it and
// illegal outside, and because errors that only surface under it are the class
// the lint exists to catch.
//
// The sandbox surface is not a guess. A probe workflow returning `typeof` for
// every escape route established it as exactly:
//   log, phase, console, budget, setTimeout, clearTimeout, Date, agent,
//   parallel, pipeline, workflow, args
// with require, process, fetch, Buffer and module all undefined, and the
// Function-constructor route closed at the V8 level ("Code generation from
// strings disallowed for this context").

import { readFileSync } from "fs";
import { fileURLToPath } from "url";
import { dirname, resolve } from "path";

/** Absolute repository root, so a test runs the same from any cwd. */
export const REPO = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");

/** Read a workflow and strip the meta export the runtime strips. */
export function loadWorkflow(relPath) {
  return readFileSync(resolve(REPO, relPath), "utf8").replace(
    /^export\s+const\s+meta/m,
    "const meta",
  );
}

// Resolve one agent call against the stub table.
//
// Keys are matched exactly first, then by wildcard, then by longest prefix,
// then `default`. The wildcard form exists because most labels in these
// workflows carry a round or a step in the middle -- `r3:review:security`,
// `build:S2:fix4` -- so a plain prefix cannot name "every review lens". A key
// containing `*` is a glob over the whole label, and the longest literal part
// of a matching glob wins, so `*:review:security` beats `*:review:`.
//
// A value may be a plain result or a function of the call, so a stub can vary
// its answer by round. Returning `null` simulates an agent that died.
function resolveStub(stubs, call) {
  if (typeof stubs === "function") return stubs(call);
  const { label } = call;
  if (Object.prototype.hasOwnProperty.call(stubs, label)) return stubs[label];

  let best = null;
  let bestScore = -1;
  for (const key of Object.keys(stubs)) {
    if (key === "default") continue;
    let score = -1;
    if (key.includes("*")) {
      const re = new RegExp(
        "^" + key.split("*").map((p) => p.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join(".*") + "$",
      );
      if (re.test(label)) score = key.replace(/\*/g, "").length;
    } else if (label.startsWith(key)) {
      score = key.length;
    }
    if (score > bestScore) {
      bestScore = score;
      best = key;
    }
  }
  const chosen = best === null ? stubs.default : stubs[best];
  return typeof chosen === "function" ? chosen(call) : chosen;
}

/**
 * Run a workflow body with `agent` stubbed.
 *
 * @param {string} relPath  workflow path relative to the repository root
 * @param {object} args     the `args` global the script receives
 * @param {object|Function} stubs  label → result, label-prefix → result,
 *                                 `default`, or one function of the call
 * @param {object} [opts]   { subworkflows: { name|path → result } }
 * @returns {Promise<{result, calls, logs, phases, error}>}
 */
export async function runWorkflow(relPath, args, stubs, opts = {}) {
  const src = loadWorkflow(relPath);
  const calls = [];
  const logs = [];
  const phases = [];

  const agent = async (prompt, agentOpts = {}) => {
    const call = {
      label: agentOpts.label || "",
      prompt: String(prompt),
      opts: agentOpts,
      index: calls.length,
    };
    calls.push(call);
    const out = resolveStub(stubs, call);
    return out === undefined ? {} : out;
  };

  // The real parallel resolves a throwing thunk to null rather than rejecting,
  // and several guards in these workflows depend on that exact behaviour.
  const parallel = async (thunks) =>
    Promise.all(
      thunks.map((f) =>
        Promise.resolve()
          .then(() => f())
          .catch(() => null),
      ),
    );

  // Each item through every stage independently; a stage that throws drops that
  // item to null and skips its remaining stages.
  const pipeline = async (items, ...stages) =>
    Promise.all(
      items.map(async (item, i) => {
        let acc = item;
        for (const stage of stages) {
          if (acc === null) return null;
          try {
            acc = await stage(acc, item, i);
          } catch {
            return null;
          }
        }
        return acc;
      }),
    );

  const subworkflow = async (ref, subArgs) => {
    const key = typeof ref === "string" ? ref : ref && ref.scriptPath;
    calls.push({ label: "workflow:" + key, prompt: JSON.stringify(subArgs || {}), opts: {}, index: calls.length });
    const table = opts.subworkflows || {};
    for (const k of Object.keys(table)) {
      if (String(key).includes(k)) return table[k];
    }
    return {};
  };

  const fn = new Function(
    "args",
    "agent",
    "parallel",
    "pipeline",
    "phase",
    "log",
    "workflow",
    "budget",
    "return (async () => {\n" + src + "\n})();",
  );

  let result = null;
  let error = null;
  try {
    result = await fn(
      args,
      agent,
      parallel,
      pipeline,
      (t) => phases.push(String(t)),
      (m) => logs.push(String(m)),
      subworkflow,
      { total: null, spent: () => 0, remaining: () => Infinity },
    );
  } catch (e) {
    error = e;
  }
  return { result, calls, logs, phases, error };
}

// ---- assertions ----------------------------------------------------------

/** Labels of every recorded call, in order. */
export const labels = (calls) => calls.map((c) => c.label);

/** Every call whose label starts with `prefix`. */
export const matching = (calls, prefix) => calls.filter((c) => c.label.startsWith(prefix));

/** Index of the first call whose label starts with `prefix`, or -1. */
export const firstIndex = (calls, prefix) =>
  calls.findIndex((c) => c.label.startsWith(prefix));

/** True when no call's label starts with `prefix`. */
export const never = (calls, prefix) => firstIndex(calls, prefix) === -1;

/** True when every `a` call precedes every `b` call. */
export function ordered(calls, aPrefix, bPrefix) {
  const as = calls.map((c, i) => [c.label, i]).filter(([l]) => l.startsWith(aPrefix)).map(([, i]) => i);
  const bs = calls.map((c, i) => [c.label, i]).filter(([l]) => l.startsWith(bPrefix)).map(([, i]) => i);
  if (as.length === 0 || bs.length === 0) return false;
  return Math.max(...as) < Math.min(...bs);
}

/**
 * A test file's reporter. Keeps the ergonomics of the original scripts: each
 * file is runnable on its own with `node <file>` and exits non-zero on failure.
 */
export function suite(name) {
  let failures = 0;
  let checks = 0;
  console.log("\n### " + name);
  return {
    section(title) {
      console.log("\n" + title);
    },
    check(what, cond, detail) {
      checks++;
      if (cond) {
        console.log("  PASS  " + what);
      } else {
        failures++;
        console.log("  FAIL  " + what + (detail ? "  :: " + detail : ""));
      }
    },
    done() {
      console.log(
        "\n" +
          (failures === 0
            ? name + ": all " + checks + " check(s) passed."
            : name + ": " + failures + " of " + checks + " check(s) FAILED."),
      );
      process.exit(failures ? 1 : 0);
    },
  };
}
