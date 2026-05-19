# Tier-6 cloud account setup

This directory holds the tier-6 end-to-end suites described in §12.6 of
`TESTING.md`. These suites run against managed Kubernetes on Google Cloud,
AWS, and Azure. This document describes how to create and configure the
cloud accounts the suites require, for both local runs and CI.

## Status

The tier-6 harness is not yet implemented. `scaffolds_test.go` defines one
scaffold per §12.6 suite, and each scaffold calls `t.Skip` with a precise
diagnosis. `scripts/cloud/{gke,eks,aks}/up.sh` and `down.sh` are placeholders
that validate their argument and exit without provisioning a cluster. The
account setup below is a prerequisite for that harness work. Completing it
does not make the suites pass on its own.

The suites read one Lenny-specific environment variable, `LENNY_CLOUD_PROVIDER`.
Every other credential below is consumed through the standard SDK credential
files (`~/.config/gcloud`, `~/.aws`, and `~/.azure`).

## Cluster configurations

Each provider runs two cluster configurations. `scripts/cloud/<provider>/up.sh`
takes the configuration name as its first argument:

- `cloud-small`: 3-node cluster, runc only, no sandbox node pool.
- `cloud-sandbox`: 3-node cluster with the provider's sandbox node pool.

Google Cloud is the canonical provider. Nightly and weekly runs target GKE,
and the pre-release run covers GKE, EKS, and AKS.

## DNS domain (required for `managed_ingress` only)

A registered DNS domain is required for one suite, `managed_ingress`, and no
others.

`managed_ingress` is a pre-release environment check. It stands up an
operator-supplied Ingress and a provider load balancer in front of the
gateway, terminates TLS at the edge, and confirms the gateway is reachable
over HTTPS on managed Kubernetes. It exercises no Lenny-specific certificate
or ingress code, because the Helm chart renders no Ingress. The Lenny-specific
part of this path, the `allow-gateway-ingress` NetworkPolicy (NET-038), is
validated on Kind in tier 5 and needs no cloud and no domain.

A certificate from a public authority requires proof of domain ownership, so
the pre-release run of `managed_ingress` needs a domain. Load balancer
provisioning and routing can be checked without a domain by addressing the
load balancer IP directly with a `Host` header.

The remaining suites, including the nightly critical-path subset
(`gvisor_isolation`, `cloud_csi`, and `cloud_kms`), do not touch DNS. Skip
this section unless you are preparing a pre-release run of `managed_ingress`.

When you prepare that run, register one domain and delegate a subdomain to
each provider so each provider owns an independent zone:

- `gke.acme.com` to Google Cloud DNS.
- `eks.acme.com` to AWS Route 53.
- `aks.acme.com` to Azure DNS.

Register the domain through Cloud Domains, the Route 53 registrar, or any
registrar. Zone delegation takes time to propagate, so complete this step
before the pre-release `managed_ingress` run.

## Local tooling

Install the toolchain from §11 of `TESTING.md`. On macOS with Homebrew:

```bash
brew install --cask google-cloud-sdk
brew install awscli eksctl kubectl helm terraform aws-iam-authenticator
gcloud components install gke-gcloud-auth-plugin
az aks install-cli   # installs kubelogin
```

Install the Azure CLI with `brew install azure-cli`. Verify each tool with
`gcloud version`, `aws --version`, `eksctl version`, `az version`,
`kubectl version --client`, `helm version`, and `terraform version`.

## Google Cloud (GKE)

### Create the account and billing

1. Sign up at `console.cloud.google.com` with a Google account. New accounts
   receive a trial credit.
2. Signup creates a billing account. Record its identifier:
   `gcloud billing accounts list`.
3. An Organization resource is optional. A standalone project is sufficient
   for tier-6 tests.

### Create the project

```bash
gcloud projects create lenny-tier6 --name="Lenny Tier-6 Tests"
gcloud billing projects link lenny-tier6 --billing-account=XXXXXX-XXXXXX-XXXXXX
gcloud config set project lenny-tier6
```

### Enable the required APIs

```bash
gcloud services enable \
  container.googleapis.com compute.googleapis.com sqladmin.googleapis.com \
  cloudkms.googleapis.com secretmanager.googleapis.com dns.googleapis.com \
  storage.googleapis.com cloudtrace.googleapis.com logging.googleapis.com \
  bigquery.googleapis.com artifactregistry.googleapis.com \
  iamcredentials.googleapis.com sts.googleapis.com
```

These APIs back the §12.6 suites: `container` and `compute` for the cluster
and the GKE Sandbox node pool, `sqladmin` for `multi_zone_dr`, `cloudkms` for
`cloud_kms`, `secretmanager` for `cloud_secret_store`, `dns` for `managed_ingress`,
`storage` for `cloud_csi`, `cloudtrace` and `logging` for `cloud_observability`,
`bigquery` for `cloud_billing_export`, and `iamcredentials` and `sts` for CI
federation.

### Choose a region and check quotas

Use `us-central1`. It has multiple zones, supports GKE Sandbox, and supports
Cloud SQL high availability. A new project's default quota covers a 3-node
cluster. Check the `CPUS` and `SSD_TOTAL_GB` quotas for the region in the
IAM and Admin console, and request an increase if a bring-up later reports a
quota error.

### Local authentication

Run the interactive logins yourself. In Claude Code, prefix the command with
`!` so its output is captured in the session.

```bash
gcloud auth login
gcloud auth application-default login
```

The second command writes Application Default Credentials to
`~/.config/gcloud/application_default_credentials.json`. Terraform and the Go
SDK read that file automatically.

### CI authentication with Workload Identity Federation

CI authenticates through OIDC federation rather than long-lived keys.

```bash
PROJ_NUM=$(gcloud projects describe lenny-tier6 --format='value(projectNumber)')

gcloud iam workload-identity-pools create github --location=global \
  --display-name="GitHub Actions"

gcloud iam workload-identity-pools providers create-oidc github-actions \
  --location=global --workload-identity-pool=github \
  --issuer-uri="https://token.actions.githubusercontent.com" \
  --attribute-mapping="google.subject=assertion.sub,attribute.repository=assertion.repository" \
  --attribute-condition="assertion.repository=='acme/lenny'"

gcloud iam service-accounts create lenny-tier6-ci --display-name="Lenny tier-6 CI"

for ROLE in container.admin cloudsql.admin cloudkms.admin dns.admin \
            storage.admin secretmanager.admin iam.serviceAccountAdmin; do
  gcloud projects add-iam-policy-binding lenny-tier6 \
    --member="serviceAccount:lenny-tier6-ci@lenny-tier6.iam.gserviceaccount.com" \
    --role="roles/${ROLE}"
done

gcloud iam service-accounts add-iam-policy-binding \
  lenny-tier6-ci@lenny-tier6.iam.gserviceaccount.com \
  --role=roles/iam.workloadIdentityUser \
  --member="principalSet://iam.googleapis.com/projects/${PROJ_NUM}/locations/global/workloadIdentityPools/github/attribute.repository/acme/lenny"
```

Scope the project roles down once the Terraform under
`deploy/terraform/cloud/gke/` defines the resources it manages.

## Amazon Web Services (EKS)

### Create the account

1. Sign up at `aws.amazon.com` with a root email address and a payment method.
2. Secure the root user immediately. Enable MFA, and stop using root for
   routine work.

### Set up IAM Identity Center

IAM Identity Center provides the `aws sso login` flow used for local runs.

1. Enable IAM Identity Center in the console, in the region you will use for
   the cluster.
2. Create a user for yourself and a permission set. `AdministratorAccess` is
   acceptable for a dedicated test account. Scope it down later.
3. Assign the user and the permission set to the account.

### Choose a region and check quotas

Use `us-west-2` or `us-east-1`. Both have at least three Availability Zones,
which `multi_zone_dr` requires. New accounts have low EC2 limits. In the
Service Quotas console, request an increase for "Running On-Demand Standard
instances" to at least 64 vCPU so the cluster and the sandbox node pool fit.
EKS cluster count needs no increase.

### Local authentication

```bash
aws configure sso
# Enter the IAM Identity Center start URL and region, select the account and
# role, and name the profile, for example "lenny-tier6".
aws sso login --profile lenny-tier6
```

Export `AWS_PROFILE` and `AWS_REGION` in any shell that runs the tests:

```bash
export AWS_PROFILE=lenny-tier6
export AWS_REGION=us-west-2
```

### CI authentication with GitHub OIDC

1. In the IAM console, add an OpenID Connect identity provider with URL
   `https://token.actions.githubusercontent.com` and audience
   `sts.amazonaws.com`. The console retrieves the provider thumbprint.
2. Create an IAM role whose trust policy restricts assumption to the
   repository:

   ```json
   {
     "Effect": "Allow",
     "Principal": {
       "Federated": "arn:aws:iam::<account-id>:oidc-provider/token.actions.githubusercontent.com"
     },
     "Action": "sts:AssumeRoleWithWebIdentity",
     "Condition": {
       "StringEquals": {
         "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
       },
       "StringLike": {
         "token.actions.githubusercontent.com:sub": "repo:acme/lenny:*"
       }
     }
   }
   ```

3. Attach permissions for EKS, EC2, RDS, AWS KMS, Route 53, S3, and Secrets
   Manager. Scope the policy down once the Terraform under
   `deploy/terraform/cloud/eks/` defines the resources it manages.

## Microsoft Azure (AKS)

### Create the account

1. Sign up at `azure.microsoft.com` with a Microsoft account and a payment
   method. New accounts receive a trial credit.
2. Signup creates a Microsoft Entra tenant and a subscription. Record the
   subscription identifier: `az account show --query id -o tsv`.

### Create the resource group and register providers

Azure requires each resource provider to be registered on the subscription
before use. This is the Azure equivalent of enabling APIs.

```bash
az login
az account set --subscription "<subscription-id>"
az group create --name lenny-tier6 --location eastus2

for NS in Microsoft.ContainerService Microsoft.KeyVault \
          Microsoft.DBforPostgreSQL Microsoft.Storage Microsoft.Network \
          Microsoft.OperationalInsights Microsoft.Insights; do
  az provider register --namespace "$NS"
done
```

These providers back the §12.6 suites: `ContainerService` for the cluster and
the Kata or Confidential Containers node pool, `DBforPostgreSQL` for
`multi_zone_dr`, `KeyVault` for `cloud_kms` and `cloud_secret_store`,
`Network` for `managed_ingress`, `Storage` for `cloud_csi`,
and `OperationalInsights` and `Insights` for `cloud_observability`.

### Choose a region and check quotas

Use `eastus2` or `westus3`. Both have availability zones, which `multi_zone_dr`
requires. Check the regional vCPU quota for the chosen VM family in the
Quotas console, and request an increase if a bring-up later reports a quota
error.

### Local authentication

```bash
az login
az account set --subscription "<subscription-id>"
```

`az login` writes credentials to `~/.azure`. Terraform and the Go SDK read
that directory automatically. Export the subscription so it is unambiguous:

```bash
export AZURE_SUBSCRIPTION_ID="<subscription-id>"
```

### CI authentication with federated credentials

CI authenticates through a Microsoft Entra application with a federated
credential, rather than a client secret.

```bash
az ad app create --display-name "lenny-tier6-ci"
APP_ID=$(az ad app list --display-name "lenny-tier6-ci" --query '[0].appId' -o tsv)
az ad sp create --id "$APP_ID"

az ad app federated-credential create --id "$APP_ID" --parameters '{
  "name": "github-lenny-main",
  "issuer": "https://token.actions.githubusercontent.com",
  "subject": "repo:acme/lenny:ref:refs/heads/main",
  "audiences": ["api://AzureADTokenExchange"]
}'

az role assignment create --assignee "$APP_ID" --role Contributor \
  --scope "/subscriptions/<subscription-id>/resourceGroups/lenny-tier6"
```

Add a second federated credential with a `pull_request` subject if CI runs
tier-6 on pull requests. Replace the `Contributor` role with a scoped custom
role once the Terraform under `deploy/terraform/cloud/aks/` defines the
resources it manages.

## Environment variables

| Variable | Consumed by | Value |
|:--|:--|:--|
| `LENNY_CLOUD_PROVIDER` | Tier-6 test code | `gke`, `eks`, or `aks` |
| `AWS_PROFILE` | AWS SDK and CLI | The SSO profile name, for example `lenny-tier6` |
| `AWS_REGION` | AWS SDK and CLI | The cluster region, for example `us-west-2` |
| `AZURE_SUBSCRIPTION_ID` | Azure SDK and CLI | The subscription identifier |

Google Cloud needs no environment variable for local runs. The project comes
from `gcloud config` and the credentials come from Application Default
Credentials. Set `CLOUDSDK_CORE_PROJECT` only to override the configured
project.

## Cost management

Managed clusters bill continuously while running. An EKS control plane bills
per hour with no free tier. GKE, Cloud SQL high availability, RDS Multi-AZ,
and Azure Database for PostgreSQL with high availability all bill while
provisioned. Run `scripts/cloud/<provider>/down.sh` after every test session,
and do not leave a cluster running between sessions. CI manages cluster
lifecycle for nightly and pre-release runs.

## Verifying the setup

Run the scaffolds with the cloud build tag once an account is configured:

```bash
LENNY_CLOUD_PROVIDER=gke go test -tags e2e_cloud ./tests/tier6_e2e_cloud/... -v
```

Until the harness ships, every suite reports a skip with a diagnosis that
names the missing component. A skip confirms the build tag and the
`LENNY_CLOUD_PROVIDER` gate are wired correctly.
