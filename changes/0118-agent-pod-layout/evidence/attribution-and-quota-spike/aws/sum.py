#!/usr/bin/env python3
# sum.py LABEL: compact view of one t1 run
import json, sys, os
L = sys.argv[1]; R = f"/data/results/t1-{L}"
def f(x): return f"p50 {x['p50_us']:.1f} p99 {x['p99_us']:.1f} max {x['max_us']:.1f} us"
for n in ["basic", "many", "pass", "close", "rst", "many6"]:
    p = f"{R}/{n}.json"
    if not os.path.exists(p): print(n, "missing"); continue
    for k, v in sorted(json.load(open(p)).items()):
        print(f"{n:6} {k:22} N={v['N']:4} diag={v['DiagUIDs']}{v['DiagErrs'] or ''} mm={v['DiagMismatch']} [{f(v['DiagLat'])}] | proc={v['ProcUIDs']}{v['ProcErrs'] or ''} mm={v['ProcMismatch']} [{f(v['ProcLat'])}] rows={v['ProcRowsMax']}")
for n in ["bench", "bench6"]:
    p = f"{R}/{n}.json"
    if os.path.exists(p): print(n, open(p).read().strip())
