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
  description = "Object-storage URL the runner uploads per-scenario k6 summaries to (azureblob://<account>/<container>/<prefix>)."
  type        = string
  default     = ""
}
variable "admin_ssh_public_key" {
  description = "ssh-rsa public key authorized for the runner VMs. Operators MUST set this in tfvars."
  type        = string
  # Test placeholder — a generated, never-used 2048-bit RSA key. The
  # up-loadgen.sh script overrides this from $LENNY_RUNNER_SSH_KEY.
  default     = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQDc2EYzPiZF3o+nUygwYHr8tjjFh3eEZRz3RfKfEhsv6c1eyBunl/LZJv7hzZKKVabnnQyHpv8eFYr2J9TwkUMv7sH8jU3MzaJrIYBgHK6gNFqL3KCEnVlrNlbgkD9PfRk1Nx96tQOoEz41+u05ohJj1g3KbWZ+EHIaqsqJv1gnnE1WYWp1Wxen1KxBnIePXRD8YjW7mzAmnumcAxYqdSpgsEjbXqMA1l1XSqgFiUmTLwOxYrnRZGqMr0Cgw8E6+I7G/Hxv8nP6sxe2Cy0iCv6L7N9V4M0CFqg9CWi5x+yWIIPbZeVrqUx84iZpvI8RGoUmFAdmDQOyrqOSQ0kEhDF5 lenny-loadrunner-placeholder@example.com"
}
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
    username = "lenny"
    # Operators MUST override admin_ssh_public_key in tfvars; this
    # default is a syntactically valid 2048-bit RSA test key so the
    # module validates without an explicit override.
    public_key = var.admin_ssh_public_key
  }

  custom_data = base64encode(<<-EOT
    #!/bin/bash
    set -euxo pipefail
    curl -fsSL https://get.docker.com | sh
    systemctl enable --now docker
    az login --identity
    az acr login --name "${split(".", var.runner_image_id)[0]}" || true
    docker pull "${var.runner_image_id}"
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
  ${var.runner_image_id} \
  /usr/local/bin/lenny-loadrunner \
    --dispatcher=azure \
    --queue-url=${azurerm_servicebus_namespace.ns.endpoint}/${azurerm_servicebus_queue.jobs.name} \
    --region=${var.location} \
    --loadctl-url=${var.loadctl_url} \
    --cloud-label=azure \
    --capacity=1 \
    --report-storage-url=${var.report_storage_url}
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
output "servicebus_queue_url" {
  description = "Service Bus queue URL the loadctl --queue-url flag accepts."
  value       = "${azurerm_servicebus_namespace.ns.endpoint}/${azurerm_servicebus_queue.jobs.name}"
}
output "servicebus_queue_id" {
  description = "Service Bus queue resource ID; loadctl module needs this to grant Data Sender role."
  value       = azurerm_servicebus_queue.jobs.id
}
output "runner_identity_id" {
  value = azurerm_user_assigned_identity.runner.id
}
output "vmss_name" {
  value = azurerm_linux_virtual_machine_scale_set.runner.name
}
