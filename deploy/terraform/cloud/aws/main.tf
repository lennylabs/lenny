# SPDX-License-Identifier: MIT
#
# AWS Terraform skeleton for a Lenny deployment.
#
# Provisions the per-release resources the chart consumes:
#
#   - An S3 bucket for the §4.5 ArtifactStore (workspace blobs,
#     checkpoint manifests).
#   - An AWS KMS key + alias for the §4.9 / §13.3 envelope encryption
#     KEK that the Token Service signer wraps under.
#   - An IAM role with an OIDC trust policy bound to the EKS cluster's
#     service-account audience (IRSA), so the gateway pod's Kubernetes
#     SA can call S3 and KMS without static AWS credentials.
#   - The outputs the Helm install consumes
#     (artifact_bucket / kms_key_arn / iam_role_arn).
#
# The skeleton intentionally omits the cluster / VPC / network layer:
# those vary per operator and the cluster (EKS, kops, kOps-on-fargate,
# etc.) is the operator's choice. Wire the outputs into the chart by
# passing them as `--set` flags or by rendering a values file.

terraform {
  required_version = ">= 1.5.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = ">= 4.0.0"
    }
  }
}

variable "release" {
  description = "The Helm release name. Used as the cloud-resource name prefix."
  type        = string
}

variable "region" {
  description = "The AWS region to provision into."
  type        = string
}

variable "eks_cluster_oidc_provider_arn" {
  description = "The EKS cluster's OIDC provider ARN. Empty disables the IRSA role; the operator wires static credentials instead."
  type        = string
  default     = ""
}

variable "eks_cluster_oidc_issuer" {
  description = "The EKS cluster's OIDC issuer URL (without the https:// prefix). Used by the IRSA trust policy."
  type        = string
  default     = ""
}

variable "namespace" {
  description = "The Kubernetes namespace the Lenny chart is released into."
  type        = string
  default     = "lenny-system"
}

variable "service_account" {
  description = "The Kubernetes service account name the gateway runs as."
  type        = string
  default     = "lenny-gateway"
}

provider "aws" {
  region = var.region
}

# §4.5 ArtifactStore bucket. SSE-S3 (AES256) bucket-level encryption
# default; the gateway can switch to SSE-KMS per object via the
# pkg/blobstore/miniostore SSEKeyResolver hook against the per-tenant
# KMS keys this module also provisions.
resource "aws_s3_bucket" "artifacts" {
  bucket = "${var.release}-artifacts"
  # The tier-6 e2e_cloud suite tears the cluster down at end of run.
  # force_destroy lets `terraform destroy` purge any objects + object
  # versions still in the bucket; without it a leftover test object
  # blocks DeleteBucket and the destroy fails at the very end.
  force_destroy = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "artifacts" {
  bucket = aws_s3_bucket.artifacts.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_versioning" "artifacts" {
  bucket = aws_s3_bucket.artifacts.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_public_access_block" "artifacts" {
  bucket                  = aws_s3_bucket.artifacts.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# §4.9 KMS KEK for the Token Service signer + §12.5 SSE-KMS resolver.
# The alias is the stable handle the chart references through
# kms.key_arn output.
resource "aws_kms_key" "platform" {
  description             = "Lenny ${var.release} platform KEK"
  deletion_window_in_days = 30
  enable_key_rotation     = true
}

resource "aws_kms_alias" "platform" {
  name          = "alias/lenny/${var.release}/platform"
  target_key_id = aws_kms_key.platform.key_id
}

# §13 IAM role: IRSA binding to the gateway service account.
data "aws_iam_policy_document" "gateway_trust" {
  count = var.eks_cluster_oidc_provider_arn == "" ? 0 : 1
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    effect  = "Allow"
    principals {
      type        = "Federated"
      identifiers = [var.eks_cluster_oidc_provider_arn]
    }
    condition {
      test     = "StringEquals"
      variable = "${var.eks_cluster_oidc_issuer}:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "${var.eks_cluster_oidc_issuer}:sub"
      values   = ["system:serviceaccount:${var.namespace}:${var.service_account}"]
    }
  }
}

resource "aws_iam_role" "gateway" {
  count              = var.eks_cluster_oidc_provider_arn == "" ? 0 : 1
  name               = "${var.release}-gateway"
  assume_role_policy = data.aws_iam_policy_document.gateway_trust[0].json
}

data "aws_iam_policy_document" "gateway_permissions" {
  count = var.eks_cluster_oidc_provider_arn == "" ? 0 : 1
  statement {
    actions   = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:ListBucket"]
    resources = [aws_s3_bucket.artifacts.arn, "${aws_s3_bucket.artifacts.arn}/*"]
  }
  statement {
    actions   = ["kms:Encrypt", "kms:Decrypt", "kms:GenerateDataKey", "kms:DescribeKey"]
    resources = [aws_kms_key.platform.arn]
  }
}

resource "aws_iam_role_policy" "gateway" {
  count  = var.eks_cluster_oidc_provider_arn == "" ? 0 : 1
  name   = "${var.release}-gateway"
  role   = aws_iam_role.gateway[0].id
  policy = data.aws_iam_policy_document.gateway_permissions[0].json
}

output "artifact_bucket" {
  description = "S3 bucket name for the §4.5 ArtifactStore."
  value       = aws_s3_bucket.artifacts.bucket
}

output "artifact_bucket_arn" {
  description = "S3 bucket ARN."
  value       = aws_s3_bucket.artifacts.arn
}

output "kms_key_arn" {
  description = "ARN of the platform KMS KEK the Token Service wraps under."
  value       = aws_kms_key.platform.arn
}

output "kms_key_alias" {
  description = "Stable alias for the KMS KEK."
  value       = aws_kms_alias.platform.name
}

output "iam_role_arn" {
  description = "IRSA role ARN. Empty when var.eks_cluster_oidc_provider_arn is unset."
  value       = try(aws_iam_role.gateway[0].arn, "")
}
