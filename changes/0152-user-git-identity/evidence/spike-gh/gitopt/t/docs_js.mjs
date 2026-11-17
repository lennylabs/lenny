import { parse, print } from '/js/node_modules/graphql/index.mjs';
import { readFileSync } from 'fs';
const m = JSON.parse(readFileSync('/docs/printed.json','utf8'));
let same=0, diff=0, fail=0, ex=[], reordered=0;
for (const [orig, printed] of Object.entries(m)) {
  try { const norm=(d)=>d.definitions.map(n=>print(n)).sort().join("\n"); const a=norm(parse(orig)), b=norm(parse(printed)); if (print(parse(orig))!==print(parse(printed))) reordered++; if (a===b) same++; else { diff++; if (ex.length<3) ex.push([orig.slice(0,160),printed.slice(0,160)]); } }
  catch(e){ fail++; if (ex.length<3) ex.push(['parse failure',String(e.message).slice(0,160),printed.slice(0,160)]); }
}
console.log(JSON.stringify({documents:Object.keys(m).length,sameAST:same,different:diff,failures:fail,definitionsReordered:reordered,examples:ex}));
