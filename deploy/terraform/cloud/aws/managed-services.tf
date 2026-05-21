# SPDX-License-Identifier: MIT
#
# Optional managed-service modules for the tier-6 e2e_cloud suite.
# Provisioned independently from the chart-installed in-cluster
# datastores (Postgres + Redis Deployments under
# tests/testinfra/kind/datastores.yaml), so the existing tests keep
# running against the in-cluster fixtures while the additive
# managed-service tests connect directly to RDS and ElastiCache.
#
# Both modules default off so a stock `up.sh` run does not incur the
# RDS + ElastiCache hourly charges. The companion run-e2e.sh exposes
# WITH_RDS=1 and WITH_ELASTICACHE=1 env knobs that flip the gates and
# export the endpoints into the tier-6 test environment.
#
# Cost note (us-west-2, on-demand):
#
#   - db.t3.micro RDS (single-AZ, 20 GB gp3)       ≈ $0.017/hour
#   - cache.t3.micro ElastiCache (single shard)    ≈ $0.017/hour
#
# Both share the cluster VPC (aws_vpc.lenny) so no extra networking
# spend is required. Multi-AZ + replica options for the Multi-AZ
# failover suites are gated by additional variables that default off.

variable "create_rds" {
  description = "When true, provision an RDS Postgres instance in the cluster VPC. Default false so a stock e2e run does not incur the RDS hourly charges. Tier-6 RDS tests skip cleanly when the endpoint is unset."
  type        = bool
  default     = false
}

variable "rds_instance_class" {
  description = "RDS instance class. db.t3.micro is the smallest gp3-capable class and is sufficient for the tier-6 conformance tests."
  type        = string
  default     = "db.t3.micro"
}

variable "rds_engine_version" {
  description = "Postgres engine version. 16.x for spec parity with the in-cluster pgvector/pgvector:pg16 fixture. RDS only ships specific minor versions (16.6, 16.8, ..., 16.14 in us-west-2); leave the default unless a region/engine combination requires a different minor."
  type        = string
  default     = "16.14"
}

variable "rds_allocated_storage_gb" {
  description = "RDS storage in GB. 20 is the minimum for gp3."
  type        = number
  default     = 20
}

variable "rds_multi_az" {
  description = "When true, provision the RDS instance as Multi-AZ for the §17.3 failover suite. Doubles the hourly cost."
  type        = bool
  default     = false
}

variable "create_elasticache" {
  description = "When true, provision an ElastiCache Redis replication group in the cluster VPC. Default false. Tier-6 Redis tests skip cleanly when the endpoint is unset."
  type        = bool
  default     = false
}

variable "elasticache_node_type" {
  description = "ElastiCache node type. cache.t3.micro is the smallest available and sufficient for the conformance tests."
  type        = string
  default     = "cache.t3.micro"
}

variable "elasticache_num_node_groups" {
  description = "Number of shards (node groups). 1 = non-cluster mode, > 1 = cluster mode with hash-slot partitioning."
  type        = number
  default     = 1
}

variable "elasticache_replicas_per_node_group" {
  description = "Number of read replicas per shard. 0 = single primary per shard, > 0 = failover-capable replicas."
  type        = number
  default     = 0
}

# Shared security group: allow ingress from anywhere inside the VPC
# (the EKS pods reach the managed services over the VPC's private
# network on Postgres 5432 + Redis 6379). The existing
# aws_eks_cluster's vpc_config attaches the cluster ENIs to the same
# public subnets, so the cluster's pod CIDR is reachable.
variable "managed_datastores_test_cidrs" {
  description = "Extra CIDRs admitted to Postgres + Redis on the managed-datastores security group. Operator's home/office IP goes here so tier-6 tests can connect from outside the VPC. Default empty so a stock install only admits VPC traffic."
  type        = list(string)
  default     = []
}

resource "aws_security_group" "managed_datastores" {
  count       = (var.create_rds || var.create_elasticache) ? 1 : 0
  name        = "${var.release}-managed-datastores"
  description = "Allow Postgres + Redis from the cluster VPC for tier-6 managed-service tests"
  vpc_id      = var.create_cluster ? aws_vpc.lenny[0].id : null

  ingress {
    description = "Postgres from the VPC + test CIDRs"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = concat([var.vpc_cidr], var.managed_datastores_test_cidrs)
  }

  ingress {
    description = "Redis from the VPC + test CIDRs"
    from_port   = 6379
    to_port     = 6379
    protocol    = "tcp"
    cidr_blocks = concat([var.vpc_cidr], var.managed_datastores_test_cidrs)
  }

  # The egress default-allow is intentional: the managed services
  # initiate outbound connections (replication, IAM, SSM agent).
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

# ===========================================================================
# RDS Postgres
# ===========================================================================

resource "aws_db_subnet_group" "rds" {
  count       = var.create_rds ? 1 : 0
  name        = "${var.release}-rds"
  description = "Subnet group for the tier-6 RDS Postgres instance"
  subnet_ids  = var.create_cluster ? aws_subnet.public[*].id : []
}

# Parameter group enforcing the §13.2 RDS TLS invariant. force_ssl=1
# refuses non-TLS connections at the engine, which is what
# TestCloudRDSTLSRequired asserts.
resource "aws_db_parameter_group" "rds" {
  count       = var.create_rds ? 1 : 0
  name        = "${var.release}-rds-pg16"
  description = "Lenny tier-6 RDS Postgres parameter group"
  family      = "postgres16"

  parameter {
    name  = "rds.force_ssl"
    value = "1"
  }

  parameter {
    name         = "shared_preload_libraries"
    value        = "pg_stat_statements"
    apply_method = "pending-reboot"
  }
}

# Master credentials handled via AWS-managed Secret. Auto-rotation
# stays off for tier-6; a production install enables rotation via
# rds.master_user_secret_rotation.
resource "random_password" "rds_master" {
  count   = var.create_rds ? 1 : 0
  length  = 32
  special = false
}

resource "aws_secretsmanager_secret" "rds_master" {
  count                   = var.create_rds ? 1 : 0
  name                    = "${var.release}-rds-master"
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "rds_master" {
  count         = var.create_rds ? 1 : 0
  secret_id     = aws_secretsmanager_secret.rds_master[0].id
  secret_string = jsonencode({
    username = "lenny"
    password = random_password.rds_master[0].result
  })
}

resource "aws_db_instance" "rds" {
  count                       = var.create_rds ? 1 : 0
  identifier                  = "${var.release}-rds"
  engine                      = "postgres"
  engine_version              = var.rds_engine_version
  instance_class              = var.rds_instance_class
  allocated_storage           = var.rds_allocated_storage_gb
  storage_type                = "gp3"
  storage_encrypted           = true
  db_subnet_group_name        = aws_db_subnet_group.rds[0].name
  parameter_group_name        = aws_db_parameter_group.rds[0].name
  vpc_security_group_ids      = [aws_security_group.managed_datastores[0].id]
  publicly_accessible         = true
  multi_az                    = var.rds_multi_az
  db_name                     = "lenny"
  username                    = "lenny"
  password                    = random_password.rds_master[0].result
  iam_database_authentication_enabled = true
  skip_final_snapshot         = true
  deletion_protection         = false
  apply_immediately           = true
  backup_retention_period     = 1
}

# ===========================================================================
# ElastiCache Redis
# ===========================================================================

resource "aws_elasticache_subnet_group" "redis" {
  count       = var.create_elasticache ? 1 : 0
  name        = "${var.release}-redis"
  description = "Subnet group for the tier-6 ElastiCache Redis cluster"
  subnet_ids  = var.create_cluster ? aws_subnet.public[*].id : []
}

resource "aws_elasticache_parameter_group" "redis" {
  count       = var.create_elasticache ? 1 : 0
  name        = "${var.release}-redis-7"
  description = "Lenny tier-6 ElastiCache Redis parameter group"
  family      = "redis7"

  # §11.2.1 billing-stream eviction-policy invariant: allkeys-lru
  # would drop billing events under memory pressure. Use noeviction
  # so the stream backpressures into the gateway's flusher rather
  # than silently dropping.
  parameter {
    name  = "maxmemory-policy"
    value = "noeviction"
  }
}

resource "random_password" "redis_auth" {
  count   = var.create_elasticache ? 1 : 0
  length  = 32
  special = false
}

resource "aws_secretsmanager_secret" "redis_auth" {
  count                   = var.create_elasticache ? 1 : 0
  name                    = "${var.release}-redis-auth"
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "redis_auth" {
  count         = var.create_elasticache ? 1 : 0
  secret_id     = aws_secretsmanager_secret.redis_auth[0].id
  secret_string = random_password.redis_auth[0].result
}

resource "aws_elasticache_replication_group" "redis" {
  count                      = var.create_elasticache ? 1 : 0
  replication_group_id       = "${var.release}-redis"
  description                = "Lenny tier-6 ElastiCache Redis"
  engine                     = "redis"
  engine_version             = "7.1"
  node_type                  = var.elasticache_node_type
  num_node_groups            = var.elasticache_num_node_groups
  replicas_per_node_group    = var.elasticache_replicas_per_node_group
  parameter_group_name       = aws_elasticache_parameter_group.redis[0].name
  subnet_group_name          = aws_elasticache_subnet_group.redis[0].name
  security_group_ids         = [aws_security_group.managed_datastores[0].id]
  port                       = 6379
  at_rest_encryption_enabled = true
  transit_encryption_enabled = true
  auth_token                 = random_password.redis_auth[0].result
  automatic_failover_enabled = var.elasticache_replicas_per_node_group > 0
  multi_az_enabled           = var.elasticache_replicas_per_node_group > 0
  apply_immediately          = true
}

# ===========================================================================
# Outputs
# ===========================================================================

output "rds_endpoint" {
  description = "RDS Postgres endpoint (hostname:port). Empty when create_rds=false. Tier-6 RDS tests read LENNY_AWS_RDS_ENDPOINT from this output."
  value       = try(aws_db_instance.rds[0].endpoint, "")
}

output "rds_instance_id" {
  description = "RDS instance identifier (the human name, e.g. lenny-e2e-rds) — used by tests that query the RDS API directly."
  value       = try(aws_db_instance.rds[0].identifier, "")
}

output "rds_master_secret_arn" {
  description = "Secrets Manager ARN holding the RDS master {username, password} JSON. Empty when create_rds=false."
  value       = try(aws_secretsmanager_secret.rds_master[0].arn, "")
}

output "rds_database_name" {
  description = "The RDS database name. Empty when create_rds=false."
  value       = try(aws_db_instance.rds[0].db_name, "")
}

output "rds_resource_id" {
  description = "RDS resource ID — used in the IAM auth policy ARN."
  value       = try(aws_db_instance.rds[0].resource_id, "")
}

output "rds_multi_az" {
  description = "True when the RDS instance is multi-AZ. Tier-6 tests gating the §17.3 failover suite read this."
  value       = var.rds_multi_az
}

output "elasticache_endpoint" {
  description = "ElastiCache primary endpoint. Empty when create_elasticache=false. Tier-6 Redis tests read LENNY_AWS_REDIS_ENDPOINT from this output."
  value       = try(aws_elasticache_replication_group.redis[0].primary_endpoint_address, "")
}

output "elasticache_configuration_endpoint" {
  description = "ElastiCache configuration endpoint for cluster-mode clients. Empty when create_elasticache=false or num_node_groups=1."
  value       = try(aws_elasticache_replication_group.redis[0].configuration_endpoint_address, "")
}

output "elasticache_auth_secret_arn" {
  description = "Secrets Manager ARN holding the Redis AUTH token. Empty when create_elasticache=false."
  value       = try(aws_secretsmanager_secret.redis_auth[0].arn, "")
}

output "elasticache_port" {
  description = "ElastiCache port. 6379 by default."
  value       = try(aws_elasticache_replication_group.redis[0].port, 0)
}

output "elasticache_cluster_mode_enabled" {
  description = "True when ElastiCache is provisioned with more than one shard."
  value       = var.elasticache_num_node_groups > 1
}
