import sys,json
t=sys.stdin.read().strip()
if not t: print("   (no summary)"); sys.exit()
d=json.loads(t)
for k in ("busy","small","noisy"):
    c=d.get(k)
    if c and c["n"]: print("   %s: n=%d codes=%s p50=%.2f p90=%.2f p99=%.2f p99.9=%.1f max=%.1f ms" % (k,c["n"],c["codes"],c["p50ms"],c["p90ms"],c["p99ms"],c["p999ms"],c["maxms"]))
print("   elapsed %.0fs broken=%d refusedFirst=%d retried=%d retryPassed=%d retryRefused=%d retryOther=%d" % (d["elapsed"],d["broken"],d["refusedFirst"],d["retried"],d["retryPassed"],d["retryRefused"],d["retryOther"]))
