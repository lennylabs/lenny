#!/usr/bin/env python3
# ledger.py [--total]: the run's cost from ledger.tsv (instances and their volumes), extras.tsv, and S3
import json, subprocess, sys, datetime, os
A = os.path.dirname(os.path.abspath(__file__))
PRICE = {"m8i.2xlarge": 0.42336, "m7i.metal-24xl": 4.8384, "i4i.2xlarge": 0.686}
GB_H = 0.08 / 730; IPV4_H = 0.005
env = dict(os.environ, AWS_PROFILE="lenny-tier6", AWS_REGION="us-west-2", AWS_PAGER="")
now = datetime.datetime.now(datetime.timezone.utc)
rows = []
total = 0.0
for line in open(os.path.join(A, "ledger.tsv")) if os.path.exists(os.path.join(A, "ledger.tsv")) else []:
    iid, name, typ, start, gb, mins = line.rstrip("\n").split("\t")[:6]
    t0 = datetime.datetime.fromisoformat(start.replace("Z", "+00:00"))
    end, state = None, "?"
    endf = os.path.join(A, f"end-{iid}")
    if os.path.exists(endf):
        end = datetime.datetime.fromisoformat(open(endf).read().strip().replace("Z", "+00:00")); state = "terminated"
    else:
        try:
            r = json.loads(subprocess.run(["aws", "ec2", "describe-instances", "--instance-ids", iid, "--output", "json"],
                                          capture_output=True, text=True, env=env, timeout=60).stdout)
            inst = r["Reservations"][0]["Instances"][0]; state = inst["State"]["Name"]
            if state in ("terminated", "shutting-down"):
                end = now  # conservative: first time seen terminated
                open(endf, "w").write(now.isoformat())
        except Exception:
            end = t0 + datetime.timedelta(minutes=int(mins)); state = "unknown(capped)"
    e = end or now
    hrs = max((e - t0).total_seconds() / 3600, 1 / 60)
    cost = hrs * (PRICE.get(typ, 1.0) + IPV4_H + int(gb) * GB_H)
    total += cost
    rows.append((iid, name, typ, state, round(hrs, 3), round(cost, 3)))
extras = os.path.join(A, "extras.tsv")  # what, usd
if os.path.exists(extras):
    for line in open(extras):
        w, usd = line.rstrip("\n").split("\t")[:2]; total += float(usd); rows.append(("extra", w, "", "", "", float(usd)))
if "--total" in sys.argv:
    print(f"{total:.2f}")
else:
    for r in rows: print("\t".join(map(str, r)))
    print(f"TOTAL\t${total:.2f}")
