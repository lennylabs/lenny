# AWS loadgen module

Provisions the tier-12 load-runner pool on AWS:

- Auto Scaling Group of `c6i.2xlarge` instances in the EKS VPC, on private subnets.
- SQS queue the runners pull jobs from.
- IAM role granting the runners SQS receive/delete on the queue plus S3 PUT on the load-reports bucket.
- A metrics-collector instance polling CloudWatch and exposing Prometheus-format metrics.
- VPC PrivateLink endpoint to the EKS cluster's gateway service.

Wave 5 cut: input/output shape and resource scaffolding. Wave 6 wires the real autoscaling configuration, the AMI bake step for the runner image, and the PrivateLink endpoint.

## Inputs

| Name | Type | Description |
|:--|:--|:--|
| `release` | string | The Lenny release name; used as the resource prefix. |
| `vpc_id` | string | The EKS cluster's VPC ID. |
| `private_subnet_ids` | list(string) | Private subnet IDs the ASG places instances in. |
| `instance_type` | string | EC2 instance type. Default `c6i.2xlarge`. |
| `desired_capacity` | number | Initial ASG desired count. Default 2. |
| `max_size` | number | ASG maximum. Default 8. |
| `runner_image_uri` | string | ECR URI of the `lenny-loadrunner` image. |
| `reports_bucket` | string | S3 bucket the runners write per-runner k6 JSON to. |

## Outputs

| Name | Description |
|:--|:--|
| `queue_url` | SQS queue URL the runners pull from. |
| `queue_arn` | SQS queue ARN; used by the loadctl module to grant `SendMessage`. |
| `runner_role_arn` | IAM role the EC2 instances assume. |
| `asg_name` | ASG name; used by `down-loadgen.sh` to scale to zero. |
| `metrics_collector_address` | Private DNS name of the metrics collector instance. |
