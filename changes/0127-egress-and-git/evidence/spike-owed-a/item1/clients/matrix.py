import sys,glob,collections
L={"https://allowed.oa.test/":"allowed","https://denied.oa.test/":"denied","https://sub.allowed.oa.test/":"sub","https://viaint.oa.test/":"viaint","https://198.19.50.20/":"ip","https://ctl.oa.test/":"ctl","https://unk.oa.test/":"unk","https://xallowed.oa.test/":"xallowed","https://xdenied.oa.test/":"xdenied","https://198.19.50.20:8443/":"xip"}
cols=list(L.values()); M=collections.OrderedDict()
for f in sorted(glob.glob("raw/*.out")):
    if "matrix" in f: continue
    for l in open(f):
        p=l.split(None,3)
        if len(p)>=3 and p[1] in L:
            M.setdefault(p[0],{})[L[p[1]]]=("ok" if p[2]=="OK" else "REF")+("" )
print("| client | "+" | ".join(cols)+" |"); print("|:--|"+":--|"*len(cols))
for k,v in M.items(): print("| "+k+" | "+" | ".join(v.get(c,"?") for c in cols)+" |")
