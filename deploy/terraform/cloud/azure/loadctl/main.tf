// SPDX-License-Identifier: MIT
// Azure loadctl module. Wave 6 scaffolding.

terraform {
  required_version = ">= 1.5"
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = ">= 3.80"
    }
  }
}

variable "release" { type = string }
variable "resource_group_name" { type = string }
variable "location" { type = string }
variable "loadctl_image" { type = string }
variable "loadgen_queue_id" { type = string }
variable "loadgen_queue_url" {
  description = "Service Bus queue URL (https://<namespace>.servicebus.windows.net/<queue>) passed to loadctl as --queue-url."
  type        = string
}
variable "reports_storage_id" { type = string }
variable "reports_storage_url" {
  description = "Object-storage base URL the loadctl --storage-url flag receives (azureblob://<account>/<container>)."
  type        = string
}
variable "db_admin_user" { type = string }
variable "db_admin_password" {
  type      = string
  sensitive = true
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
  description = "ProgressSink target. Empty disables persistence."
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
  tags        = merge(var.tags, { "lenny.dev/component" = "loadctl" })
}

resource "azurerm_postgresql_flexible_server" "loadctl" {
  name                = "${local.name_prefix}-db"
  resource_group_name = var.resource_group_name
  location            = var.location
  version             = "16"

  administrator_login    = var.db_admin_user
  administrator_password = var.db_admin_password

  sku_name   = "B_Standard_B1ms"
  storage_mb = 32768

  tags = local.tags
}

resource "azurerm_postgresql_flexible_server_database" "loadctl" {
  name      = "lenny_loadctl"
  server_id = azurerm_postgresql_flexible_server.loadctl.id
  collation = "en_US.utf8"
  charset   = "utf8"
}

resource "azurerm_user_assigned_identity" "loadctl" {
  name                = "${local.name_prefix}-identity"
  resource_group_name = var.resource_group_name
  location            = var.location
  tags                = local.tags
}

resource "azurerm_role_assignment" "sender" {
  scope                = var.loadgen_queue_id
  role_definition_name = "Azure Service Bus Data Sender"
  principal_id         = azurerm_user_assigned_identity.loadctl.principal_id
}

resource "azurerm_role_assignment" "blob" {
  scope                = var.reports_storage_id
  role_definition_name = "Storage Blob Data Contributor"
  principal_id         = azurerm_user_assigned_identity.loadctl.principal_id
}

resource "azurerm_container_app_environment" "loadctl" {
  name                = "${local.name_prefix}-env"
  resource_group_name = var.resource_group_name
  location            = var.location
  tags                = local.tags
}

locals {
  loadctl_args = concat(
    [
      "-listen=:8080",
      "-storage-url=${var.reports_storage_url}",
      "-database-url=postgres://${var.db_admin_user}:${var.db_admin_password}@${azurerm_postgresql_flexible_server.loadctl.fqdn}/lenny_loadctl?sslmode=require",
      "-dispatcher=azure",
      "-queue-url=${var.loadgen_queue_url}",
      "-region=${var.location}",
    ],
    var.progress_dir == "" ? [] : ["-progress-dir=${var.progress_dir}"],
    var.run_duration == "" ? [] : ["-run-duration=${var.run_duration}"],
    var.ratelimit_runs_per_min == 0 ? [] : ["-ratelimit-runs-per-min=${var.ratelimit_runs_per_min}"],
    var.ratelimit_progress_per_sec == 0 ? [] : ["-ratelimit-progress-per-sec=${var.ratelimit_progress_per_sec}"],
    var.ratelimit_ack_per_sec == 0 ? [] : ["-ratelimit-ack-per-sec=${var.ratelimit_ack_per_sec}"],
  )
}

resource "azurerm_container_app" "loadctl" {
  name                         = local.name_prefix
  container_app_environment_id = azurerm_container_app_environment.loadctl.id
  resource_group_name          = var.resource_group_name
  revision_mode                = "Single"

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.loadctl.id]
  }

  secret {
    name  = "operator-tokens"
    value = var.operator_tokens
  }
  secret {
    name  = "runner-tokens"
    value = var.runner_tokens
  }

  template {
    container {
      name    = "loadctl"
      image   = var.loadctl_image
      cpu     = 0.5
      memory  = "1Gi"
      command = local.loadctl_args
      env {
        name        = "LENNY_LOADCTL_OPERATOR_TOKENS"
        secret_name = "operator-tokens"
      }
      env {
        name        = "LENNY_LOADCTL_RUNNER_TOKENS"
        secret_name = "runner-tokens"
      }
    }
  }

  ingress {
    external_enabled = true
    target_port      = 8080
    transport        = "auto"
    traffic_weight {
      latest_revision = true
      percentage      = 100
    }
  }

  tags = local.tags
}

output "service_fqdn" {
  value = azurerm_container_app.loadctl.latest_revision_fqdn
}
output "db_fqdn" {
  value = azurerm_postgresql_flexible_server.loadctl.fqdn
}
output "identity_id" {
  value = azurerm_user_assigned_identity.loadctl.id
}
