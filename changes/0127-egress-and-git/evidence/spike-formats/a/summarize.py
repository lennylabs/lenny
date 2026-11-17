import json, sys, os
d = sys.argv[1]
s3 = [json.loads(l) for l in open(os.path.join(d, "s3.log")) if l.startswith("{")]
for c in ["py", "go", "js", "java"]:
    fetch = {}
    for l in open(os.path.join(d, f"cred-{c}.log")):
        if l.startswith("{"):
            x = json.loads(l)
            if x.get("akid"): fetch[x["akid"]] = (x["t"], x["exp"])
    print(f"== {c}: {len(fetch)} credential fetches")
    for x in s3:
        if x["akid"] and x["akid"].startswith("AKID" + c.upper()):
            ft, exp = fetch.get(x["akid"], ("?", "?"))
            print(f"  s3 {x['t']} signed with {x['akid']} (fetched {ft}, exp {exp[11:19]}) sectok={x['sectok']}")
