# Per-provider Terraform for cloud deployments

The Lenny chart treats Postgres, Redis, MinIO, KMS, and the
ingress / IAM bindings as bring-your-own external services. This
directory ships the per-provider Terraform skeletons an operator
uses to provision those services before invoking
`helm install lennylabs/lenny`.

Each `cloud/<provider>/` subdirectory is a self-contained root
module the operator runs:

```bash
cd deploy/terraform/cloud/aws
terraform init
terraform apply -var "release=lenny-prod" -var "region=us-east-1"
```

The module emits the values the Helm install needs (Postgres DSN,
Redis endpoint, MinIO bucket, KMS key ARN, OIDC issuer URL) as
Terraform outputs. The release pipeline reads those outputs and
renders the corresponding `values.yaml` overrides.

## Provider matrix

| Provider | KMS                       | Storage                 | Identity binding              | Status        |
|:---------|:--------------------------|:------------------------|:------------------------------|:--------------|
| AWS      | AWS KMS                   | S3 (bucket per release) | IRSA (IAM role for SA)        | skeleton ships |
| GCP      | Cloud KMS                 | GCS                     | Workload Identity Federation  | skeleton ships |
| Azure    | Key Vault                 | Azure Blob              | Workload Identity             | skeleton ships |

The skeleton subdirectories carry the resource scaffolding (KMS key
+ alias, object-storage bucket, IAM role, OIDC binding) and the
expected output set. They do **not** include cloud-specific
implementation details like VPC layouts or networking; those vary
per operator and are the operator's responsibility.

## v1 scope

The v1 Lenny chart consumes only the Postgres DSN, Redis URL, and
MinIO endpoint as connection-string env. The full cloud KMS
provisioning that Tier-6 e2e exercises (the per-tenant KMS key
allocator on workspaceTier T4 promotion) needs the cloud KMS
provider in `pkg/kms/<provider>/` — also v2 follow-on. The
Terraform here lays the resource graph that those Go adapters
target.
