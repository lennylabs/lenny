. /b/own-prep.sh
U=http://fake.test/t
for m in home env; do
  if [ $m = env ]; then mv /home/u/.netrc /home/u/.netrc.off; X="NETRC=/cfg/netrc"; else X="NOTHING=1"; fi
  setpriv --reuid=1000 --regid=1000 --clear-groups env HOME=/home/u $X python - $m <<'PY'
import sys, os, requests, httpx
m = sys.argv[1]; U = "http://fake.test/t"
print("uid", os.getuid())
for name, f in [("requests", lambda u: requests.get(u).status_code),
                ("httpx-netrcauth", lambda u: httpx.get(u, auth=httpx.NetRCAuth(file=os.environ.get("NETRC"))).status_code)]:
    print("---", f"{name}-own{m}")
    try: print(f(f"{U}/{name}-own{m}/plain"))
    except Exception as e: print("EXC", type(e).__name__, e)
PY
  echo "--- pip-own$m"; setpriv --reuid=1000 --regid=1000 --clear-groups env HOME=/home/u $X pip download --no-deps --no-input --disable-pip-version-check -q -d /tmp/dl$m --index-url $U/pip-own$m/simple/ --trusted-host fake.test spikepkg 2>&1 | tail -1
done
