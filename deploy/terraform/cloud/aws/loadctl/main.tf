// SPDX-License-Identifier: MIT
// AWS loadctl module. Provisions the tier-12 control plane on AWS.
// Wave 6 cut.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }
}

variable "release" { type = string }
variable "vpc_id" { type = string }
variable "private_subnet_ids" { type = list(string) }
variable "public_subnet_ids" { type = list(string) }
variable "loadctl_image_uri" { type = string }
variable "loadgen_queue_arn" { type = string }
variable "loadgen_queue_url" {
  description = "SQS queue URL passed to lenny-loadctl as --queue-url so it can submit Jobs to the runner pool."
  type        = string
}
variable "reports_bucket" { type = string }
variable "db_username" { type = string }
variable "db_password" {
  type      = string
  sensitive = true
}
variable "tls_certificate_arn" { type = string }
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
  description = "ProgressSink target. Empty disables persistence; s3://bucket/prefix or file:///mnt/path writes one JSONL per run."
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

# --- Postgres (RDS) -------------------------------------------------

resource "aws_db_subnet_group" "loadctl" {
  name       = "${local.name_prefix}-db"
  subnet_ids = var.private_subnet_ids
  tags       = local.tags
}

resource "aws_security_group" "db" {
  name        = "${local.name_prefix}-db"
  description = "loadctl RDS access from the Fargate task SG"
  vpc_id      = var.vpc_id
  tags        = local.tags
}

resource "aws_db_instance" "loadctl" {
  identifier             = "${local.name_prefix}-db"
  engine                 = "postgres"
  engine_version         = "16"
  instance_class         = "db.t4g.small"
  allocated_storage      = 20
  storage_type           = "gp3"
  db_name                = "lenny_loadctl"
  username               = var.db_username
  password               = var.db_password
  db_subnet_group_name   = aws_db_subnet_group.loadctl.name
  vpc_security_group_ids = [aws_security_group.db.id]
  skip_final_snapshot    = true
  publicly_accessible    = false
  storage_encrypted      = true
  tags                   = local.tags
}

# --- Fargate service ------------------------------------------------

resource "aws_ecs_cluster" "loadctl" {
  name = "${local.name_prefix}-ecs"
  tags = local.tags
}

resource "aws_security_group" "task" {
  name        = "${local.name_prefix}-task"
  description = "loadctl Fargate task egress"
  vpc_id      = var.vpc_id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = local.tags
}

resource "aws_security_group_rule" "task_to_db" {
  type                     = "ingress"
  from_port                = 5432
  to_port                  = 5432
  protocol                 = "tcp"
  source_security_group_id = aws_security_group.task.id
  security_group_id        = aws_security_group.db.id
}

resource "aws_iam_role" "task_execution" {
  name = "${local.name_prefix}-task-exec"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
  tags = local.tags
}

resource "aws_iam_role_policy_attachment" "task_execution" {
  role       = aws_iam_role.task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

resource "aws_iam_role" "task" {
  name = "${local.name_prefix}-task"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
  tags = local.tags
}

resource "aws_iam_role_policy" "task" {
  name = "${local.name_prefix}-task"
  role = aws_iam_role.task.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["sqs:SendMessage", "sqs:GetQueueAttributes"]
        Resource = var.loadgen_queue_arn
      },
      {
        Effect   = "Allow"
        Action   = ["s3:PutObject", "s3:GetObject", "s3:ListBucket"]
        Resource = ["arn:aws:s3:::${var.reports_bucket}", "arn:aws:s3:::${var.reports_bucket}/*"]
      },
      {
        Effect   = "Allow"
        Action   = ["secretsmanager:GetSecretValue"]
        Resource = [aws_secretsmanager_secret.operator_tokens.arn, aws_secretsmanager_secret.runner_tokens.arn]
      }
    ]
  })
}

# Operator + runner bearer tokens live in Secrets Manager so they are
# not exposed in the task definition. ECS valueFrom hydrates them into
# the container environment at task start.
resource "aws_secretsmanager_secret" "operator_tokens" {
  name = "${local.name_prefix}-operator-tokens"
  tags = local.tags
}

resource "aws_secretsmanager_secret_version" "operator_tokens" {
  secret_id     = aws_secretsmanager_secret.operator_tokens.id
  secret_string = var.operator_tokens
}

resource "aws_secretsmanager_secret" "runner_tokens" {
  name = "${local.name_prefix}-runner-tokens"
  tags = local.tags
}

resource "aws_secretsmanager_secret_version" "runner_tokens" {
  secret_id     = aws_secretsmanager_secret.runner_tokens.id
  secret_string = var.runner_tokens
}

# The execution role pulls the secrets at task-launch time.
resource "aws_iam_role_policy" "task_execution_secrets" {
  name = "${local.name_prefix}-task-exec-secrets"
  role = aws_iam_role.task_execution.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["secretsmanager:GetSecretValue"]
      Resource = [aws_secretsmanager_secret.operator_tokens.arn, aws_secretsmanager_secret.runner_tokens.arn]
    }]
  })
}

locals {
  # Flag list passed to the lenny-loadctl binary. Empty optional
  # values are dropped so the binary's own defaults take over.
  loadctl_flags = concat(
    [
      "-listen=:8080",
      "-storage-url=s3://${var.reports_bucket}",
      "-database-url=postgres://${var.db_username}:${var.db_password}@${aws_db_instance.loadctl.endpoint}/lenny_loadctl?sslmode=require",
      "-dispatcher=aws",
      "-queue-url=${var.loadgen_queue_url}",
      "-region=${data.aws_region.current.name}",
    ],
    var.progress_dir == "" ? [] : ["-progress-dir=${var.progress_dir}"],
    var.run_duration == "" ? [] : ["-run-duration=${var.run_duration}"],
    var.ratelimit_runs_per_min == 0 ? [] : ["-ratelimit-runs-per-min=${var.ratelimit_runs_per_min}"],
    var.ratelimit_progress_per_sec == 0 ? [] : ["-ratelimit-progress-per-sec=${var.ratelimit_progress_per_sec}"],
    var.ratelimit_ack_per_sec == 0 ? [] : ["-ratelimit-ack-per-sec=${var.ratelimit_ack_per_sec}"],
  )
}

data "aws_region" "current" {}

resource "aws_ecs_task_definition" "loadctl" {
  family                   = "${local.name_prefix}-task"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = "512"
  memory                   = "1024"
  execution_role_arn       = aws_iam_role.task_execution.arn
  task_role_arn            = aws_iam_role.task.arn

  container_definitions = jsonencode([{
    name         = "loadctl"
    image        = var.loadctl_image_uri
    command      = local.loadctl_flags
    portMappings = [{ containerPort = 8080, protocol = "tcp" }]
    secrets = [
      { name = "LENNY_LOADCTL_OPERATOR_TOKENS", valueFrom = aws_secretsmanager_secret.operator_tokens.arn },
      { name = "LENNY_LOADCTL_RUNNER_TOKENS", valueFrom = aws_secretsmanager_secret.runner_tokens.arn },
    ]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.loadctl.name
        awslogs-region        = data.aws_region.current.name
        awslogs-stream-prefix = "loadctl"
      }
    }
  }])

  tags = local.tags
}

resource "aws_cloudwatch_log_group" "loadctl" {
  name              = "/lenny/${local.name_prefix}"
  retention_in_days = 14
  tags              = local.tags
}

# --- ALB ------------------------------------------------------------

resource "aws_security_group" "alb" {
  name        = "${local.name_prefix}-alb"
  description = "loadctl ALB"
  vpc_id      = var.vpc_id
  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = local.tags
}

resource "aws_lb" "loadctl" {
  name               = local.name_prefix
  load_balancer_type = "application"
  internal           = false
  security_groups    = [aws_security_group.alb.id]
  subnets            = var.public_subnet_ids
  tags               = local.tags
}

resource "aws_lb_target_group" "loadctl" {
  name        = "${local.name_prefix}-tg"
  port        = 8080
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = var.vpc_id

  health_check {
    path = "/healthz"
  }

  # Session affinity for the WebSocket telemetry channel.
  stickiness {
    type            = "lb_cookie"
    cookie_duration = 3600
    enabled         = true
  }

  tags = local.tags
}

resource "aws_lb_listener" "loadctl" {
  load_balancer_arn = aws_lb.loadctl.arn
  port              = 443
  protocol          = "HTTPS"
  certificate_arn   = var.tls_certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.loadctl.arn
  }
}

resource "aws_ecs_service" "loadctl" {
  name            = local.name_prefix
  cluster         = aws_ecs_cluster.loadctl.id
  task_definition = aws_ecs_task_definition.loadctl.arn
  desired_count   = 1
  launch_type     = "FARGATE"

  network_configuration {
    subnets         = var.private_subnet_ids
    security_groups = [aws_security_group.task.id]
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.loadctl.arn
    container_name   = "loadctl"
    container_port   = 8080
  }

  depends_on = [aws_lb_listener.loadctl]
}

output "service_url" {
  value = "https://${aws_lb.loadctl.dns_name}"
}
output "db_endpoint" {
  value = aws_db_instance.loadctl.endpoint
}
output "task_role_arn" {
  value = aws_iam_role.task.arn
}
