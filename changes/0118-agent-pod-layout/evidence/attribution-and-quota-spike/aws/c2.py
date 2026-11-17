#!/usr/bin/env python3
# c2.py FILE: compact a t2 log (probe lines reduced to the fields that matter)
import json, sys
M = 1048576
for ln in open(sys.argv[1]):
    ln = ln.rstrip("\n")
    if ln.startswith("{"):
        try: d = json.loads(ln)
        except Exception: print(ln[:300]); continue
        if "statfs" in d:
            s = d["statfs"]; m = d.get("mount", {})
            print(f"  probe uid={d.get('uid')} {d['path']}: statfs size={s.get('size_b',0)/M:.1f}M used={s.get('used_b',0)/M:.1f}M type={s.get('type')} {s.get('err','')} | fstype={m.get('fstype')} super_prjquota={'prjquota' in m.get('super','')} opts={m.get('opts')} | projid={d.get('projid')} inherit={d.get('projinherit')} getx_err={d.get('fsgetxattr_err','')} | qstatv={d.get('qstatv') or d.get('qstatv_err')} xget={d.get('xgetquota') or d.get('xgetquota_err')}")
        else:
            print("  " + json.dumps(d)[:600])
    else:
        print(ln[:300])
