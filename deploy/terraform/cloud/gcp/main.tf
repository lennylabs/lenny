# SPDX-License-Identifier: MIT
#
# GCP Terraform skeleton for a Lenny deployment.
#
# Provisions the per-release resources the chart consumes:
#
#   - A GCS bucket for the §4.5 ArtifactStore.
#   - A Cloud KMS keyring + key for the §4.9 / §13.3 envelope
#     encryption KEK.
#   - A GCP service account with Workload Identity Federation bound
#     to the GKE cluster's Kubernetes service account, so the
#     gateway pod can call GCS and Cloud KMS without static
#     credentials.
#   - The outputs the Helm install consumes
#     (artifact_bucket / kms_key_id / gcp_service_account_email).

terraform {
  required_version = ">= 1.5.0"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0.0"
    }
  }
}

variable "release" {
  description = "The Helm release name. Used as the cloud-resource name prefix."
  type        = string
}

variable "project" {
  description = "The GCP project ID."
  type        = string
}

variable "region" {
  description = "The GCP region for the GCS bucket + KMS keyring."
  type        = string
}

variable "gke_cluster_name" {
  description = "The GKE cluster's name. Empty disables the Workload Identity binding."
  type        = string
  default     = ""
}

variable "gke_cluster_location" {
  description = "The GKE cluster's location (zone or region)."
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

provider "google" {
  project = var.project
  region  = var.region
}

resource "google_storage_bucket" "artifacts" {
  name                        = "${var.release}-artifacts"
  location                    = var.region
  uniform_bucket_level_access = true
  versioning {
    enabled = true
  }
  public_access_prevention = "enforced"
}

resource "google_kms_key_ring" "platform" {
  name     = "${var.release}-platform"
  location = var.region
}

resource "google_kms_crypto_key" "platform" {
  name     = "platform"
  key_ring = google_kms_key_ring.platform.id
  purpose  = "ENCRYPT_DECRYPT"
  rotation_period = "7776000s"  # 90 days, per §4.9.1
  lifecycle {
    prevent_destroy = true
  }
}

resource "google_service_account" "gateway" {
  count        = var.gke_cluster_name == "" ? 0 : 1
  account_id   = "${var.release}-gateway"
  display_name = "Lenny ${var.release} gateway"
}

resource "google_service_account_iam_member" "gateway_wi" {
  count              = var.gke_cluster_name == "" ? 0 : 1
  service_account_id = google_service_account.gateway[0].name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project}.svc.id.goog[${var.namespace}/${var.service_account}]"
}

resource "google_storage_bucket_iam_member" "gateway_bucket" {
  count  = var.gke_cluster_name == "" ? 0 : 1
  bucket = google_storage_bucket.artifacts.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.gateway[0].email}"
}

resource "google_kms_crypto_key_iam_member" "gateway_kms" {
  count         = var.gke_cluster_name == "" ? 0 : 1
  crypto_key_id = google_kms_crypto_key.platform.id
  role          = "roles/cloudkms.cryptoKeyEncrypterDecrypter"
  member        = "serviceAccount:${google_service_account.gateway[0].email}"
}

output "artifact_bucket" {
  description = "GCS bucket name for the §4.5 ArtifactStore."
  value       = google_storage_bucket.artifacts.name
}

output "kms_key_id" {
  description = "Full resource name of the platform KMS key."
  value       = google_kms_crypto_key.platform.id
}

output "gcp_service_account_email" {
  description = "The IAM service account email the gateway impersonates via Workload Identity. Empty when var.gke_cluster_name is unset."
  value       = try(google_service_account.gateway[0].email, "")
}
