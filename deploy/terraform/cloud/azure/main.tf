# SPDX-License-Identifier: MIT
#
# Azure Terraform skeleton for a Lenny deployment.
#
# Provisions the per-release resources the chart consumes:
#
#   - An Azure Blob Storage container for the §4.5 ArtifactStore.
#   - A Key Vault key for the §4.9 / §13.3 envelope encryption KEK.
#   - A user-assigned managed identity federated to the AKS cluster's
#     Kubernetes service account via Azure Workload Identity, so the
#     gateway pod can call Blob and Key Vault without static
#     credentials.
#   - The outputs the Helm install consumes
#     (artifact_container_url / key_vault_key_id /
#     workload_identity_client_id).

terraform {
  required_version = ">= 1.5.0"
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = ">= 3.80.0"
    }
  }
}

variable "release" {
  description = "The Helm release name. Used as the cloud-resource name prefix."
  type        = string
}

variable "resource_group" {
  description = "The Azure resource group hosting the deployment."
  type        = string
}

variable "location" {
  description = "The Azure region the resources are provisioned into."
  type        = string
}

variable "aks_oidc_issuer_url" {
  description = "The AKS cluster's OIDC issuer URL. Empty disables the workload identity binding."
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

provider "azurerm" {
  features {}
}

data "azurerm_resource_group" "this" {
  name = var.resource_group
}

resource "azurerm_storage_account" "artifacts" {
  name                            = replace("${var.release}artifacts", "-", "")
  resource_group_name             = data.azurerm_resource_group.this.name
  location                        = var.location
  account_tier                    = "Standard"
  account_replication_type        = "ZRS"
  allow_nested_items_to_be_public = false
  blob_properties {
    versioning_enabled = true
  }
}

resource "azurerm_storage_container" "artifacts" {
  name                  = "lenny-artifacts"
  storage_account_id    = azurerm_storage_account.artifacts.id
  container_access_type = "private"
}

resource "azurerm_key_vault" "platform" {
  name                       = "${var.release}-kv"
  resource_group_name        = data.azurerm_resource_group.this.name
  location                   = var.location
  tenant_id                  = data.azurerm_client_config.current.tenant_id
  sku_name                   = "standard"
  purge_protection_enabled   = true
  soft_delete_retention_days = 30
}

data "azurerm_client_config" "current" {}

resource "azurerm_key_vault_key" "platform" {
  name         = "platform"
  key_vault_id = azurerm_key_vault.platform.id
  key_type     = "RSA"
  key_size     = 4096
  key_opts     = ["wrapKey", "unwrapKey", "encrypt", "decrypt", "sign", "verify"]
  rotation_policy {
    automatic {
      time_after_creation = "P90D"  # §4.9.1 90-day rotation
    }
    expire_after         = "P1Y"
    notify_before_expiry = "P30D"
  }
}

resource "azurerm_user_assigned_identity" "gateway" {
  count               = var.aks_oidc_issuer_url == "" ? 0 : 1
  name                = "${var.release}-gateway"
  resource_group_name = data.azurerm_resource_group.this.name
  location            = var.location
}

resource "azurerm_federated_identity_credential" "gateway" {
  count               = var.aks_oidc_issuer_url == "" ? 0 : 1
  name                = "${var.release}-gateway-federated"
  resource_group_name = data.azurerm_resource_group.this.name
  parent_id           = azurerm_user_assigned_identity.gateway[0].id
  audience            = ["api://AzureADTokenExchange"]
  issuer              = var.aks_oidc_issuer_url
  subject             = "system:serviceaccount:${var.namespace}:${var.service_account}"
}

resource "azurerm_role_assignment" "gateway_blob" {
  count                = var.aks_oidc_issuer_url == "" ? 0 : 1
  scope                = azurerm_storage_account.artifacts.id
  role_definition_name = "Storage Blob Data Contributor"
  principal_id         = azurerm_user_assigned_identity.gateway[0].principal_id
}

resource "azurerm_key_vault_access_policy" "gateway_key" {
  count        = var.aks_oidc_issuer_url == "" ? 0 : 1
  key_vault_id = azurerm_key_vault.platform.id
  tenant_id    = data.azurerm_client_config.current.tenant_id
  object_id    = azurerm_user_assigned_identity.gateway[0].principal_id
  key_permissions = ["Get", "Encrypt", "Decrypt", "WrapKey", "UnwrapKey"]
}

output "artifact_container_url" {
  description = "Azure Blob container URL for the §4.5 ArtifactStore."
  value       = "${azurerm_storage_account.artifacts.primary_blob_endpoint}${azurerm_storage_container.artifacts.name}"
}

output "key_vault_key_id" {
  description = "Versioned key identifier for the Key Vault platform key."
  value       = azurerm_key_vault_key.platform.id
}

output "workload_identity_client_id" {
  description = "Client ID of the user-assigned managed identity the gateway pod's federated SA binds to. Empty when var.aks_oidc_issuer_url is unset."
  value       = try(azurerm_user_assigned_identity.gateway[0].client_id, "")
}
