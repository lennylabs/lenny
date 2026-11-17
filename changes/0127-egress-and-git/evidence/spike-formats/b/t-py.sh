. /b/prelude.sh
python -c 'import requests,httpx,sys; print("python",sys.version.split()[0],"requests",requests.__version__,"httpx",httpx.__version__)'; pip --version
for v in $VARIANTS; do setup $v
python - $v <<'PY'
import sys, requests, httpx
v = sys.argv[1]; U = "http://fake.test/t"
def go(name, f):
    print("---", f"{name}-{v}")
    try: print(f(f"{U}/{name}-{v}/plain"))
    except Exception as e: print("EXC", type(e).__name__, e)
go("requests", lambda u: requests.get(u).status_code)
s = requests.Session(); s.trust_env = False
go("requests-notrustenv", lambda u: s.get(u).status_code)
go("httpx", lambda u: httpx.get(u).status_code)
go("httpx-netrcauth", lambda u: httpx.get(u, auth=httpx.NetRCAuth()).status_code)
go("httpx-netrcauth-envfile", lambda u: httpx.get(u, auth=httpx.NetRCAuth(file="/cfg/netrc")).status_code)
PY
r pip-$v; pip download --no-deps --no-input --disable-pip-version-check -q -d /tmp/dl --index-url $U/pip-$v/simple/ --trusted-host fake.test spikepkg 2>&1 | tail -2
done
