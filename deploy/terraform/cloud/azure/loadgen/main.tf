// SPDX-License-Identifier: MIT
// Azure loadgen module. Provisions the tier-12 load-runner pool.
// Wave 5 cut: scaffolding. Wave 6 fills the custom-data, image bake,
// and Private Endpoint.

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
variable "subnet_id" { type = string }
variable "instance_type" {
  type    = string
  default = "Standard_F8s_v2"
}
variable "capacity" {
  type    = number
  default = 2
}
variable "runner_image_id" { type = string }
variable "reports_container" { type = string }
variable "storage_account_name" { type = string }
variable "tags" {
  type    = map(string)
  default = {}
}

locals {
  name_prefix = "${var.release}-loadgen"
  tags        = merge(var.tags, { "lenny.dev/component" = "loadgen" })
}

resource "azurerm_servicebus_namespace" "ns" {
  name                = "${local.name_prefix}-sb"
  location            = var.location
  resource_group_name = var.resource_group_name
  sku                 = "Standard"
  tags                = local.tags
}

resource "azurerm_servicebus_queue" "jobs" {
  name                  = "${local.name_prefix}-jobs"
  namespace_id          = azurerm_servicebus_namespace.ns.id
  lock_duration         = "PT5M"
  default_message_ttl   = "P1D"
  dead_lettering_on_message_expiration = true
}

resource "azurerm_user_assigned_identity" "runner" {
  name                = "${local.name_prefix}-runner"
  resource_group_name = var.resource_group_name
  location            = var.location
  tags                = local.tags
}

resource "azurerm_role_assignment" "runner_sb" {
  scope                = azurerm_servicebus_queue.jobs.id
  role_definition_name = "Azure Service Bus Data Receiver"
  principal_id         = azurerm_user_assigned_identity.runner.principal_id
}

resource "azurerm_role_assignment" "runner_blob" {
  scope                = "/subscriptions/${data.azurerm_client_config.current.subscription_id}/resourceGroups/${var.resource_group_name}/providers/Microsoft.Storage/storageAccounts/${var.storage_account_name}/blobServices/default/containers/${var.reports_container}"
  role_definition_name = "Storage Blob Data Contributor"
  principal_id         = azurerm_user_assigned_identity.runner.principal_id
}

data "azurerm_client_config" "current" {}

resource "azurerm_linux_virtual_machine_scale_set" "runner" {
  name                = "${local.name_prefix}-runner"
  resource_group_name = var.resource_group_name
  location            = var.location
  sku                 = var.instance_type
  instances           = var.capacity

  source_image_id = var.runner_image_id
  admin_username  = "lenny"

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.runner.id]
  }

  network_interface {
    name    = "primary"
    primary = true
    ip_configuration {
      name      = "ipcfg"
      primary   = true
      subnet_id = var.subnet_id
    }
  }

  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "Standard_LRS"
  }

  admin_ssh_key {
    username   = "lenny"
    public_key = "ssh-rsa AAAA-loadgen-placeholder-wave6"
  }

  custom_data = base64encode(<<-EOT
    #!/bin/bash
    set -euxo pipefail
    curl -fsSL https://get.docker.com | sh
    systemctl enable --now docker
    az login --identity
    az acr login --name "${split(".", var.runner_image_id)[0]}" || true
    docker pull "${var.runner_image_id}"
    cat > /etc/systemd/system/lenny-loadrunner.service <<'UNIT'
[Unit]
Description=Lenny loadrunner agent
After=docker.service network-online.target
[Service]
Type=simple
Restart=always
RestartSec=5
ExecStart=/usr/bin/docker run --rm --network host \
  ${var.runner_image_id} \
  /usr/local/bin/lenny-loadrunner \
    --dispatcher=azure \
    --queue-url=${azurerm_servicebus_namespace.ns.endpoint}/${azurerm_servicebus_queue.jobs.name} \
    --region=${var.location}
[Install]
WantedBy=multi-user.target
UNIT
    systemctl daemon-reload
    systemctl enable --now lenny-loadrunner.service
  EOT
  )

  tags = local.tags
}

output "servicebus_queue_name" {
  value = azurerm_servicebus_queue.jobs.name
}
output "servicebus_namespace_name" {
  value = azurerm_servicebus_namespace.ns.name
}
output "runner_identity_id" {
  value = azurerm_user_assigned_identity.runner.id
}
output "vmss_name" {
  value = azurerm_linux_virtual_machine_scale_set.runner.name
}
