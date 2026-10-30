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

import { createHash } from "crypto";
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
  // An exact match is resolved the same way a glob match is, below: a function
  // stub is CALLED. Returning it uncalled handed the workflow a function object,
  // which is truthy, so a test that stubbed an exact label with a function
  // silently asserted nothing and passed.
  if (Object.prototype.hasOwnProperty.call(stubs, label)) {
    const exact = stubs[label];
    return typeof exact === "function" ? exact(call) : exact;
  }

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

// ---- resume replay -------------------------------------------------------
//
// A model of the runtime's resume journal, for testing that a resumed run
// replays every agent call the interrupted run completed.
//
// The runtime keys each agent() call on a hash chained over the calls invoked
// before it: key = sha256(previous key, prompt, the options that reach the
// model). That was read from the runtime and checked against a real journal,
// where the sampled keys of a measured run recomputed from their predecessors'. A
// resumed run returns a call's journaled result only while every call before
// it hit the journal; the first miss runs live and so does everything after
// it. A call whose agent returned nothing is journaled as failed and never
// replays.
//
// So a resume replays the prefix only when the ORDER in which the script
// invokes its agents is the same in both runs. Order is the part a plain stub
// harness cannot see: its agents all return in the same microtask, so a script
// whose invocation order depends on agent latency looks deterministic there.
// This model gives live agents a latency and replayed ones none, as the
// runtime does, and parallel() yields a macrotask before it invokes its
// thunks, as the runtime's does. A newer runtime chains each parallel() slot
// separately; a single chain is the stricter of the two, because an order that
// is fixed globally is fixed within every slot.

const KEYED_OPTS = ["schema", "model", "effort", "isolation", "agentType", "disallowedTools", "bashCommandClamp"];

function canonical(v) {
  if (typeof v === "function") return undefined;
  if (Array.isArray(v)) return v.map(canonical);
  if (v && typeof v === "object") {
    const out = {};
    for (const k of Object.keys(v).sort()) out[k] = canonical(v[k]);
    return out;
  }
  return v;
}

/** The options that key an agent call, serialised as the runtime does. */
export function keyedOpts(opts) {
  const picked = {};
  for (const k of KEYED_OPTS) {
    if (opts && opts[k] !== undefined && typeof opts[k] !== "function") picked[k] = opts[k];
  }
  return JSON.stringify(canonical(picked));
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/**
 * Run a workflow body against a modelled resume journal.
 *
 * @param {string} relPath  workflow path relative to the repository root
 * @param {object} args     the `args` global the script receives
 * @param {object|Function} stubs  as for runWorkflow
 * @param {object} [opts]
 *   journal   the `journal` a previous run returned; omitted for a fresh run
 *   latency   (call) => milliseconds a LIVE agent takes; replayed ones take none
 *   stopAt    (call) => true at the first call the account limit refuses; that
 *             call and every later live call return null, as a usage limit does
 *   inline    run a workflow() child's real body with these same hooks, as the
 *             runtime does, rather than returning a stubbed result
 *   source    workflow source text, in place of reading relPath
 * @returns {Promise<{result, calls, logs, error, journal, firstMiss, stopIndex}>}
 *   Each call carries its key, its keyed options, and whether it replayed.
 */
export async function runJournaled(relPath, args, stubs, opts = {}) {
  const journal = { results: new Map(opts.journal ? opts.journal.results : []) };
  const latency = opts.latency || (() => 1);
  const stopAt = opts.stopAt || (() => false);
  const calls = [];
  const logs = [];
  let prev = "";
  let missed = false;
  let stopIndex = -1;
  let firstMiss = -1;

  const agent = async (prompt, agentOpts = {}) => {
    const p = String(prompt);
    const ko = keyedOpts(agentOpts);
    const key = "v2:" + createHash("sha256").update(prev).update("\0").update(p).update("\0").update(ko).digest("hex");
    prev = key;
    const call = { label: agentOpts.label || "", prompt: p, opts: agentOpts, keyedOpts: ko, key, index: calls.length, replayed: false };
    calls.push(call);
    if (!missed && journal.results.has(key)) {
      call.replayed = true;
      await null;
      return JSON.parse(journal.results.get(key));
    }
    if (!missed) {
      missed = true;
      firstMiss = call.index;
    }
    if (stopIndex === -1 && stopAt(call)) stopIndex = call.index;
    // The stub is resolved when the call is invoked, as runWorkflow does, so a
    // stub that counts its calls counts them in invocation order.
    const out = stopIndex !== -1 ? null : resolveStub(stubs, call);
    await sleep(latency(call));
    if (out === null) return null;
    const value = out === undefined ? {} : out;
    // Journaled as the runtime journals it: serialised, and replayed as a copy.
    const text = JSON.stringify(value);
    journal.results.set(key, text);
    return JSON.parse(text);
  };

  // The runtime's parallel() yields once, then invokes every thunk in slot
  // order inside one synchronous pass; a thunk that throws resolves to null.
  const parallel = async (thunks) => {
    await sleep(0);
    return Promise.all(
      thunks.map((f) => {
        try {
          return Promise.resolve(f()).catch(() => null);
        } catch {
          return Promise.resolve(null);
        }
      }),
    );
  };
  const pipeline = async (items, ...stages) => {
    await sleep(0);
    return Promise.all(
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
  };

  const budget = { total: null, spent: () => 0, remaining: () => Infinity };
  const body = (src) =>
    new Function(
      "args", "agent", "parallel", "pipeline", "phase", "log", "workflow", "budget",
      "return (async () => {\n" + src + "\n})();",
    );
  const workflow = async (ref, subArgs) => {
    const key = typeof ref === "string" ? ref : ref && ref.scriptPath;
    if (opts.inline && typeof key === "string" && key.endsWith(".js")) {
      const child = loadWorkflow(".claude/workflows/" + key.replace(/^.*\//, ""));
      return body(child)(subArgs, agent, parallel, pipeline, () => {}, (m) => logs.push(String(m)), workflow, budget);
    }
    const table = opts.subworkflows || {};
    for (const k of Object.keys(table)) {
      if (String(key).includes(k)) return table[k];
    }
    return {};
  };

  const src = opts.source !== undefined ? opts.source : loadWorkflow(relPath);
  let result = null;
  let error = null;
  try {
    result = await body(src)(args, agent, parallel, pipeline, () => {}, (m) => logs.push(String(m)), workflow, budget);
  } catch (e) {
    error = e;
  }
  return { result, calls, logs, error, journal, firstMiss, stopIndex };
}

/**
 * Compare an interrupted run with its resume. Every call the interrupted run
 * invoked before its stop must replay, with the same prompt and options in the
 * same position.
 */
export function replayDivergence(interrupted, resumed) {
  const upTo = interrupted.stopIndex === -1 ? interrupted.calls.length : interrupted.stopIndex;
  for (let i = 0; i < upTo; i++) {
    const a = interrupted.calls[i];
    const b = resumed.calls[i];
    if (!b) return { index: i, label: a.label, why: "the resume made fewer calls" };
    if (a.prompt !== b.prompt) return { index: i, label: a.label, other: b.label, why: "prompt differs" };
    if (a.keyedOpts !== b.keyedOpts) return { index: i, label: a.label, other: b.label, why: "options differ" };
    if (!b.replayed) return { index: i, label: a.label, why: "ran live" };
  }
  return null;
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
  let finished = false;
  console.log("\n### " + name);
  // A file that THROWS mid-way never reaches done(), so its summary line never
  // prints and every check after the throw silently never ran. The runner still
  // reports the file as failed, which is honest, but the count it shows is the
  // count before the crash and reads as a small failure rather than a whole-file
  // abort. A mutation audit hit exactly that and had to notice by hand.
  process.on("exit", () => {
    if (finished) return;
    console.log(
      "\n" + name + ": ABORTED after " + checks + " check(s) (" + failures +
        " failed before the abort); the rest of the file never ran.",
    );
  });
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
      finished = true;
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
