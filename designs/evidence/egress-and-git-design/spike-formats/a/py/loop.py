import os, time, datetime, boto3, botocore
s3 = boto3.client("s3", region_name="us-east-1")
dur = int(os.environ.get("LOOP_SECONDS", "0")); iv = int(os.environ.get("INTERVAL", "15"))
start = time.time()
print("boto3", boto3.__version__, "botocore", botocore.__version__, flush=True)
while True:
    t = datetime.datetime.now(datetime.timezone.utc).strftime("%H:%M:%S")
    try:
        r = s3.list_buckets(); print(t, "OK", [b["Name"] for b in r["Buckets"]], flush=True)
    except Exception as e:
        print(t, "ERR", type(e).__name__, str(e)[:300], flush=True)
    if time.time() - start + iv > dur: break
    time.sleep(iv)
