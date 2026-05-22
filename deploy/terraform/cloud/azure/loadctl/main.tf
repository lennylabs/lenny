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
variable "reports_storage_id" { type = string }
variable "db_admin_user" { type = string }
variable "db_admin_password" {
  type      = string
  sensitive = true
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

resource "azurerm_container_app" "loadctl" {
  name                         = local.name_prefix
  container_app_environment_id = azurerm_container_app_environment.loadctl.id
  resource_group_name          = var.resource_group_name
  revision_mode                = "Single"

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.loadctl.id]
  }

  template {
    container {
      name   = "loadctl"
      image  = var.loadctl_image
      cpu    = 0.5
      memory = "1Gi"

      env {
        name  = "LENNY_STORAGE_URL"
        value = "azureblob://${var.reports_storage_id}"
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
