#!/usr/bin/env python3
import re,sys,collections
R=sys.argv[1]; log=open(R).read().splitlines()
marks=[(int(l.split()[1])," ".join(l.split()[2:])) for l in log if l.startswith("MARK ")]
d="out/s2"
fails=[]
for c in ("c1","c2","c3"):
    for l in open(f"{d}/{c}.log"):
        m=re.match(r"(\d+) FAIL (.*)",l)
        if m: fails.append((int(m[1]),c,m[2]))
fails.sort()
def phase(t):
    cur="before"
    for mt,mn in marks:
        if t>=mt: cur=mn
    return cur
agg=collections.OrderedDict()
for t,c,x in fails:
    k=phase(t)
    x2=re.sub(r"\d+\.\d+\.\d+\.\d+:\d+","ADDR",x); x2=x2[:70]
    a=agg.setdefault(k,{"n":0,"first":t,"last":t,"kinds":collections.Counter(),"by":collections.Counter()})
    a["n"]+=1;a["last"]=t;a["kinds"][x2]+=1;a["by"][c]+=1
for mt,mn in marks: print(f"{(mt-marks[0][0])/1e9:8.1f} s  {mn}")
print("--- failures by the step they fell in")
for k,a in agg.items():
    print(f"after [{k}]: {a['n']} failures from {(a['first']-marks[0][0])/1e9:.1f} s to {(a['last']-marks[0][0])/1e9:.1f} s by {dict(a['by'])}")
    for kk,v in a["kinds"].most_common(4): print("    ",v,kk)
print("total failures",len(fails))
ok=[]
for c in ("c1","c2","c3"):
    s=[l for l in open(f"{d}/{c}.log") if " summary " in l]
    if s: ok.append(s[-1].strip())
print(ok)
