# AWS loadctl module

Provisions the tier-12 control plane on AWS:

- ECS Fargate service running the `lenny-loadctl` container image.
- Application Load Balancer with HTTPS listener (TLS via ACM) in front of the service.
- RDS Postgres for run-state persistence.
- IAM role granting the task SQS `SendMessage` on the loadgen queue and S3 PUT on the reports bucket.
- Secrets Manager entry storing the loadctl OIDC client secret.

Wave 6 cut: the resources land here. The container image is built and pushed in the runtime build pipeline.

## Inputs

| Name | Type | Description |
|:--|:--|:--|
| `release` | string | Lenny release name; resource prefix. |
| `vpc_id` | string | EKS VPC. |
| `private_subnet_ids` | list(string) | Subnets for Fargate tasks. |
| `public_subnet_ids` | list(string) | Subnets for the ALB. |
| `loadctl_image_uri` | string | ECR URI of `cmd/lenny-loadctl`. |
| `loadgen_queue_arn` | string | SQS queue ARN from the loadgen module (for IAM scope). |
| `loadgen_queue_url` | string | SQS queue URL from the loadgen module (passed as `--queue-url`). |
| `reports_bucket` | string | S3 bucket for per-run reports. |
| `db_username` / `db_password` | string (sensitive) | RDS admin credentials. |
| `tls_certificate_arn` | string | ACM cert ARN for the ALB. |
| `operator_tokens` | string (sensitive) | Comma-separated operator bearer tokens (loadctl `LENNY_LOADCTL_OPERATOR_TOKENS`). |
| `runner_tokens` | string (sensitive) | Comma-separated runner bearer tokens (loadctl `LENNY_LOADCTL_RUNNER_TOKENS`). |
| `progress_dir` | string | Optional persistent-sink URL (`s3://bucket/prefix` or `file:///path`). |
| `run_duration` | string | Optional per-scenario duration override (e.g. `60s`). |
| `ratelimit_runs_per_min` | number | Optional cap on `POST /api/v1/runs`. 0 disables. |
| `ratelimit_progress_per_sec` | number | Optional cap on `POST /api/v1/progress`. 0 disables. |
| `ratelimit_ack_per_sec` | number | Optional cap on `POST /api/v1/ack`. 0 disables. |

The tokens are stored in Secrets Manager and injected as env vars at task start. The container `command` is built from the flag list — overriding the image default is not necessary.

## Outputs

| Name | Description |
|:--|:--|
| `service_url` | The ALB HTTPS endpoint (https://<dns>). |
| `db_endpoint` | RDS connection endpoint. |
| `task_role_arn` | IAM role the Fargate task assumes. |
