// Parse-check a workflow script the way the runtime does: the body is wrapped in
// an async function, so a top-level `return` is legal and `export const meta` is
// stripped first. `node --check` treats the file as a module and misses errors
// that only surface in that wrapping, which shipped a broken script once.
import { readFileSync } from "fs";
let bad = 0;
for (const f of process.argv.slice(2)) {
  const src = readFileSync(f, "utf8").replace(/^export\s+const\s+meta/m, "const meta");
  try {
    new Function("args", "agent", "parallel", "pipeline", "phase", "log", "workflow", "budget",
      "return (async () => {\n" + src + "\n})();");
    console.log("PARSE OK   " + f);
    // The sandbox's globals are agent, parallel, pipeline, phase, log, workflow,
    // budget, and args. `require` is not among them, so a `require("fs")` call
    // throws at runtime; wrapped in a try/catch, as file access here always was,
    // it degrades silently and disables whatever depended on it. File work
    // belongs in an agent, which has Bash and Read.
    // An identifier referenced but never defined. new Function() parses fine and
    // throws only when that line executes, which in a workflow can be after
    // hours of agent work — this shipped three times before the guard existed.
    // Strings and comments are removed by a character scanner rather than a
    // regex: these files concatenate very long strings containing escaped
    // quotes, and a regex mis-pairs them and swallows real declarations.
    const strip = (t) => {
      let out = "", i = 0;
      while (i < t.length) {
        const c = t[i], d = t[i + 1];
        if (c === "/" && d === "/") { while (i < t.length && t[i] !== "\n") i++; continue; }
        if (c === "/" && d === "*") { i += 2; while (i < t.length && !(t[i] === "*" && t[i + 1] === "/")) i++; i += 2; continue; }
        if (c === '"' || c === "'" || c === "`") {
          const q = c; i++;
          while (i < t.length && t[i] !== q) { if (t[i] === "\\") i++; i++; }
          i++; out += '""'; continue;
        }
        out += c; i++;
      }
      return out;
    };
    const code = strip(src);
    const declared = new Set([
      ...code.matchAll(/\b(?:const|let|var|function|class)\s+([A-Za-z_$][\w$]*)/g),
    ].map((m) => m[1]));
    const GLOBALS = new Set(["JSON","Math","Object","Array","String","Number","Boolean","Set","Map","Date",
      "Promise","Error","RegExp","console","args","agent","parallel","pipeline","phase","log","workflow",
      "budget","setTimeout","clearTimeout","Infinity","NaN","undefined","globalThis","meta"]);
    for (const m of code.matchAll(/\b([A-Z][A-Z0-9_]{2,})\b/g)) {
      const id = m[1];
      if (declared.has(id) || GLOBALS.has(id)) continue;
      console.log("UNDEFINED " + f + " :: " + id + " is referenced but never declared");
      bad++;
      break;
    }

    const uses = src.split("\n").map((l, i) => [i + 1, l])
      .filter(([, l]) => /(^|[^.\w])require\s*\(/.test(l) && !/^\s*(\/\/|\*)/.test(l));
    for (const [n, l] of uses) {
      console.log("SANDBOX FAIL " + f + ":" + n + " :: require() is not defined in the workflow sandbox — " + l.trim());
      bad++;
    }
  } catch (e) {
    console.log("PARSE FAIL " + f + " :: " + e.message.split("\n")[0]);
    bad++;
  }
}
process.exit(bad ? 1 : 0);
