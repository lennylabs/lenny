# Federation spike results

Date: 2026-11-01 (host clock PST; server log timestamps are UTC). Docker 29.4.3 on macOS (linuxkit arm64).

## Harness

- `spikefed-net` is a `docker network create --internal` network. Internet egress from it fails (verified: `https://example.com` raised `ConnectionError`).
- `spikefed-srv` (`server/fakesrv.py`, Python stdlib) listens on HTTP :80, HTTP :8080, and HTTPS :443 (cert from a throwaway CA in `certs/`). It routes by path and logs every request as one JSON line to `logs/server.jsonl`. Network aliases: `sts.aws.test`, `dynamodb.aws.test`, `sts.gcp.test`, `iamcredentials.gcp.test`, `api.gcp.test`, `login.azure.test`, `api.azure.test`, `msi.azure.test`, `metadata.gcp.test`, and `cloudresourcemanager.googleapis.com` (added after the first GCP run, see b).
- Server-issued credentials encode the subject-token version, so the service log shows which token file content produced the credential: AWS AccessKeyId `ASIAFAKEV1...`/`ASIAFAKEV2...`, GCP `gcp-sts-at-v1-...`, Azure `az-at-<tenant8>-v1-...`. All issued credentials expire after `EXP=20` seconds.
- JWTs: RS256, signed by a throwaway key (`run/issuer-key.pem`), `iss=https://issuer.netagent.test`, `sub=tenant-a:session-123`, a `ver` claim of 1 or 2, `aud` = `sts.amazonaws.com` (AWS), the WIF provider resource name (GCP), or `api://AzureADTokenExchange` (Azure). `run/<provider>/token.v1` and `token.v2`.
- Re-read test: each client copies `token.v1` to `token` (atomic rename), makes call 1, renames `token.v2` into place, sleeps (`SLEEP`, default 31 s, longer than the 20 s expiry), and makes call 2. Re-read is PASS when the second STS/token request carries `ver: 2`.
- `run/` is mounted at `/var/run/netagent` in every client. Family 2 clients use `--network container:spikefed-srv` so `127.0.0.1` is the fake server.
- Per-test transcripts (client output plus the server log lines for that test) are in `logs/<test>.out`. `t.sh` runs a test; `srv.sh` (re)creates the server.

## Versions

| Image | Libraries |
|:--|:--|
| `spikefed-py` (python:3.13-slim, CPython 3.13.18) | boto3/botocore 1.43.123, google-auth 2.62.0, requests 2.34.2, azure-identity 1.27.0, azure-core 1.41.0, msal 1.39.0 |
| `spikefed-node` (node:24-slim, Node 24.22.2) | @aws-sdk/client-dynamodb 3.1161.0, @aws-sdk/credential-providers 3.1161.0 (credential-provider-ini 3.973.25, credential-provider-web-identity 3.972.86, nested-clients 3.997.54), google-auth-library 11.3.2 (gcp-metadata 9.2.3), @azure/identity 4.14.1 (@azure/msal-node 7.1.2) |
| `spikefed-go` (golang:1.26, go1.26.7) | aws-sdk-go-v2 1.47.5, config 1.33.13, credentials 1.20.13, service/sts 1.51.6, service/dynamodb 1.72.3, golang.org/x/oauth2 0.37.0, cloud.google.com/go/compute/metadata 0.10.0, azidentity 1.15.0, azcore 1.23.3, MSAL Go 1.10.1 |
| `spikefed-java` (maven:3.9-eclipse-temurin-21) | AWS SDK for Java v2 2.57.8 (dynamodb, sts, auth, regions, sdk-core) |

## TLS requirements

| Endpoint | HTTP accepted | Notes |
|:--|:--|:--|
| AWS STS and service endpoints (all four SDKs) | Yes | All AWS tests used `http://`. |
| GCP STS `token_url`, IAM credentials impersonation URL (Python, Node, Go) | Yes | All GCP tests used `http://`. |
| GCP Cloud Resource Manager project lookup (Python, Node) | n/a | The libraries build `https://cloudresourcemanager.googleapis.com/...` themselves; the fake answered over TLS with the test CA (`REQUESTS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`). Node also accepts `cloud_resource_manager_url` in the JSON, and `http://` worked there. |
| Azure authority host (Python, JS, Go) | No | Python: `ValueError: 'http://login.azure.test' is an invalid authority. The value must be a TLS protected (https) URL.` JS: `The authorityHost address must use the 'https' protocol.` Go: `cannot use an authority host without https`. Test CA trusted with `REQUESTS_CA_BUNDLE`+`SSL_CERT_FILE` (Python), `NODE_EXTRA_CA_CERTS` (JS), `SSL_CERT_FILE` (Go). |
| Azure API call through azure-core `BearerTokenCredentialPolicy` (Python) | Not tried | The test used `https://api.azure.test`. |
| GCE metadata emulation (Python, Node, Go) | Yes | Plain HTTP on `127.0.0.1:8080` and on `metadata.gcp.test:8080`. |
| Azure App Service managed identity endpoint (Python, JS, Go) | Yes | Plain HTTP on `127.0.0.1:8080` and on `msi.azure.test:8080`. |

## Family 1: OIDC token-file federation

### a. AWS web identity via shared config file

Files: `run/aws/config` (`/var/run/netagent/aws/config`):

```ini
[default]
region = us-east-1
role_arn = arn:aws:iam::111122223333:role/tenant-a-role
web_identity_token_file = /var/run/netagent/aws/token
role_session_name = netagent-tenant-a
```

Env (`env/aws.env`): `AWS_CONFIG_FILE=/var/run/netagent/aws/config`, `AWS_SHARED_CREDENTIALS_FILE=/nonexistent`, `AWS_ENDPOINT_URL_STS=http://sts.aws.test`, `AWS_ENDPOINT_URL=http://dynamodb.aws.test`, `AWS_EC2_METADATA_DISABLED=true`. No `AWS_ROLE_ARN`, `AWS_WEB_IDENTITY_TOKEN_FILE`, `AWS_REGION`, or `AWS_PROFILE`. The API call is DynamoDB `ListTables` (SigV4).

| Client | Result | Observed |
|:--|:--|:--|
| boto3 1.43.123 (`boto3.Session().client("dynamodb")`) | PASS | STS `AssumeRoleWithWebIdentity` form POST to `sts.aws.test` with `RoleArn`, `RoleSessionName` from the file; region from the file; DynamoDB request signed with `ASIAFAKEV1...` + `X-Amz-Security-Token`. After the swap, the next refresh sent the v2 token and DynamoDB saw `ASIAFAKEV2...`. |
| AWS SDK Go v2 (`config.LoadDefaultConfig`) | PASS | Same. Re-read PASS. A `WARN ... response did not match expected checksum` came from the fake DynamoDB missing `x-amz-crc32`; fixed in the server, not an SDK issue. |
| AWS SDK JS v3 (`new DynamoDBClient({})`) | PASS | Same. Re-read PASS. With short-lived credentials JS refreshes in the background: the call made 1 s after the swap still used v1 credentials while a parallel STS call with v2 ran. |
| AWS SDK Java v2 2.57.8 (`DynamoDbClient.create()`) | PASS | Same. Re-read PASS. `CachedSupplier` logged `Retrieved value expiration is in the past`, which is due to the 20 s test expiry being shorter than the SDK's 1 min stale window. |

Variant `a-*-svc`: endpoints moved into the config file with no endpoint env vars (`env/aws-svc.env`, `run/aws/config-svc`):

```ini
[default]
...
services = fake
[services fake]
sts =
  endpoint_url = http://sts.aws.test
dynamodb =
  endpoint_url = http://dynamodb.aws.test
```

All four SDKs sent STS and DynamoDB to the fake server (PASS for boto3, Go, JS, and Java). With this variant the only environment variable is `AWS_CONFIG_FILE` (constant path), plus the two disabling vars.

Caveats: boto3 and JS issued two STS calls per refresh with the 20 s expiry (credentials stayed inside the SDK's refresh window). AWS needs no TLS for the fake.

### b. GCP external account (workload identity federation)

Files: `run/gcp/ea-http.json`:

```json
{"type": "external_account",
 "audience": "//iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/netagent-pool/providers/netagent",
 "subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
 "token_url": "http://sts.gcp.test/v1/token",
 "credential_source": {"file": "/var/run/netagent/gcp/token"}}
```

`ea-imp-http.json` adds `"service_account_impersonation_url": "http://iamcredentials.gcp.test/v1/projects/-/serviceAccounts/tenant-a@proj-a.iam.gserviceaccount.com:generateAccessToken"`.

Env: `GOOGLE_APPLICATION_CREDENTIALS=/var/run/netagent/gcp/ea-http.json` (plus CA trust vars for the CRM lookup).

| Client | Result | Observed |
|:--|:--|:--|
| google-auth 2.62.0 (`google.auth.default()`, `refresh()`, `AuthorizedSession`) | PASS with caveat | STS form POST: `grant_type=urn:ietf:params:oauth:grant-type:token-exchange`, the `audience`, `subject_token_type`, `scope`, and the JWT as `subject_token`. API saw `Bearer gcp-sts-at-v1-...`, then `...v2-...` after the swap. Impersonation variant: `generateAccessToken` called with the STS token; API saw `gcp-sa-at-from-gcp-sts-at-v2-...` after the swap. |
| google-auth-library 11.3.2 (`new GoogleAuth().getClient()`, `client.request`) | PASS with caveat | Same STS request shape; re-read PASS; impersonation variant PASS. |
| golang.org/x/oauth2 0.37.0 (`google.FindDefaultCredentials`, `oauth2.NewClient`) | PASS | Same; re-read PASS; impersonation PASS. Go does not perform the project-id lookup (`ProjectID` is empty). |

Project-id caveat (observed):

- `google.auth.default()` (Python) calls `GET https://cloudresourcemanager.googleapis.com/v1/projects/<project number from audience>` with the federated token. When that host is unreachable (DNS failure, or connection refused via `--add-host ...:127.0.0.1`), `google.auth.default()` raises `TransportError` and the program exits. When it returns 403, Python continues (`project` = None) and the API calls work. With `GOOGLE_CLOUD_PROJECT=proj-a` the lookup is skipped.
- Node `GoogleAuth.getClient()` performs the same lookup and fails on both an unreachable host and a 403 (`ERR getClient Permission denied`). With `GOOGLE_CLOUD_PROJECT=proj-a` it is skipped. Node also reads `cloud_resource_manager_url` from the credential JSON; pointing it at `http://api.gcp.test/v1/projects/` (fake) made `getClient()` succeed with no env var (`b-node-crmurl`). Python has no such JSON field (its URL derives from `universe_domain`).
- In this harness the lookup is satisfied by aliasing `cloudresourcemanager.googleapis.com` to the fake server with a test-CA cert.

Token file re-read: every refresh re-read the file in all three libraries (no file cache observed).

### c. Azure workload identity

Env (`env/azwi.env`): `AZURE_FEDERATED_TOKEN_FILE=/var/run/netagent/azure/token`, `AZURE_CLIENT_ID=1111...`, `AZURE_TENANT_ID=2222...`, `AZURE_AUTHORITY_HOST=https://login.azure.test`, CA trust vars. Token request observed at the fake: `POST https://login.azure.test/<tenant>/oauth2/v2.0/token` with `grant_type=client_credentials`, `client_id`, `scope=https://management.azure.com/.default`, `client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer`, and the JWT as `client_assertion`.

| Client | Result | Discovery traffic observed | Instance discovery |
|:--|:--|:--|:--|
| azure-identity 1.27.0 Python `WorkloadIdentityCredential` | PASS | None: token POST only | Not needed. With `disable_instance_discovery` omitted, the run still made only the token POST. |
| Python `DefaultAzureCredential(disable_instance_discovery=True)` | PASS | Token POST only | |
| @azure/identity 4.14.1 `WorkloadIdentityCredential({disableInstanceDiscovery: true})` | PASS | `GET /<tenant>/v2.0/.well-known/openid-configuration`, then token POST | Required. Without it: `CredentialUnavailableError endpoints_resolution_error`, no request reached the fake. |
| JS `DefaultAzureCredential({disableInstanceDiscovery: true})` | PASS | Same | |
| azidentity 1.15.0 `NewWorkloadIdentityCredential(&{DisableInstanceDiscovery: true})` | PASS | `GET /<tenant>/v2.0/.well-known/openid-configuration?`, then token POST | Required. Without it: `GET https://login.microsoftonline.com/common/discovery/instance?api-version=1.1&authorization_endpoint=https://login.azure.test/<tenant>/oauth2/v2.0/authorize` failed DNS. |
| Go `NewDefaultAzureCredential(&{DisableInstanceDiscovery: true})` | PASS | Same | |

A grep of the installed sources found no environment variable that disables instance discovery; the tests used the constructor option (`disable_instance_discovery`, `disableInstanceDiscovery`, `DisableInstanceDiscovery`). JS `ManagedIdentityCredential`'s AKS token-exchange path hard-codes `disableInstanceDiscovery: true` (source). The JS and Go fakes needed an OIDC discovery document whose `token_endpoint` and `issuer` are on the fake host.

Where the IDs can come from (observed with `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, and `AZURE_FEDERATED_TOKEN_FILE` unset):

| Credential | client ID, tenant ID, token file outside env |
|:--|:--|
| Python `WorkloadIdentityCredential(tenant_id=, client_id=, token_file_path=)` | PASS (fake saw tenant `4444...` from the args) |
| JS `WorkloadIdentityCredential({tenantId, clientId, tokenFilePath})` | PASS |
| Go `WorkloadIdentityCredentialOptions{TenantID, ClientID, TokenFilePath}` | PASS |
| Python `DefaultAzureCredential(workload_identity_client_id=, workload_identity_tenant_id=)` | PASS, but the token file still comes only from `AZURE_FEDERATED_TOKEN_FILE` (source: `token_file_path=os.environ.get(...)`) |
| JS `DefaultAzureCredential({workloadIdentityClientId, tenantId})` | PASS with `AZURE_FEDERATED_TOKEN_FILE` set |
| Go `DefaultAzureCredentialOptions` | Has `TenantID` but no client ID field; client ID comes from `AZURE_CLIENT_ID` only (source read, not run) |

All three require constructor code to move IDs out of env; none reads them from a file. `AZURE_AUTHORITY_HOST` is constant across tenants.

Token file re-read: all three SDKs cache the file contents. Short re-read test (31 s): all three sent the v1 assertion again after the swap. Source: Python re-reads when `now - _last_read_time > 600`; JS when `Date.now() - cacheDate >= 5 min`; Go sets `expires = now.Add(10 * time.Minute)`.

Long re-read test, first attempt (`logs/c-*-wi-long.contaminated.out`): invalid. The `c-node-httpauth` and `c-go-httpauth` runs exited after renaming `token.v1` into place and before renaming `token.v2`, so the file held v1 when the long tests re-read it. The Docker VM was also suspended for about 15 minutes during that attempt (VM wall clock lagged the host by 15 min). Long re-read test, rerun (`logs/c-{py,node,go}-wi-long.out`, `SLEEP=640`): PASS for all three. Call 1 at 08:07:45-49 UTC sent the v1 assertion; call 2 at 08:30:48-53 UTC (server clock) sent the v2 assertion and the API saw `az-at-22222222-v2-...`. The elapsed server time exceeds 640 s because the Docker VM clock drifted during the wait; the result shows re-read after the cache window, not the exact window length. Combined with the 31 s runs, the observed behaviour is: a token file change is picked up by the next token request after 10 min (Python, Go) or 5 min (JS) from the previous file read, and not before.

### d. Vault JWT auth

Skipped.

## Family 2: pod-local metadata endpoints

### e. GCP metadata server emulation

Server: `127.0.0.1:8080` in the client's network namespace (`--network container:spikefed-srv`). Requests without `Metadata-Flavor: Google` get 403; every response carries `Metadata-Flavor: Google`. Every client sent the header on every metadata request.

| Client | Env honoured | Result | Paths requested (in order) |
|:--|:--|:--|:--|
| google-auth 2.62.0 Python | `GCE_METADATA_HOST` for data, `GCE_METADATA_IP` for the detection ping | `GCE_METADATA_HOST` alone: FAIL (`DefaultCredentialsError` after about 3 s; the ping went to 169.254.169.254). Both set (`127.0.0.1:8080`): PASS | `GET /` (ping), `/computeMetadata/v1/project/project-id`, `/computeMetadata/v1/instance/service-accounts/default/?recursive=true`, `/…/default/token?scopes=…`, `/computeMetadata/v1/universe/universe-domain`; on refresh `/…/service-accounts/<email>/?recursive=true` and `/…/default/token?scopes=…` |
| google-auth-library 11.3.2 Node | `GCE_METADATA_HOST` (detection ping goes to it) | PASS with `GCE_METADATA_HOST` alone, once the fake returned 200 for `/computeMetadata/v1/instance`. `METADATA_SERVER_DETECTION=assume-present` skips the ping. | `/computeMetadata/v1/instance` (ping), `/project/project-id`, `/instance/service-accounts/default/email`, `/…/default/token?scopes=…` |
| cloud.google.com/go/compute/metadata 0.10.0 + x/oauth2/google | `GCE_METADATA_HOST` (`OnGCE()` returns true with no request) | PASS | `/project/project-id`, `/…/default/email` (explicit `metadata.Email` call in the test), `/…/default/token?scopes=…` |

Python's refresh asks for `/service-accounts/<email>/` after the first token, so the emulator has to serve the email alias as well as `default`. A non-loopback host (`metadata.gcp.test:8080`) also worked for all three. Tokens are server-minted per request, so no file is involved.

### f. Azure managed identity (App Service style)

Env: `IDENTITY_ENDPOINT=http://127.0.0.1:8080/msi/token`, `IDENTITY_HEADER=s3cr3t-identity-header`. The fake returns `{access_token, expires_on (epoch string), resource, token_type, client_id}`.

| Client | Result | Request observed |
|:--|:--|:--|
| Python `ManagedIdentityCredential()` and `DefaultAzureCredential()` | PASS | `GET /msi/token?api-version=2019-08-01&resource=https://management.azure.com` with `X-IDENTITY-HEADER` and `Metadata: true` |
| JS `ManagedIdentityCredential()` and `DefaultAzureCredential()` | PASS | `GET /msi/token?api-version=2019-08-01&resource=https%3A%2F%2Fmanagement.azure.com` with `X-IDENTITY-HEADER` |
| Go `NewManagedIdentityCredential(nil)` and `NewDefaultAzureCredential(nil)` | PASS | Same as JS, header `X-Identity-Header` |

- `resource` is the scope without `/.default`. A user-assigned client ID (constructor `client_id` / `clientId`) adds `&client_id=<id>` (Python and JS observed).
- A non-loopback `IDENTITY_ENDPOINT=http://msi.azure.test:8080/msi/token` was accepted by all three, for both the managed identity credential and `DefaultAzureCredential`.
- Wrong `IDENTITY_HEADER` (fake returns 401): Python raised `CredentialUnavailableError App Service managed identity configuration not found in environment.`
- `DefaultAzureCredential` made exactly one request (to the MSI endpoint) in all three; no IMDS probe was observed.

## Per-tenant values and environment variables

| Mechanism | Per-tenant values | Where they can live with stock default-chain code |
|:--|:--|:--|
| AWS web identity | role ARN, session name, token, region, and endpoints | All in the config file (`AWS_CONFIG_FILE` is a constant path). Observed in all four SDKs. |
| GCP external account | audience, token URL, impersonation URL, token | All in the JSON file at a constant `GOOGLE_APPLICATION_CREDENTIALS` path. Project id: Python and Node need a reachable CRM lookup (Python also tolerates a 403), or `GOOGLE_CLOUD_PROJECT` in env; Node alternatively reads `cloud_resource_manager_url` from the file. |
| Azure workload identity | tenant ID, client ID | Env only (`AZURE_TENANT_ID`, `AZURE_CLIENT_ID`) for the default-chain path in all three SDKs; constructor args otherwise. No file source. Both values are sent to `AZURE_AUTHORITY_HOST` (tenant in the URL path, client ID in the form body), which the fake controlled. |
| GCE metadata emulation | none in the client | The emulator decides; env holds only the constant host. |
| Azure managed identity | none in the client (optional user-assigned client ID) | `IDENTITY_ENDPOINT` and `IDENTITY_HEADER` are constant per pod. |
