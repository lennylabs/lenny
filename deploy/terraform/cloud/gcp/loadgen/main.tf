// SPDX-License-Identifier: MIT
// GCP loadgen module. Provisions the tier-12 load-runner pool.
// Wave 5 cut: scaffolding. Wave 6 fills the user-data, image bake,
// and PSC endpoint.

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
variable "network" { type = string }
variable "subnetwork" { type = string }
variable "region" { type = string }
variable "instance_type" {
  type    = string
  default = "c2-standard-8"
}
variable "target_size" {
  type    = number
  default = 2
}
variable "runner_image" { type = string }
variable "reports_bucket" { type = string }
variable "loadctl_url" {
  description = "Base URL the runner uses for ack/progress/registration callbacks."
  type        = string
}
variable "runner_token" {
  description = "Bearer token the runner sends with every loadctl callback."
  type        = string
  sensitive   = true
}
variable "report_storage_url" {
  description = "Object-storage URL the runner uploads per-scenario k6 summaries to (gs://bucket/prefix)."
  type        = string
  default     = ""
}
variable "tags" {
  type    = map(string)
  default = {}
}

locals {
  name_prefix = "${var.release}-loadgen"
  labels      = merge(var.tags, { "lenny-component" = "loadgen" })
}

resource "google_pubsub_topic" "jobs" {
  project = var.project_id
  name    = "${local.name_prefix}-jobs"
  labels  = local.labels
}

resource "google_pubsub_subscription" "runner" {
  project = var.project_id
  name    = "${local.name_prefix}-runner"
  topic   = google_pubsub_topic.jobs.name
  ack_deadline_seconds = 300
  labels  = local.labels
}

resource "google_service_account" "runner" {
  project      = var.project_id
  account_id   = substr("${local.name_prefix}-runner", 0, 30)
  display_name = "lenny-loadrunner service account"
}

resource "google_pubsub_subscription_iam_member" "runner" {
  project      = var.project_id
  subscription = google_pubsub_subscription.runner.name
  role         = "roles/pubsub.subscriber"
  member       = "serviceAccount:${google_service_account.runner.email}"
}

resource "google_storage_bucket_iam_member" "runner" {
  bucket = var.reports_bucket
  role   = "roles/storage.objectCreator"
  member = "serviceAccount:${google_service_account.runner.email}"
}

resource "google_compute_instance_template" "runner" {
  project      = var.project_id
  name_prefix  = "${local.name_prefix}-"
  machine_type = var.instance_type
  region       = var.region

  disk {
    source_image = "projects/debian-cloud/global/images/family/debian-12"
    auto_delete  = true
    boot         = true
  }

  network_interface {
    network    = var.network
    subnetwork = var.subnetwork
  }

  service_account {
    email  = google_service_account.runner.email
    scopes = ["cloud-platform"]
  }

  # Bootstrap installs Docker, pulls the runner image, and starts
  # the systemd unit that runs lenny-loadrunner against the Pub/Sub
  # subscription.
  metadata_startup_script = <<-EOT
    #!/bin/bash
    set -euxo pipefail
    curl -fsSL https://get.docker.com | sh
    systemctl enable --now docker
    gcloud auth configure-docker ${var.region}-docker.pkg.dev --quiet
    docker pull "${var.runner_image}"
    install -m 0600 /dev/null /etc/lenny-loadrunner.env
    cat > /etc/lenny-loadrunner.env <<EOF
LENNY_LOADRUNNER_TOKEN=${var.runner_token}
EOF
    cat > /etc/systemd/system/lenny-loadrunner.service <<'UNIT'
[Unit]
Description=Lenny loadrunner agent
After=docker.service network-online.target
[Service]
Type=simple
Restart=always
RestartSec=5
EnvironmentFile=/etc/lenny-loadrunner.env
ExecStart=/usr/bin/docker run --rm --network host \
  -e LENNY_LOADRUNNER_TOKEN \
  ${var.runner_image} \
  /usr/local/bin/lenny-loadrunner \
    --dispatcher=gcp \
    --queue-url=${google_pubsub_subscription.runner.id} \
    --region=${var.region} \
    --loadctl-url=${var.loadctl_url} \
    --cloud-label=gcp \
    --capacity=1 \
    --report-storage-url=${var.report_storage_url}
[Install]
WantedBy=multi-user.target
UNIT
    systemctl daemon-reload
    systemctl enable --now lenny-loadrunner.service
  EOT

  labels = local.labels

  lifecycle { create_before_destroy = true }
}

resource "google_compute_region_instance_group_manager" "runner" {
  project            = var.project_id
  region             = var.region
  name               = "${local.name_prefix}-runner"
  base_instance_name = "${local.name_prefix}-runner"
  target_size        = var.target_size

  version {
    instance_template = google_compute_instance_template.runner.id
  }
}

output "subscription_name" {
  value = google_pubsub_subscription.runner.name
}
output "topic_name" {
  value = google_pubsub_topic.jobs.name
}
output "runner_sa_email" {
  value = google_service_account.runner.email
}
output "mig_name" {
  value = google_compute_region_instance_group_manager.runner.name
}
