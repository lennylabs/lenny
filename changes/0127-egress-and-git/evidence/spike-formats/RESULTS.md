# Spike results: AWS container credentials endpoint and .netrc support

Date: 2026-11-02 (UTC). Host: Docker 29.4.3 on macOS, containers linux/arm64.
All test traffic ran on the Docker network `spike-net`, created with `--internal`. A probe to `https://example.com` from that network failed (curl `000`), so no test reached the internet. Images were built with internet access on the default build network; tests ran only on `spike-net`.

Raw logs are kept next to this file:

- `a/out-short/`: Test A run with a 180 s credential lifetime and a token rotation at t=100 s.
- `a/out-mid/`: Test A run with a 420 s credential lifetime and no rotation.
- `a/nonlo.log`: Test A non-loopback rejection.
- `b/out/`: Test B client output (`tools.log`, `py.log`, `go.log`, `go-tls.log`, `node.log`, `java.log`, `own-*.log`) and the fake server request log (`server.log`, `server-http-phase.log`).

## Test A: AWS container credentials endpoint

### Setup

- `a/servers/credsrv.py` plays the pod-local agent. It serves `GET /creds` on `0.0.0.0:8080`, re-reads `/tok/token` on every request, and returns 401 unless the `Authorization` header equals the file content (whitespace-stripped for comparison; the raw header is logged). A 200 response carries `AccessKeyId` = `AKID<CLIENT><counter>`, so each fetch has a unique key ID, `Token` = `sesstok-<client>-<counter>`, and `Expiration` = now + `EXPIRY_SECONDS`.
- `a/servers/fakes3.py` is reachable as `http://fakes3:9000`. It logs the SigV4 `Credential=` key ID and the `X-Amz-Security-Token` header of every request and returns a one-bucket `ListAllMyBucketsResult`.
- Each client container ran with `--network container:spike-cred-<client>`, so `127.0.0.1:8080` is the agent in a shared network namespace (the pod model).
- Client environment for every SDK:

```
AWS_CONTAINER_CREDENTIALS_FULL_URI=http://127.0.0.1:8080/creds
AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE=/tok/token
AWS_ENDPOINT_URL=http://fakes3:9000
AWS_REGION=us-east-1
AWS_EC2_METADATA_DISABLED=true
```

No other credential configuration existed in the images (no env keys, no `~/.aws`).

- Each SDK client (`a/py/loop.py`, `a/go/main.go`, `a/js/loop.mjs`, `a/java/.../Main.java`) builds one S3 client and calls `ListBuckets` every 15 s in one long-lived process. The CLIs were invoked once per call.

### Versions

| Client | Version |
|:--|:--|
| boto3 | boto3 1.43.123, botocore 1.43.123, Python 3.13.18 |
| AWS CLI v1 (pip `awscli`) | `aws-cli/1.46.2 Python/3.13.18 botocore/1.43.62` (as printed by `aws --version`) |
| AWS CLI v2 (official aarch64 installer) | `aws-cli/2.39.3 Python/3.14.8 exe/aarch64.debian.13` |
| AWS SDK for Go v2 | aws-sdk-go-v2 v1.47.5, config v1.33.13, credentials v1.20.13, service/s3 v1.114.6, Go 1.26 |
| AWS SDK for JavaScript v3 | @aws-sdk/client-s3 3.1161.0, Node v24.22.2 |
| AWS SDK for Java v2 | 2.31.50 (pinned in `a/java/pom.xml`, not the newest release), Temurin 21.0.13 |

### Fetch and sign

Every client fetched credentials from the endpoint with `Authorization: <token file content>` and signed the S3 request with the fetched key ID and session token. Example from `a/out-short/s3.log` and `cred-go.log`: fetch `AKIDGO0001` at 06:35:36.638, S3 request at 06:35:36.642 with `Credential=AKIDGO0001/...` and `X-Amz-Security-Token: sesstok-go-1`. The same pairing holds for all clients in both runs (see `python3 a/summarize.py a/out-short`).

| Client | Fetch + sign | Notes |
|:--|:--|:--|
| boto3 | PASS | |
| AWS CLI v1 | PASS | Two fetches per invocation with a 180 s lifetime (initial load, then an immediate refresh because the lifetime is inside botocore's refresh window). |
| AWS CLI v2 | PASS | Same two-fetch behavior. |
| Go SDK v2 | PASS | `UsePathStyle` set in code; not required for ListBuckets. |
| JS SDK v3 | PASS | |
| Java SDK v2 | PASS | |

### Token file re-read

In `a/out-short`, the harness overwrote every token file with `tok2-<client>` at 06:37:17 while the clients kept running. Every subsequent fetch from every SDK sent `tok2-<client>`, and no fetch received a 401. The CLI v2 smoke test (`a/smoke.sh`) changed the token between two invocations and the second invocation sent the new token.

Result: boto3, Go v2, JS v3, and Java v2 re-read `AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE` on each credential fetch inside one process. PASS for all four.

### Trailing newline in the token file

With the token file written as `tok-nl-cli2\n`, every client refused to send it:

- boto3 and CLI v2: `Auth token value is not a legal header value`.
- Go v2: `resolve auth token: authorization token contains invalid newline sequence`.
- JS v3 `fromHttp`: `TypeError [ERR_INVALID_CHAR]: Invalid character in header content ["Authorization"]`. The default chain then fell through to another provider that sent a request to the same `FULL_URI` with no `Authorization` header at all (seen twice in the agent log, `ua: null`, about 3 s after the client started). This was attributed to the JS client by isolating it in a separate run; the provider name was not confirmed.
- Java v2: `SdkClientException ... Unable to load credentials from any of the providers in the chain`.

The token file content must not end in a newline. The agent must also treat an unauthenticated request as a 401, which `credsrv.py` did.

### Refresh before Expiration

No client signed a request with an expired credential in either run.

180 s lifetime (`a/out-short`):

- boto3, Go v2, and JS v3 fetched new credentials on every call (15 s cadence), so the lifetime is inside each SDK's refresh window.
- JS v3 signed each call with the credential fetched on the previous call and fetched a new one in the same call.
- Java v2 fetched on every call for the first 11 calls, then reused one credential for 45 to 60 s at a time (for example `AKIDJAVA0012` fetched 06:38:52, used through 06:39:37 with 3 min 0 s to 2 min 15 s remaining).

420 s lifetime (`a/out-mid`):

| Client | Observed refresh point | Fetches in 300 s |
|:--|:--|:--|
| boto3 | Every call. A 420 s credential is always inside its refresh window. | 21 |
| Go v2 | When about 300 s remained (fetched 06:40:40, reused through 06:42:25 with 315 s left, refetched 06:42:40 with 300 s left). | 3 |
| JS v3 | When about 300 s remained (fetched 06:40:40.9, refetched 06:42:41.1). | 3 |
| Java v2 | When 165 to 180 s remained (reused `AKIDJAVA0001` through 06:44:41 with 180 s left, refetched 06:44:56 with 165 s left), then every call. | 5 |

The exact window constants were not read from SDK source; the table states only the observed points. A lifetime of 2 to 3 minutes makes boto3, Go, and JS fetch on every call, which multiplies agent load.

### Non-loopback http FULL_URI

Each client ran on `spike-net` (its own network namespace) with `AWS_CONTAINER_CREDENTIALS_FULL_URI=http://cred-nonlo:8080/creds`. The agent at `cred-nonlo` received zero requests. All clients rejected the URI:

| Client | Result | Message |
|:--|:--|:--|
| boto3 | Rejected | `ValueError: Unsupported host 'cred-nonlo'. Can only retrieve metadata from a loopback address or one of these hosts: 169.254.170.2, 169.254.170.23, fd00:ec2::23, localhost` |
| CLI v1 | Rejected | same text |
| CLI v2 | Rejected | `aws: [ERROR]: Unsupported host 'cred-nonlo'...` same text |
| Go v2 | Rejected at config load | `invalid endpoint host, "cred-nonlo", only loopback/ecs/eks hosts are allowed` |
| JS v3 | Rejected | `fromHttp`: `URL not accepted. It must either be HTTPS or match one of the following: loopback CIDR 127.0.0.0/8 or [::1/128], ECS container host 169.254.170.2, EKS container host 169.254.170.23 or [fd00:ec2::23]` |
| Java v2 | Rejected | `The full URI (http://cred-nonlo:8080/creds) ... has an invalid host. Host should resolve to a loopback address or have the full URI be HTTPS.` |

Not tested: the link-local addresses (169.254.170.2, 169.254.170.23, fd00:ec2::23), `http://localhost`, and an HTTPS non-loopback URI.

## Test B: .netrc support

### Setup

- `b/fakehttp.py` runs on `spike-net` with the network alias `fake.test`, on port 80 (HTTP) and port 443 (HTTPS with a certificate for `fake.test` signed by a throwaway CA in `b/tls/`). Every request under `/t/<tag>/` is logged with its decoded Basic credentials. A request without `Authorization` receives `401` with `WWW-Authenticate: Basic realm="fake"`. A request with `Authorization` receives a git smart-HTTP v0 ref advertisement for `info/refs`, an empty simple-index page for `/simple/`, `200 ok` for `/plain`, and 404 otherwise.
- netrc line: `machine fake.test login alice password s3cret`.
- Variants (from `b/prelude.sh`), all run as root with `HOME=/root` unless noted:
  - `home600`: `$HOME/.netrc`, mode 0600.
  - `home644`: `$HOME/.netrc`, mode 0644.
  - `env600`: `NETRC=/cfg/netrc`, mode 0600, no `$HOME/.netrc`.
  - `env644`: `NETRC=/cfg/netrc`, mode 0644, no `$HOME/.netrc`.
  - `ownhome` / `ownenv` (`b/own-*.sh`): file owned by root, mode 0644, read by uid 1000 with `HOME=/home/u`, through `$HOME/.netrc` or through `NETRC=/cfg/netrc`.

### Versions

curl 8.14.1 (Alpine 3.22), GNU Wget 1.25.0, BusyBox v1.37.0 wget, git 2.49.1, Python 3.13.18 with requests 2.34.2 and httpx 0.28.1, pip 26.2.2, Go 1.26.7 (`GOAUTH=netrc` default), Node v24.22.2, npm 11.19.1, OpenJDK 21.0.13.

### Results

"Sends" means the server logged `alice:s3cret`. "Preemptive" means the first request already carried it; "after 401" means the client sent once without credentials, received the challenge, and retried with them.

| Client | `$HOME/.netrc` | `NETRC` env | 0644 / foreign owner | Opt-in required | When sent |
|:--|:--|:--|:--|:--|:--|
| curl | Sends with `--netrc` or `--netrc-optional`; no credentials without a flag | Ignored (`--netrc` failed with code 000 when only `NETRC` was set; `--netrc-optional` sent nothing). `--netrc-file /cfg/netrc` works | Accepted | Yes: `--netrc`, `--netrc-optional`, or `--netrc-file PATH` | Preemptive |
| BusyBox wget | Never sends | Never sends | n/a | No netrc support | n/a |
| GNU wget 1.25.0 | Sends by default | Ignored (exit 6, auth failure) | Accepted | No | After 401 |
| git 2.49.1 (HTTP `ls-remote`) | Sends by default | Ignored (`could not read Username`, with `GIT_TERMINAL_PROMPT=0`) | Accepted | No | After 401 |
| Python requests 2.34.2 | Sends by default | Honored | Accepted (0644 and root-owned both sent) | No; `Session.trust_env = False` disables it | Preemptive |
| httpx 0.28.1 default client | Never sends (`trust_env=True` default) | Never sends | n/a | Requires `auth=httpx.NetRCAuth()` | n/a |
| httpx `NetRCAuth()` | Sends with mode 0600 | `NetRCAuth()` does not read `NETRC` (looked for `/root/.netrc`); `NetRCAuth(file=path)` works | Rejected for `~/.netrc`: `NetrcParseError ... access too permissive` (0644) and `file owner (root, uid 1000) does not match current user`. An explicit `file=` path at 0644 or root-owned was accepted | Yes | Preemptive |
| pip 26.2.2 (`--index-url http://fake.test/... --trusted-host fake.test`) | Sends by default | Honored | Accepted | No | Preemptive |
| Go 1.26.7 `go mod download`, `GOPROXY=https://...` | Sends by default | Honored | Accepted | No (`GOAUTH=off` disables it) | Preemptive (two requests per download, both authenticated) |
| Go 1.26.7, `GOPROXY=http://...` | Never sends, with or without `GOINSECURE=fake.test` | Never sends | n/a | Not possible over plain HTTP | n/a |
| npm 11.19.1 (`npm view --registry http://fake.test/...`) | Never sends | Never sends | n/a | No netrc support | n/a |
| Node 24 `fetch` | Never sends | Never sends | n/a | No netrc support | n/a |
| Java 21 `HttpClient` and `HttpURLConnection` | Never sends | Never sends | n/a | No netrc support | n/a |

Go over HTTPS used `SSL_CERT_FILE=/b/tls/ca.crt` to trust the test CA, with `GOSUMDB=off`.

### Permission notes

- Python's `netrc` module checks owner and mode only when it opens the default `~/.netrc` itself. requests and pip passed an explicit path and accepted mode 0644 and a root-owned file. httpx `NetRCAuth()` without `file=` applied the check and raised.
- curl, GNU wget, git, and Go accepted mode 0644 and a root-owned file.

## Cleanup

All containers, the `spike-net` network, and the images built for this spike (`spike-a-*`, `spike-b-*`) were removed after the runs. Pulled base images were kept.
