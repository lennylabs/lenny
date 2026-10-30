# SPDX-License-Identifier: MIT
#
# Azure managed Postgres + Redis modules. Mirrors
# deploy/terraform/cloud/aws/managed-services.tf for the AWS WITH_RDS /
# WITH_ELASTICACHE path. Tier-7 cloud-load on Azure provisions both so
# the gateway has a production-shaped data plane that does not consume
# AKS node CPU.
#
# The modules use the simplest public-endpoint topology. Both services
# support private VNet integration (Postgres Flexible Server VNet
# injection, Premium Redis Cache); a follow-on can switch to
# private-endpoint when an azure_vnet variable lands. Today the public
# endpoint plus TLS plus AUTH/password is sufficient for an ephemeral
# test cluster, and matches the AWS variant's reachability story
# (public endpoint with a security-group ACL).

variable "create_flexible_postgres" {
  description = "When true, provision an Azure Database for PostgreSQL Flexible Server. Gated so the default tier-6 path stays cluster-only."
  type        = bool
  default     = false
}

variable "flexible_postgres_sku" {
  description = "SKU for the Flexible Server. Burstable B_Standard_B1ms is the cheapest viable size for tier-7 load."
  type        = string
  default     = "B_Standard_B2s"
}

variable "flexible_postgres_storage_mb" {
  description = "Allocated storage in MB. Minimum 32768 (32 GiB)."
  type        = number
  default     = 32768
}

variable "flexible_postgres_version" {
  description = "Postgres major version."
  type        = string
  default     = "16"
}

variable "create_azure_redis" {
  description = "When true, provision an Azure Cache for Redis instance."
  type        = bool
  default     = false
}

variable "azure_redis_sku" {
  description = "Redis SKU. Standard supports replication and is the minimum tier with TLS+AUTH."
  type        = string
  default     = "Standard"
}

variable "azure_redis_family" {
  description = "Redis family (C=Basic/Standard, P=Premium)."
  type        = string
  default     = "C"
}

variable "azure_redis_capacity" {
  description = "Redis capacity (C-family: 0=250MB, 1=1GB, 2=2.5GB, ...; tier-7 load fits in 1)."
  type        = number
  default     = 1
}

variable "managed_datastores_caller_ip" {
  description = "Caller IP address (a CIDR or single IP). When set, the firewall rule admits the operator workstation so a Secrets-Manager-style script can connect to compose the DSN for tests. Empty disables the rule."
  type        = string
  default     = ""
}

# Postgres Flexible Server. Public-endpoint deployment with TLS-only
# and the firewall opened to AKS egress + the AllowAllAzureIps range
# so the test cluster can reach it. The admin password is generated
# and stored in Key Vault.
resource "random_password" "postgres_admin" {
  count   = var.create_flexible_postgres ? 1 : 0
  length  = 32
  special = false
}

resource "azurerm_postgresql_flexible_server" "this" {
  count                  = var.create_flexible_postgres ? 1 : 0
  name                   = "${var.release}-pg"
  resource_group_name    = data.azurerm_resource_group.this.name
  location               = var.location
  version                = var.flexible_postgres_version
  administrator_login    = "lenny"
  administrator_password = random_password.postgres_admin[0].result
  sku_name               = var.flexible_postgres_sku
  storage_mb             = var.flexible_postgres_storage_mb
  zone                   = "1"

  # Default backup retention is fine for ephemeral test infra.
  backup_retention_days = 7

  authentication {
    password_auth_enabled = true
  }
}

resource "azurerm_postgresql_flexible_server_database" "lenny" {
  count     = var.create_flexible_postgres ? 1 : 0
  name      = "lenny"
  server_id = azurerm_postgresql_flexible_server.this[0].id
  charset   = "UTF8"
  collation = "en_US.utf8"
}

# pgvector allowlist. Migration 0044_agent_memory_embedding.up.sql
# runs `CREATE EXTENSION vector;` to enable the §9.4 pgvector
# embedding column. Azure Flexible Server rejects CREATE EXTENSION
# unless the extension is named in `azure.extensions`; without this
# parameter the migration fails with "extension not allow-listed" and
# the gateway never starts. require_secure_transport defaults to ON,
# matching the sslmode=require DSN the script composes.
resource "azurerm_postgresql_flexible_server_configuration" "extensions" {
  count     = var.create_flexible_postgres ? 1 : 0
  name      = "azure.extensions"
  server_id = azurerm_postgresql_flexible_server.this[0].id
  value     = "VECTOR,PG_STAT_STATEMENTS"
}

resource "azurerm_postgresql_flexible_server_firewall_rule" "azure_services" {
  count            = var.create_flexible_postgres ? 1 : 0
  name             = "allow-azure-services"
  server_id        = azurerm_postgresql_flexible_server.this[0].id
  start_ip_address = "0.0.0.0"
  end_ip_address   = "0.0.0.0"
}

resource "azurerm_postgresql_flexible_server_firewall_rule" "caller" {
  count            = (var.create_flexible_postgres && var.managed_datastores_caller_ip != "") ? 1 : 0
  name             = "allow-caller"
  server_id        = azurerm_postgresql_flexible_server.this[0].id
  start_ip_address = var.managed_datastores_caller_ip
  end_ip_address   = var.managed_datastores_caller_ip
}

# Store the Postgres admin secret in Key Vault so the cloud script can
# fetch it without re-running terraform output. The access policy is
# attached to the gateway managed identity in main.tf, plus the
# Terraform principal needs Set/Get to write here.
resource "azurerm_key_vault_secret" "postgres_admin" {
  count        = var.create_flexible_postgres ? 1 : 0
  name         = "${var.release}-postgres-admin"
  key_vault_id = azurerm_key_vault.platform.id
  value = jsonencode({
    username = "lenny"
    password = random_password.postgres_admin[0].result
    host     = azurerm_postgresql_flexible_server.this[0].fqdn
    port     = 5432
    database = "lenny"
  })
  depends_on = [azurerm_key_vault_access_policy.terraform_self]
}

# Azure Cache for Redis. Standard tier (replicated, AZ-redundant when
# the region supports it). TLS-only, with AUTH key auto-generated by
# Azure.
resource "azurerm_redis_cache" "this" {
  count                         = var.create_azure_redis ? 1 : 0
  name                          = "${var.release}-redis"
  resource_group_name           = data.azurerm_resource_group.this.name
  location                      = var.location
  capacity                      = var.azure_redis_capacity
  family                        = var.azure_redis_family
  sku_name                      = var.azure_redis_sku
  non_ssl_port_enabled          = false
  minimum_tls_version           = "1.2"
  public_network_access_enabled = true

  redis_configuration {
    # §11.2.1 billing-stream eviction-policy invariant: allkeys-lru
    # would drop billing events under memory pressure. Use noeviction
    # so the stream backpressures into the gateway's flusher rather
    # than silently dropping.
    maxmemory_policy = "noeviction"
  }
}

resource "azurerm_key_vault_secret" "redis_auth" {
  count        = var.create_azure_redis ? 1 : 0
  name         = "${var.release}-redis-auth"
  key_vault_id = azurerm_key_vault.platform.id
  value = jsonencode({
    host = azurerm_redis_cache.this[0].hostname
    port = azurerm_redis_cache.this[0].ssl_port
    auth = azurerm_redis_cache.this[0].primary_access_key
  })
  depends_on = [azurerm_key_vault_access_policy.terraform_self]
}

# The Terraform principal needs Key Vault Secret write access to
# persist the postgres + redis credentials above. The gateway-side
# access policy in main.tf only grants Key permissions; this access
# policy reuses the data.azurerm_client_config.current already
# declared in main.tf.
resource "azurerm_key_vault_access_policy" "terraform_self" {
  count        = (var.create_flexible_postgres || var.create_azure_redis) ? 1 : 0
  key_vault_id = azurerm_key_vault.platform.id
  tenant_id    = data.azurerm_client_config.current.tenant_id
  object_id    = data.azurerm_client_config.current.object_id
  secret_permissions = ["Get", "Set", "List", "Delete", "Purge", "Recover"]
}

# Outputs the tier-6 / tier-7 driver scripts read into env vars and
# pass into the chart values overlay. Empty when the corresponding
# create_* gate is false; the script falls back to the in-cluster
# fixtures then.
output "flexible_postgres_fqdn" {
  description = "FQDN of the Flexible Server. Empty when create_flexible_postgres=false."
  value       = try(azurerm_postgresql_flexible_server.this[0].fqdn, "")
}

output "flexible_postgres_admin_secret_name" {
  description = "Key Vault secret name holding the JSON {username, password, host, port, database} for the Flexible Server master account."
  value       = try(azurerm_key_vault_secret.postgres_admin[0].name, "")
}

output "flexible_postgres_database_name" {
  description = "Database name."
  value       = try(azurerm_postgresql_flexible_server_database.lenny[0].name, "")
}

output "azure_redis_hostname" {
  description = "Azure Cache for Redis hostname. Empty when create_azure_redis=false."
  value       = try(azurerm_redis_cache.this[0].hostname, "")
}

output "azure_redis_ssl_port" {
  description = "TLS port (default 6380)."
  value       = try(azurerm_redis_cache.this[0].ssl_port, 0)
}

output "azure_redis_auth_secret_name" {
  description = "Key Vault secret name holding the JSON {host, port, auth} for the Redis cache."
  value       = try(azurerm_key_vault_secret.redis_auth[0].name, "")
}
