#!/usr/bin/env python3
"""A deployer's sync job: it polls the document, renders it, and reconfigures Squid. It renders deny-all when the document it holds has expired."""
import json,os,subprocess,sys,time,urllib.request
URL,OUT,V,EVERY=sys.argv[1],sys.argv[2],sys.argv[3],float(sys.argv[4])
ver=None; held=None
while True:
    try:
        d=json.load(urllib.request.urlopen(URL,timeout=2))
        held=d
        if d["version"]!=ver:
            json.dump(d,open(OUT+"/p.json","w"))
            subprocess.check_call(["python3","/l1/render5.py",OUT+"/p.json",OUT,V])
            if ver is not None: subprocess.call(["squid","-k","reconfigure","-f",OUT+"/squid.conf"])
            ver=d["version"]; print("synced",ver,time.time(),flush=True)
    except Exception as e:
        print("sync error",e,flush=True)
    time.sleep(EVERY)
