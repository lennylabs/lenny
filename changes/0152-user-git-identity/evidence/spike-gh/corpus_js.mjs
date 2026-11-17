import { parse } from '/js/node_modules/graphql/index.mjs';
import { readFileSync } from 'fs';
const c = JSON.parse(readFileSync('/o/run/corpus.json','utf8')); const out = {};
const top = (ss, frs, seen) => ss.selections.flatMap(s => s.kind==='Field' ? [s.name.value] : s.kind==='InlineFragment' ? top(s.selectionSet, frs, seen) : (frs[s.name.value] && !seen.has(s.name.value) ? (seen.add(s.name.value), top(frs[s.name.value].selectionSet, frs, seen)) : []));
for (const [id, doc] of Object.entries(c)) {
  try { const d = parse(doc); const frs = {}; for (const n of d.definitions) if (n.kind==='FragmentDefinition') frs[n.name.value]=n;
    const ops = d.definitions.filter(n=>n.kind==='OperationDefinition').map(o=>({type:o.operation,name:o.name?o.name.value:'',fields:top(o.selectionSet,frs,new Set())}));
    const other = d.definitions.filter(n=>n.kind!=='OperationDefinition'&&n.kind!=='FragmentDefinition').map(n=>n.kind);
    out[id] = other.length ? {ops, other} : {ops};
  } catch (e) { out[id] = {err: String(e.message).split('\n')[0]}; }
}
console.log(JSON.stringify(out));
