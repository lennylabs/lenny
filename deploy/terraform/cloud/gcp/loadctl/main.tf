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
variable "loadgen_topic" {
  description = "Pub/Sub topic the runner pool subscribes to; loadctl publishes Jobs into it."
  type        = string
}
variable "reports_bucket" { type = string }
variable "vpc_connector_id" { type = string }
variable "db_password" {
  description = "Cloud SQL admin password for the loadctl database."
  type        = string
  sensitive   = true
}
variable "operator_tokens" {
  description = "Comma-separated operator bearer tokens that protect the run-control surface."
  type        = string
  sensitive   = true
}
variable "runner_tokens" {
  description = "Comma-separated runner bearer tokens that protect /api/v1/ack, /progress, /runners/*."
  type        = string
  sensitive   = true
}
variable "progress_dir" {
  description = "ProgressSink target. Empty disables persistence; gs://bucket/prefix writes one JSONL per run."
  type        = string
  default     = ""
}
variable "run_duration" {
  description = "Per-scenario duration stamped onto every Job. Empty selects the 60s default."
  type        = string
  default     = ""
}
variable "ratelimit_runs_per_min" {
  description = "POST /api/v1/runs per-source cap. 0 disables."
  type        = number
  default     = 0
}
variable "ratelimit_progress_per_sec" {
  description = "POST /api/v1/progress per-source cap. 0 disables."
  type        = number
  default     = 0
}
variable "ratelimit_ack_per_sec" {
  description = "POST /api/v1/ack per-source cap. 0 disables."
  type        = number
  default     = 0
}
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

resource "google_sql_user" "loadctl" {
  project  = var.project_id
  name     = "lenny"
  instance = google_sql_database_instance.loadctl.name
  password = var.db_password
}

# Operator + runner bearer tokens live in Secret Manager so Cloud
# Run can hydrate them as env vars at task start.
resource "google_secret_manager_secret" "operator_tokens" {
  project   = var.project_id
  secret_id = "${local.name_prefix}-operator-tokens"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "operator_tokens" {
  secret      = google_secret_manager_secret.operator_tokens.id
  secret_data = var.operator_tokens
}

resource "google_secret_manager_secret" "runner_tokens" {
  project   = var.project_id
  secret_id = "${local.name_prefix}-runner-tokens"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "runner_tokens" {
  secret      = google_secret_manager_secret.runner_tokens.id
  secret_data = var.runner_tokens
}

resource "google_secret_manager_secret_iam_member" "operator_tokens" {
  project   = var.project_id
  secret_id = google_secret_manager_secret.operator_tokens.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.loadctl.email}"
}

resource "google_secret_manager_secret_iam_member" "runner_tokens" {
  project   = var.project_id
  secret_id = google_secret_manager_secret.runner_tokens.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.loadctl.email}"
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

locals {
  loadctl_args = concat(
    [
      "-listen=:8080",
      "-storage-url=gs://${var.reports_bucket}",
      "-database-url=postgres://lenny:${var.db_password}@/lenny_loadctl?host=/cloudsql/${google_sql_database_instance.loadctl.connection_name}&sslmode=disable",
      "-dispatcher=gcp",
      "-queue-url=${var.loadgen_topic}",
      "-region=${var.region}",
    ],
    var.progress_dir == "" ? [] : ["-progress-dir=${var.progress_dir}"],
    var.run_duration == "" ? [] : ["-run-duration=${var.run_duration}"],
    var.ratelimit_runs_per_min == 0 ? [] : ["-ratelimit-runs-per-min=${var.ratelimit_runs_per_min}"],
    var.ratelimit_progress_per_sec == 0 ? [] : ["-ratelimit-progress-per-sec=${var.ratelimit_progress_per_sec}"],
    var.ratelimit_ack_per_sec == 0 ? [] : ["-ratelimit-ack-per-sec=${var.ratelimit_ack_per_sec}"],
  )
}

resource "google_cloud_run_v2_service" "loadctl" {
  project  = var.project_id
  location = var.region
  name     = local.name_prefix

  template {
    service_account = google_service_account.loadctl.email
    containers {
      image = var.loadctl_image
      args  = local.loadctl_args
      ports { container_port = 8080 }
      env {
        name = "LENNY_LOADCTL_OPERATOR_TOKENS"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.operator_tokens.secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "LENNY_LOADCTL_RUNNER_TOKENS"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.runner_tokens.secret_id
            version = "latest"
          }
        }
      }
      volume_mounts {
        name       = "cloudsql"
        mount_path = "/cloudsql"
      }
    }
    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [google_sql_database_instance.loadctl.connection_name]
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
