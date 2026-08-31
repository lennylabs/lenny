// Layer 1 of the workflow test plan: static gates over the workflow scripts.
//
// Moved here from scripts/check-workflow-scripts.mjs, which tested .claude/
// drivers from the product script tree. The checks it already carried are kept
// verbatim in behaviour; the ones the rework plan adds are here too, gated
// behind --strict until the phase that makes them satisfiable lands.
//
// Run:  node .claude/tests/lint-workflows.mjs [--strict] [files...]
//       node .claude/tests/run.mjs --lint
//
// Default file set is .claude/workflows/*.js.

import { readFileSync, readdirSync } from "fs";
import { fileURLToPath } from "url";
import { dirname, resolve, basename } from "path";

const REPO = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");

const argv = process.argv.slice(2);
const STRICT = argv.includes("--strict");
const files = argv.filter((a) => !a.startsWith("--"));
const targets =
  files.length > 0
    ? files.map((f) => resolve(REPO, f))
    : readdirSync(resolve(REPO, ".claude/workflows"))
        .filter((f) => f.endsWith(".js"))
        .sort()
        .map((f) => resolve(REPO, ".claude/workflows", f));

let bad = 0;
const fail = (file, msg) => {
  console.log("  FAIL  " + basename(file) + " :: " + msg);
  bad++;
};
const softFail = (file, msg) => {
  if (STRICT) fail(file, msg);
  else console.log("  skip  " + basename(file) + " :: " + msg + "  (--strict)");
};

// Strings and comments are removed by a character scanner rather than a regex:
// these files concatenate very long strings containing escaped quotes, and a
// regex mis-pairs them and swallows real declarations.
function stripCode(t) {
  let out = "";
  let i = 0;
  // A regex literal is a token, not code, and its body must not be scanned for
  // identifiers: /YES/i would otherwise read as an undeclared constant. Telling
  // a regex from a division needs the preceding token, and the standard
  // heuristic is that a `/` following an operator or an opening bracket starts
  // one while a `/` following a value divides.
  const regexCanStart = () => {
    let j = out.length - 1;
    while (j >= 0 && /\s/.test(out[j])) j--;
    if (j < 0) return true;
    return "(,=:[!&|?{};+-*%~^".includes(out[j]);
  };
  while (i < t.length) {
    const c = t[i];
    const d = t[i + 1];
    if (c === "/" && d !== "/" && d !== "*" && regexCanStart()) {
      let j = i + 1;
      let inClass = false;
      let closed = false;
      while (j < t.length) {
        const ch = t[j];
        if (ch === "\\") {
          j += 2;
          continue;
        }
        if (ch === "\n") break;
        if (ch === "[") inClass = true;
        else if (ch === "]") inClass = false;
        else if (ch === "/" && !inClass) {
          closed = true;
          break;
        }
        j++;
      }
      if (closed) {
        i = j + 1;
        while (i < t.length && /[a-z]/.test(t[i])) i++; // flags
        out += "0";
        continue;
      }
    }
    if (c === "/" && d === "/") {
      while (i < t.length && t[i] !== "\n") i++;
      continue;
    }
    if (c === "/" && d === "*") {
      i += 2;
      while (i < t.length && !(t[i] === "*" && t[i + 1] === "/")) i++;
      i += 2;
      continue;
    }
    if (c === '"' || c === "'" || c === "`") {
      const q = c;
      i++;
      while (i < t.length && t[i] !== q) {
        if (t[i] === "\\") i++;
        i++;
      }
      i++;
      out += '""';
      continue;
    }
    out += c;
    i++;
  }
  return out;
}

// The sandbox globals, established empirically by a probe workflow, plus the
// JS built-ins a script may reach.
const SANDBOX = [
  "args",
  "agent",
  "parallel",
  "pipeline",
  "phase",
  "log",
  "workflow",
  "budget",
  "console",
  "setTimeout",
  "clearTimeout",
  "Date",
];
const BUILTINS = [
  "JSON", "Math", "Object", "Array", "String", "Number", "Boolean", "Set", "Map",
  "Promise", "Error", "RegExp", "Infinity", "NaN", "undefined", "globalThis", "meta",
];
// Absent from the sandbox: reaching for one is a runtime failure hours in.
const ABSENT = ["require", "process", "fetch", "Buffer", "module", "__dirname", "exports"];

console.log("### layer 1: workflow lint" + (STRICT ? " (strict)" : ""));

for (const file of targets) {
  const src = readFileSync(file, "utf8").replace(/^export\s+const\s+meta/m, "const meta");

  // 1. Parse the way the runtime does. `node --check` treats the file as a
  //    module and misses errors that only surface under the async wrapper.
  try {
    new Function(
      "args", "agent", "parallel", "pipeline", "phase", "log", "workflow", "budget",
      "return (async () => {\n" + src + "\n})();",
    );
  } catch (e) {
    fail(file, "parse: " + String(e.message).split("\n")[0]);
    continue;
  }

  const code = stripCode(src);
  const declared = new Set(
    [...code.matchAll(/\b(?:const|let|var|function|class)\s+([A-Za-z_$][\w$]*)/g)].map((m) => m[1]),
  );

  // 2. An UPPERCASE identifier referenced but never declared. new Function()
  //    parses fine and throws only when that line executes, which in a workflow
  //    can be after hours of agent work. This has shipped three times.
  const known = new Set([...SANDBOX, ...BUILTINS, ...declared]);
  for (const m of code.matchAll(/\b([A-Z][A-Z0-9_]{2,})\b/g)) {
    if (known.has(m[1])) continue;
    fail(file, m[1] + " is referenced but never declared");
    break;
  }

  // 3. A global the sandbox does not define. `require("fs")` inside a
  //    try/catch degraded silently and disabled three features at once.
  for (const name of ABSENT) {
    const re = new RegExp("(^|[^.\\w])" + name + "\\s*[(.\\[]");
    src.split("\n").forEach((line, n) => {
      if (/^\s*(\/\/|\*)/.test(line)) return;
      if (re.test(line)) fail(file, name + " is not defined in the workflow sandbox (line " + (n + 1) + ")");
    });
  }

  // 4. Determinism. Date.now/Math.random/new Date break resume and the runtime
  //    rejects the script before it runs, so catching it here is cheaper.
  src.split("\n").forEach((line, n) => {
    if (/^\s*(\/\/|\*)/.test(line)) return;
    if (/\bDate\.now\s*\(|\bMath\.random\s*\(|new\s+Date\s*\(\s*\)/.test(line)) {
      fail(file, "non-deterministic call breaks resume (line " + (n + 1) + ")");
    }
  });

  // ---- checks the rework plan adds; not satisfiable until their phase lands --

  // 5. Every agent label built in the script is distinct. A duplicate breaks
  //    both the runtime journal cache and the lens cache, invisibly: the second
  //    call returns the first call's result.
  // Only a label that is a COMPLETE literal can collide. A concatenated label
  // (`label: "review:" + step.id + ":" + tag`) carries a per-call suffix, and
  // matching its first fragment would report every such site as a duplicate.
  const labelLits = [...src.matchAll(/label:\s*"([^"]+)"\s*(?=[,}])/g)].map((m) => m[1]);
  const seen = new Set();
  const dupes = new Set();
  for (const l of labelLits) {
    if (seen.has(l)) dupes.add(l);
    seen.add(l);
  }
  if (dupes.size > 0) {
    softFail(file, "duplicate literal agent label(s): " + [...dupes].join(", "));
  }

  // 6. No prompt in the proposal pipeline hardcodes a proposals/ path; every
  //    one interpolates a value the resolver produced. This is what stops the
  //    folder migration from half-landing. Scoped to the pipeline: a one-off
  //    workflow written to apply a single named proposal pins that path on
  //    purpose, and there is nothing for a resolver to resolve.
  const PIPELINE = ["change-proposal.js", "implement-proposal.js", "implement-proposal-build.js"];
  if (PIPELINE.includes(basename(file))) {
    for (const m of src.matchAll(/"[^"]*proposals\/[0-9]{4}[^"]*"/g)) {
      softFail(file, "hardcoded proposal path in a prompt: " + m[0].slice(0, 60));
      break;
    }
  }

  // 7. Every input the script reads is classified in ARG_CLASS, which is what
  //    makes the resume decision table a lookup rather than a judgement.
  const inputs = new Set([...code.matchAll(/\binput\.([A-Za-z_$][\w$]*)/g)].map((m) => m[1]));
  if (inputs.size > 0) {
    const hasRegistry = /\bARG_CLASS\b/.test(code);
    if (!hasRegistry) {
      softFail(file, "reads " + inputs.size + " input(s) but declares no ARG_CLASS registry");
    } else {
      const registry = src.slice(src.indexOf("ARG_CLASS"));
      const missing = [...inputs].filter((i) => !new RegExp('["\']?' + i + '["\']?\\s*:').test(registry));
      if (missing.length > 0) softFail(file, "ARG_CLASS omits: " + missing.join(", "));
    }
  }

  if (!STRICT) console.log("  ok    " + basename(file));
  else console.log("  ok    " + basename(file) + " (strict)");
}

console.log(bad ? "\n" + bad + " lint failure(s)." : "\nlint clean.");
process.exit(bad ? 1 : 0);
