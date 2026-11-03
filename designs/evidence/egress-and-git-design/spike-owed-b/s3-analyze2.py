#!/usr/bin/env python3
import sys,glob,re,os,statistics as st
d=sys.argv[1]
def one(p):
    s=open(f"{d}/{p}.stamp").read(); m=re.search(r"start=(\d+) end=(\d+) code=(\d+)",s)
    t0,t1,code=int(m[1]),int(m[2]),int(m[3]); op=None; reg=0; dropped=False; blk=[]
    for l in open(f"{d}/{p}.log"):
        f=l.split()
        if len(f)<2: continue
        if f[1]=="STATE":
            if f[2]=="dropped" and int(f[0])<t0: dropped=True
            if f[2]=="open" and op is None: op=int(f[0])
        if f[1]=="REGRESS": reg+=1
        if f[1]=="BLOCKING_DIAL":
            x=re.search(r"started=(\d+) ok_after=([\d.]+)",l)
            if x and op: blk.append((round((int(x[1])-t1)/1e9,2),float(x[2]),round((int(f[0])-op)/1e9,2)))
    return dict(code=code,dropped=dropped,reg=reg,lat=None if op is None else (op-t1)/1e9,last=max(0,0),blk=blk)
for name,pfx in (("other node","x"),("same node","s")):
    r=[(p,one(p)) for p in sorted((os.path.basename(f)[:-6] for f in glob.glob(f"{d}/{pfx}*.stamp")),key=lambda x:int(x[1:]))]
    a=[x["lat"] for _,x in r if x["lat"] is not None]
    print(f"{name}: n={len(r)} never_opened_in_window={[p for p,x in r if x['lat'] is None]} dropped_before_all={all(x['dropped'] for _,x in r)} regress={sum(x['reg'] for _,x in r)} codes={set(x['code'] for _,x in r)}")
    if a: print(f"   open after call return, s: min={min(a):.2f} median={st.median(a):.2f} max={max(a):.2f}; in stamp order: {[None if x['lat'] is None else round(x['lat'],2) for _,x in r]}")
    b=[k for _,x in r for k in x["blk"] if k[0]<0 and k[2]>=0]
    if b: print("   sample blocking dials started before the stamp: first success lags the path opening by, s:", sorted(set(k[2] for k in b))[-6:])
