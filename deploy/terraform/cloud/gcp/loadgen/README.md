# GCP loadgen module

Provisions the tier-12 load-runner pool on GCP:

- Managed Instance Group of `c2-standard-8` instances in the GKE VPC, on private subnets.
- Pub/Sub topic + subscription the runners pull jobs from.
- Service account granting the runners Pub/Sub subscribe on the subscription plus GCS object create on the load-reports bucket.
- A metrics-collector instance polling Cloud Monitoring and exposing Prometheus-format metrics.
- Private Service Connect endpoint to the GKE cluster's gateway service.

Wave 5 cut: input/output shape and resource scaffolding. Wave 6 wires the real autoscaling configuration, the runner image bake step, and the PSC endpoint.

## Inputs

| Name | Type | Description |
|:--|:--|:--|
| `release` | string | Lenny release name; used as the resource prefix. |
| `project_id` | string | GCP project ID. |
| `network` | string | VPC network self-link. |
| `subnetwork` | string | Subnetwork self-link. |
| `region` | string | GCP region. |
| `instance_type` | string | Compute instance type. Default `c2-standard-8`. |
| `target_size` | number | Initial MIG target size. Default 2. |
| `runner_image` | string | Artifact Registry URI of the `lenny-loadrunner` image. |
| `reports_bucket` | string | GCS bucket the runners write per-runner k6 JSON to. |

## Outputs

| Name | Description |
|:--|:--|
| `subscription_name` | Pub/Sub subscription name the runners pull from. |
| `topic_name` | Pub/Sub topic name; used by the loadctl module to publish. |
| `runner_sa_email` | Service account email the instances run as. |
| `mig_name` | MIG name; used by `down-loadgen.sh` to scale to zero. |
