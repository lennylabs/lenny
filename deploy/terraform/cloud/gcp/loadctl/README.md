# GCP loadctl module

Provisions the tier-12 control plane on GCP:

- Cloud Run v2 service running the `lenny-loadctl` container image.
- Cloud SQL Postgres for run-state persistence.
- Cloud Armor policy for the Cloud Run frontend.
- Service account granting Pub/Sub publish on the loadgen topic and GCS object writes on the reports bucket.
- Serverless VPC connector to the GKE cluster's network.

Wave 6 cut: terraform scaffolding. The Cloud Run revision is sized for low concurrency since the WebSocket telemetry hub needs session affinity (one connection pins to one revision).

## Outputs

| Name | Description |
|:--|:--|
| `service_url` | Cloud Run service URL. |
| `db_connection_name` | Cloud SQL connection name. |
| `runner_publisher_sa` | Service account that publishes to the loadgen Pub/Sub topic. |
