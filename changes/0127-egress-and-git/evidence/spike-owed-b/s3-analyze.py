#!/usr/bin/env python3
import sys,glob,re,os,statistics as st
d=sys.argv[1]
rows={}
for sf in sorted(glob.glob(d+"/*.stamp")):
    p=os.path.basename(sf)[:-6]
    s=open(sf).read()
    m=re.search(r"start=(\d+) end=(\d+) code=(\d+)",s)
    if not m: print(p,"bad stamp",s[:200]); continue
    t0,t1,code=int(m[1]),int(m[2]),int(m[3])
    openT=None; regress=0; dropped_before=False; errs=[]; bl=[]
    for l in open(f"{d}/{p}.log"):
        f=l.split()
        if len(f)<2: continue
        if f[1]=="STATE":
            ts=int(f[0])
            if f[2]=="dropped" and ts<t0: dropped_before=True
            if f[2]=="open" and openT is None: openT=ts
            if f[2].startswith("err"): errs.append(l.strip())
        elif f[1]=="REGRESS": regress+=1
        elif f[1]=="BLOCKING_DIAL":
            m2=re.search(r"started=(\d+) ok_after=([\d.]+)",l)
            if m2: bl.append((int(m2[1]),float(m2[2])))
    rows[p]=dict(code=code,after_ret=None if openT is None else (openT-t1)/1e9,after_start=None if openT is None else (openT-t0)/1e9,call=(t1-t0)/1e9,dropped_before=dropped_before,regress=regress,errs=errs,
      bl=[(round((s-t0)/1e9,2),a) for s,a in bl if s<t1])
def grp(name,keys):
    v=[rows[k] for k in keys if k in rows]
    a=[r["after_ret"] for r in v if r["after_ret"] is not None]
    c=[r["call"] for r in v]
    miss=[k for k in keys if k in rows and rows[k]["after_ret"] is None]
    print(f"{name}: n={len(v)} never_opened={miss} dropped_before_all={all(r['dropped_before'] for r in v)} regress={sum(r['regress'] for r in v)} codes={sorted(set(r['code'] for r in v))}")
    if a: print(f"   open after call RETURN s: min={min(a):.3f} median={st.median(a):.3f} max={max(a):.3f}; values={sorted(round(x,3) for x in a)}")
    b=[r["after_start"] for r in v if r["after_start"] is not None]
    if b: print(f"   open after call START  s: min={min(b):.3f} median={st.median(b):.3f} max={max(b):.3f}; call duration max={max(c):.3f}")
    blk=[x for r in v for x in r["bl"]]
    print("   blocking dials begun before the stamp returned: (start relative to stamp, connect took s):", sorted(blk)[:12])
    for k in keys:
        if k in rows and rows[k]["errs"]: print("   ",k,rows[k]["errs"][:2])
grp("sequential, other node",[f"x{i}" for i in range(10)])
grp("sequential, same node",[f"s{i}" for i in range(10)])
grp("burst, other node",[f"b{i}" for i in range(0,12,2)])
grp("burst, same node",[f"b{i}" for i in range(1,12,2)])
