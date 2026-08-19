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
