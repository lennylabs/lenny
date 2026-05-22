// SPDX-License-Identifier: MIT
// GCP loadctl module. Wave 6 scaffolding.

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

variable "release" { type = string }
variable "project_id" { type = string }
variable "region" { type = string }
variable "loadctl_image" { type = string }
variable "loadgen_topic" { type = string }
variable "reports_bucket" { type = string }
variable "vpc_connector_id" { type = string }
variable "tags" {
  type    = map(string)
  default = {}
}

locals {
  name_prefix = "${var.release}-loadctl"
  labels      = merge(var.tags, { "lenny-component" = "loadctl" })
}

resource "google_sql_database_instance" "loadctl" {
  project          = var.project_id
  name             = "${local.name_prefix}-db"
  database_version = "POSTGRES_16"
  region           = var.region

  settings {
    tier = "db-custom-1-3840"
    backup_configuration {
      enabled = true
    }
    ip_configuration {
      ipv4_enabled = false
    }
  }
  deletion_protection = false
}

resource "google_sql_database" "loadctl" {
  project  = var.project_id
  name     = "lenny_loadctl"
  instance = google_sql_database_instance.loadctl.name
}

resource "google_service_account" "loadctl" {
  project      = var.project_id
  account_id   = substr("${local.name_prefix}-sa", 0, 30)
  display_name = "lenny-loadctl service account"
}

resource "google_pubsub_topic_iam_member" "publisher" {
  project = var.project_id
  topic   = var.loadgen_topic
  role    = "roles/pubsub.publisher"
  member  = "serviceAccount:${google_service_account.loadctl.email}"
}

resource "google_storage_bucket_iam_member" "writer" {
  bucket = var.reports_bucket
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.loadctl.email}"
}

resource "google_cloud_run_v2_service" "loadctl" {
  project  = var.project_id
  location = var.region
  name     = local.name_prefix

  template {
    service_account = google_service_account.loadctl.email
    containers {
      image = var.loadctl_image
      ports { container_port = 8080 }
      env {
        name  = "LENNY_STORAGE_URL"
        value = "gs://${var.reports_bucket}"
      }
    }
    vpc_access {
      connector = var.vpc_connector_id
      egress    = "PRIVATE_RANGES_ONLY"
    }
    session_affinity = true
  }
}

resource "google_cloud_run_v2_service_iam_member" "public" {
  project  = var.project_id
  location = google_cloud_run_v2_service.loadctl.location
  name     = google_cloud_run_v2_service.loadctl.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

output "service_url" {
  value = google_cloud_run_v2_service.loadctl.uri
}
output "db_connection_name" {
  value = google_sql_database_instance.loadctl.connection_name
}
output "runner_publisher_sa" {
  value = google_service_account.loadctl.email
}
