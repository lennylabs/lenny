# Azure loadctl module

Provisions the tier-12 control plane on Azure:

- Container Apps environment hosting the `lenny-loadctl` container image.
- Flexible Server Postgres for run-state persistence.
- Managed certificate for the Container Apps ingress.
- User-assigned managed identity granting Service Bus `Sender` on the loadgen queue and Blob Storage `Contributor` on the reports container.

## Inputs

| Name | Type | Description |
|:--|:--|:--|
| `release` | string | Resource prefix. |
| `resource_group_name` | string | Resource group (must match `up.sh`). |
| `location` | string | Azure region. |
| `loadctl_image` | string | Container Registry image for `cmd/lenny-loadctl`. |
| `loadgen_queue_id` | string | Service Bus queue resource ID (for IAM scope). |
| `loadgen_queue_url` | string | Service Bus queue URL; passed as `--queue-url`. |
| `reports_storage_id` | string | Storage container resource ID (for IAM scope). |
| `reports_storage_url` | string | Storage URL `azureblob://<account>/<container>`; passed as `--storage-url`. |
| `db_admin_user` / `db_admin_password` | string (sensitive) | Flexible Server admin credentials. |
| `operator_tokens` | string (sensitive) | Comma-separated operator bearer tokens. |
| `runner_tokens` | string (sensitive) | Comma-separated runner bearer tokens. |
| `progress_dir` | string | Optional persistent-sink URL. |
| `run_duration` | string | Optional per-scenario duration override. |
| `ratelimit_runs_per_min` | number | Optional cap on `POST /api/v1/runs`. |
| `ratelimit_progress_per_sec` | number | Optional cap on `POST /api/v1/progress`. |
| `ratelimit_ack_per_sec` | number | Optional cap on `POST /api/v1/ack`. |

Tokens are stored as Container App secrets and exposed as env vars. The container `command` is built from the flag list.

## Outputs

| Name | Description |
|:--|:--|
| `service_fqdn` | Container Apps ingress hostname. |
| `db_fqdn` | Flexible Server hostname. |
| `identity_id` | User-assigned managed identity. |
