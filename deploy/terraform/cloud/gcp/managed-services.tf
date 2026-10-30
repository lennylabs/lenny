# SPDX-License-Identifier: MIT
#
# GCP managed Postgres + Redis modules. Mirrors
# deploy/terraform/cloud/aws/managed-services.tf (RDS + ElastiCache)
# and deploy/terraform/cloud/azure/managed-services.tf (Flexible
# Server + Cache for Redis). Tier-7 cloud-load on GCP provisions both
# so the gateway has a production-shaped data plane that does not
# consume GKE node CPU.
#
# Network topology: the simplest reachability for an
# operator-supplied GKE cluster is Cloud SQL with a public IP and
# Cloud SQL Auth Proxy in front, but for parity with the AWS / Azure
# tier-7 paths this module uses Private Service Access — the VPC the
# GKE cluster lives in must be reachable from a peering connection
# to Google's services range. The variable network is the VPC name
# the operator's cluster uses; the module assumes that VPC already
# has a Private Service Access allocation (configured at cluster
# creation time, outside this module).
#
# Memorystore for Redis is a single-region Standard-tier instance
# with AUTH enabled and TLS-only.

variable "create_cloud_sql" {
  description = "When true, provision a Cloud SQL for PostgreSQL instance. Gated so the default tier-6 path stays cluster-only."
  type        = bool
  default     = false
}

variable "cloud_sql_tier" {
  description = "Cloud SQL instance tier. db-custom-1-3840 is the cheapest viable size for tier-7 load."
  type        = string
  default     = "db-custom-2-7680"
}

variable "cloud_sql_database_version" {
  description = "Cloud SQL Postgres version."
  type        = string
  default     = "POSTGRES_16"
}

variable "cloud_sql_disk_size_gb" {
  description = "Cloud SQL data-disk size in GiB."
  type        = number
  default     = 20
}

variable "create_memorystore" {
  description = "When true, provision a Memorystore for Redis instance."
  type        = bool
  default     = false
}

variable "memorystore_tier" {
  description = "Memorystore tier. STANDARD_HA is the minimum tier with replication + AUTH + TLS."
  type        = string
  default     = "STANDARD_HA"
}

variable "memorystore_memory_size_gb" {
  description = "Memorystore memory size in GiB. The tier-7 envelope fits in 1 GiB."
  type        = number
  default     = 1
}

variable "memorystore_redis_version" {
  description = "Memorystore Redis version. REDIS_7_2 matches the AWS / Azure 7.x baseline."
  type        = string
  default     = "REDIS_7_2"
}

variable "managed_datastores_network" {
  description = "VPC network self-link the managed services attach to via Private Service Access. The default empty string is invalid when create_* is on — the operator must supply the cluster's VPC."
  type        = string
  default     = ""
}

variable "managed_datastores_authorized_networks" {
  description = "List of CIDR ranges authorized to reach Cloud SQL via its public IP, when private_network is not in use. Empty disables the public path."
  type        = list(string)
  default     = []
}

resource "random_password" "cloud_sql" {
  count   = var.create_cloud_sql ? 1 : 0
  length  = 32
  special = false
}

resource "google_sql_database_instance" "this" {
  count               = var.create_cloud_sql ? 1 : 0
  name                = "${var.release}-pg"
  database_version    = var.cloud_sql_database_version
  region              = var.region
  deletion_protection = false

  settings {
    tier              = var.cloud_sql_tier
    availability_type = "REGIONAL"
    disk_size         = var.cloud_sql_disk_size_gb
    disk_type         = "PD_SSD"

    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = true
    }

    ip_configuration {
      ipv4_enabled                                  = length(var.managed_datastores_authorized_networks) > 0
      private_network                               = var.managed_datastores_network != "" ? var.managed_datastores_network : null
      enable_private_path_for_google_cloud_services = var.managed_datastores_network != ""
      # Cloud SQL enforces TLS only when require_ssl is set; the
      # gateway DSN sets sslmode=require so the connection is
      # rejected otherwise.
      ssl_mode = "ENCRYPTED_ONLY"

      dynamic "authorized_networks" {
        for_each = var.managed_datastores_authorized_networks
        content {
          name  = "caller-${authorized_networks.key}"
          value = authorized_networks.value
        }
      }
    }
  }
}

resource "google_sql_database" "lenny" {
  count    = var.create_cloud_sql ? 1 : 0
  name     = "lenny"
  instance = google_sql_database_instance.this[0].name
}

resource "google_sql_user" "lenny" {
  count    = var.create_cloud_sql ? 1 : 0
  name     = "lenny"
  instance = google_sql_database_instance.this[0].name
  password = random_password.cloud_sql[0].result
}

# Persist the Cloud SQL admin credentials in Secret Manager so the
# cloud script can fetch them via `gcloud secrets versions access`.
resource "google_secret_manager_secret" "cloud_sql_admin" {
  count     = var.create_cloud_sql ? 1 : 0
  secret_id = "${var.release}-postgres-admin"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "cloud_sql_admin" {
  count   = var.create_cloud_sql ? 1 : 0
  secret  = google_secret_manager_secret.cloud_sql_admin[0].id
  secret_data = jsonencode({
    username = "lenny"
    password = random_password.cloud_sql[0].result
    host     = google_sql_database_instance.this[0].public_ip_address != "" ? google_sql_database_instance.this[0].public_ip_address : google_sql_database_instance.this[0].private_ip_address
    port     = 5432
    database = "lenny"
  })
}

resource "google_redis_instance" "this" {
  count              = var.create_memorystore ? 1 : 0
  name               = "${var.release}-redis"
  tier               = var.memorystore_tier
  memory_size_gb     = var.memorystore_memory_size_gb
  region             = var.region
  redis_version      = var.memorystore_redis_version
  auth_enabled       = true
  transit_encryption_mode = "SERVER_AUTHENTICATION"
  authorized_network = var.managed_datastores_network != "" ? var.managed_datastores_network : null
  connect_mode       = var.managed_datastores_network != "" ? "PRIVATE_SERVICE_ACCESS" : "DIRECT_PEERING"

  redis_configs = {
    # §11.2.1 billing-stream eviction-policy invariant: allkeys-lru
    # would drop billing events under memory pressure.
    "maxmemory-policy" = "noeviction"
  }
}

# Persist the Redis connection details in Secret Manager.
resource "google_secret_manager_secret" "memorystore_auth" {
  count     = var.create_memorystore ? 1 : 0
  secret_id = "${var.release}-redis-auth"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "memorystore_auth" {
  count  = var.create_memorystore ? 1 : 0
  secret = google_secret_manager_secret.memorystore_auth[0].id
  secret_data = jsonencode({
    host = google_redis_instance.this[0].host
    port = google_redis_instance.this[0].port
    auth = google_redis_instance.this[0].auth_string
  })
}

# Outputs the tier-6 / tier-7 driver scripts read into env vars.
output "cloud_sql_connection_name" {
  description = "Cloud SQL connection name (project:region:instance). Empty when create_cloud_sql=false."
  value       = try(google_sql_database_instance.this[0].connection_name, "")
}

output "cloud_sql_public_ip" {
  description = "Cloud SQL public IP address. Empty when ipv4_enabled=false."
  value       = try(google_sql_database_instance.this[0].public_ip_address, "")
}

output "cloud_sql_private_ip" {
  description = "Cloud SQL private IP address (via PSA). Empty when private_network is unset."
  value       = try(google_sql_database_instance.this[0].private_ip_address, "")
}

output "cloud_sql_admin_secret_name" {
  description = "Secret Manager secret holding the JSON {username, password, host, port, database} for the Cloud SQL master account."
  value       = try(google_secret_manager_secret.cloud_sql_admin[0].secret_id, "")
}

output "cloud_sql_database_name" {
  description = "Database name."
  value       = try(google_sql_database.lenny[0].name, "")
}

output "memorystore_instance_id" {
  description = "Memorystore instance ID."
  value       = try(google_redis_instance.this[0].name, "")
}

output "memorystore_host" {
  description = "Memorystore host (IP)."
  value       = try(google_redis_instance.this[0].host, "")
}

output "memorystore_port" {
  description = "Memorystore port (6379)."
  value       = try(google_redis_instance.this[0].port, 0)
}

output "memorystore_auth_secret_name" {
  description = "Secret Manager secret holding the connection details {host, port, auth}; the auth field is populated post-apply by the cloud script."
  value       = try(google_secret_manager_secret.memorystore_auth[0].secret_id, "")
}
