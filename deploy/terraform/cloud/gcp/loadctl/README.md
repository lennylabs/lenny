# GCP loadctl module

Provisions the tier-12 control plane on GCP:

- Cloud Run v2 service running the `lenny-loadctl` container image.
- Cloud SQL Postgres for run-state persistence.
- Cloud Armor policy for the Cloud Run frontend.
- Service account granting Pub/Sub publish on the loadgen topic and GCS object writes on the reports bucket.
- Serverless VPC connector to the GKE cluster's network.

## Inputs

| Name | Type | Description |
|:--|:--|:--|
| `release` | string | Resource prefix. |
| `project_id` | string | GCP project. |
| `region` | string | Cloud Run + Cloud SQL region. |
| `loadctl_image` | string | Artifact Registry image for `cmd/lenny-loadctl`. |
| `loadgen_topic` | string | Pub/Sub topic the runner pool subscribes to; passed as `--queue-url`. |
| `reports_bucket` | string | GCS bucket name for per-run reports. |
| `vpc_connector_id` | string | Serverless VPC Access connector. |
| `db_password` | string (sensitive) | Cloud SQL admin password. |
| `operator_tokens` | string (sensitive) | Comma-separated operator bearer tokens. |
| `runner_tokens` | string (sensitive) | Comma-separated runner bearer tokens. |
| `progress_dir` | string | Optional persistent-sink URL (`gs://bucket/prefix` or `file:///…`). |
| `run_duration` | string | Optional per-scenario duration override. |
| `ratelimit_runs_per_min` | number | Optional cap on `POST /api/v1/runs`. 0 disables. |
| `ratelimit_progress_per_sec` | number | Optional cap on `POST /api/v1/progress`. 0 disables. |
| `ratelimit_ack_per_sec` | number | Optional cap on `POST /api/v1/ack`. 0 disables. |

The tokens live in Secret Manager and are hydrated as env vars on the Cloud Run revision. The container `args` list is built from the configured flags.

## Outputs

| Name | Description |
|:--|:--|
| `service_url` | Cloud Run service URL. |
| `db_connection_name` | Cloud SQL connection name. |
| `runner_publisher_sa` | Service account that publishes to the loadgen Pub/Sub topic. |
