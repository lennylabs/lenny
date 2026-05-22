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
| `loadgen_queue_arn` | string | SQS queue ARN from the loadgen module. |
| `reports_bucket` | string | S3 bucket for per-run reports. |
| `db_username` / `db_password` | string | RDS admin credentials. |
| `tls_certificate_arn` | string | ACM cert ARN for the ALB. |

## Outputs

| Name | Description |
|:--|:--|
| `service_url` | The ALB HTTPS endpoint (https://<dns>). |
| `db_endpoint` | RDS connection endpoint. |
| `task_role_arn` | IAM role the Fargate task assumes. |
