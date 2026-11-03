#!/usr/bin/env python3
# i1-sum.py WLOG CLOG: reads the per-reading logs, prints the last good reading and the peak.
import sys
M=1048576
w=[l.split() for l in open(sys.argv[1]) if len(l.split())==15]
c=[l.split() for l in open(sys.argv[2]) if len(l.split())==5]
c=[list(map(int,x)) for x in c]; w=[list(map(int,x)) for x in w]
peak=max((x[2] for x in c),default=0); ups=max((x[1] for x in c),default=0); fail=max((x[3] for x in c),default=0)
# last reading with the cgroup present and the stats endpoint answering, and last reading with the cgroup present
good=[x for x in w if x[1]>=0 and x[9]>=0]; cg=[x for x in w if x[1]>=0]
lg=good[-1] if good else None; lc=cg[-1] if cg else None
oom=max((x[7] for x in w),default=0)
tl=(good[-1][0] if good else 0)
cbefore=[x for x in c if x[0]<=tl]
fb=cbefore[-1][3] if cbefore else 0; lb=cbefore[-1][2] if cbefore else 0
print(f"up_total={ups} live_peak={peak} live_at_last_stats={lb} fail_at_last_stats={fb} fail_total={fail} readings={len(w)}")
if lg:
  print(f"  last stats reading: current={lg[1]/M:.1f} anon={lg[3]/M:.1f} sock={lg[4]/M:.1f} kernel={lg[5]/M:.1f} file={lg[6]/M:.1f} peak={lg[2]/M:.1f} MiB goroutines={lg[9]} heapInuse={lg[10]/M:.1f} stack={lg[11]/M:.1f} active={lg[12]} terminated={lg[13]} opaque={lg[14]}")
if lc:
  print(f"  last cgroup reading: current={lc[1]/M:.1f} peak={lc[2]/M:.1f} anon={lc[3]/M:.1f} sock={lc[4]/M:.1f}")
