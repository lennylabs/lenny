#!/usr/bin/env python3
import re,glob,statistics as st,sys
d=sys.argv[1] if len(sys.argv)>1 else "out/s1-renew"
def rd(f): return open(f"{d}/{f}").read().splitlines()
print("### leaf renewals of lenny-egress-tls: Secret write -> file in the pod -> first TLS handshake that shows the new serial")
secs=[]
for l in rd("secrets.log"):
    m=re.match(r"(\d+) (\w+) lenny-system/lenny-egress-tls rv=\d+ lenny-egress\.lenny-system\.svc#(\w+)",l)
    if m: secs.append((int(m[1]),m[3]))
# de-duplicate by serial: first time a serial appears
first={}
for t,s in secs: first.setdefault(s,t)
files={}
for l in rd("envoy-wleaf.log"):
    m=re.match(r"(\d+) change .*certs=\[lenny-egress\.lenny-system\.svc#(\w+)\]",l)
    if m: files.setdefault(m[2],int(m[1]))
served={}
for c in ("c1","c2","c3"):
    for l in rd(c+".log"):
        m=re.match(r"(\d+) serial (\w+)/",l)
        if m:
            t=int(m[1]); s=m[2]
            if s not in served or t<served[s]: served[s]=t
dfile=[];dserved=[]
for s,t in sorted(first.items(),key=lambda x:x[1]):
    f=files.get(s); v=served.get(s)
    a=(f-t)/1e9 if f else None; b=(v-f)/1e9 if (v and f) else None
    print(f" serial {s}: secret->file {a if a is None else round(a,2)} s; file->served {b if b is None else round(b,2)} s")
    if a is not None: dfile.append(a)
    if b is not None: dserved.append(b)
if dfile: print(f" secret->file: n={len(dfile)} min={min(dfile):.2f} max={max(dfile):.2f}")
if dserved: print(f" file->served: n={len(dserved)} min={min(dserved):.2f} max={max(dserved):.2f}")
print("### client probes")
for c in ("c1","c2","c3"):
    ls=rd(c+".log"); f=[l for l in ls if " FAIL " in l]; sm=[l for l in ls if " summary " in l]
    print(f" {c}: FAIL lines={len(f)}; {sm[-1] if sm else ''}")
    for x in f[:5]: print("   ",x[:170])
print("### soak"); 
for l in rd("soak.log")[-12:]: print(" ",l[:200])
print("### stats")
for l in rd("envoy-stats.txt"):
    if re.search(r"^sds\.|update_(success|failure|rejected)|^listener\.0\.0\.0\.0_3128\.ssl\.(connection_error|handshake|fail)|^cluster\.authz\.ssl\.(connection_error|handshake|fail)|^cluster\.authz\.upstream_cx_(total|active|destroy$|connect_fail)|^downstream_cx_active|^listener\.0\.0\.0\.0_3128\.downstream_cx_(active|destroy)",l): print(" ",l)
print("### I3 peers seen by the decision service")
for l in rd("authz.log"):
    if "I3PEER" in l: print(" ",l[:170])
print("### ConfigMap propagation: PUT returned -> file changed in running pods, no subPath")
w={}
for l in rd("writer.log"):
    m=re.match(r"WRITE (\d+) start=(\d+) end=(\d+) code=(\d+)",l)
    if m: w[m[1]]=int(m[3])
all=[];perpod={}
for p in ("p1","p2","p3","p4","p5"):
    for l in rd(p+".log"):
        m=re.match(r'(\d+) change .*head="(\d{6})"',l)
        if m and m[2] in w:
            x=(int(m[1])-w[m[2]])/1e9; all.append(x); perpod.setdefault(p,[]).append(x)
if all:
    all.sort(); print(f" samples={len(all)} writes={len(w)} min={all[0]:.1f} median={st.median(all):.1f} p90={all[int(.9*len(all))]:.1f} max={all[-1]:.1f} s")
    print(" per pod max:",{k:round(max(v),1) for k,v in perpod.items()}, "samples per pod:",{k:len(v) for k,v in perpod.items()})
    print(" note: a write overtaken by the next write before delivery shows no sample for that value")
sub=[l for l in rd("psub.log")]
print(" subPath control: changes seen:",len(sub)-0,[l[:60] for l in sub[:3]])
