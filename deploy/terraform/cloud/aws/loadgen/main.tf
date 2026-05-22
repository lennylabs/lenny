// SPDX-License-Identifier: MIT
// AWS loadgen module. Provisions the tier-12 load-runner pool.
//
// Wave 5 cut: SQS queue, IAM role, ASG, and launch template
// scaffolding. The user-data, the runner image bake, and the
// PrivateLink endpoint are placeholders to be filled in by Wave 6.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }
}

locals {
  name_prefix = "${var.release}-loadgen"
  tags        = merge(var.tags, { "lenny.dev/component" = "loadgen" })
}

# SQS queue the runners pull jobs from.
resource "aws_sqs_queue" "jobs" {
  name                       = "${local.name_prefix}-jobs"
  visibility_timeout_seconds = 300
  message_retention_seconds  = 86400
  tags                       = local.tags
}

# IAM role for the runner EC2 instances.
resource "aws_iam_role" "runner" {
  name = "${local.name_prefix}-runner"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action = "sts:AssumeRole"
    }]
  })
  tags = local.tags
}

# Grant the runners SQS receive/delete and S3 put on the reports bucket.
resource "aws_iam_role_policy" "runner" {
  name = "${local.name_prefix}-runner"
  role = aws_iam_role.runner.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:ChangeMessageVisibility", "sqs:GetQueueAttributes"]
        Resource = aws_sqs_queue.jobs.arn
      },
      {
        Effect   = "Allow"
        Action   = ["s3:PutObject", "s3:PutObjectAcl"]
        Resource = "arn:aws:s3:::${var.reports_bucket}/*"
      }
    ]
  })
}

resource "aws_iam_instance_profile" "runner" {
  name = "${local.name_prefix}-runner"
  role = aws_iam_role.runner.name
}

# Security group allowing outbound HTTPS and DNS only.
resource "aws_security_group" "runner" {
  name        = "${local.name_prefix}-runner"
  description = "lenny-loadrunner egress"
  vpc_id      = var.vpc_id

  egress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    from_port   = 53
    to_port     = 53
    protocol    = "udp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = local.tags
}

# Launch template for the runner instances. Wave 6 fills in user-data
# that pulls the lenny-loadrunner image, configures the dispatcher,
# and starts the agent under systemd.
resource "aws_launch_template" "runner" {
  name_prefix   = "${local.name_prefix}-"
  image_id      = data.aws_ami.al2023.id
  instance_type = var.instance_type

  iam_instance_profile {
    name = aws_iam_instance_profile.runner.name
  }

  network_interfaces {
    associate_public_ip_address = false
    security_groups             = [aws_security_group.runner.id]
  }

  user_data = base64encode(<<-EOT
    #!/bin/bash
    set -euxo pipefail
    # Install Docker and the AWS CLI (al2023 ships docker as a package).
    dnf install -y docker awscli
    systemctl enable --now docker
    # Authenticate to ECR and pull the runner image.
    aws ecr get-login-password --region ${data.aws_region.current.name} \
      | docker login --username AWS --password-stdin "${split("/", var.runner_image_uri)[0]}"
    docker pull "${var.runner_image_uri}"
    # systemd unit that runs lenny-loadrunner against the SQS queue.
    cat > /etc/systemd/system/lenny-loadrunner.service <<'UNIT'
[Unit]
Description=Lenny loadrunner agent
After=docker.service network-online.target
Wants=docker.service network-online.target
[Service]
Type=simple
Restart=always
RestartSec=5
ExecStart=/usr/bin/docker run --rm --network host \
  -e AWS_REGION=${data.aws_region.current.name} \
  ${var.runner_image_uri} \
  /usr/local/bin/lenny-loadrunner \
    --dispatcher=aws \
    --queue-url=${aws_sqs_queue.jobs.url} \
    --region=${data.aws_region.current.name}
[Install]
WantedBy=multi-user.target
UNIT
    systemctl daemon-reload
    systemctl enable --now lenny-loadrunner.service
  EOT
  )

  tag_specifications {
    resource_type = "instance"
    tags          = merge(local.tags, { Name = "${local.name_prefix}-runner" })
  }
}

resource "aws_autoscaling_group" "runner" {
  name                = "${local.name_prefix}-runner"
  vpc_zone_identifier = var.private_subnet_ids
  desired_capacity    = var.desired_capacity
  min_size            = 0
  max_size            = var.max_size

  launch_template {
    id      = aws_launch_template.runner.id
    version = "$Latest"
  }

  tag {
    key                 = "lenny.dev/release"
    value               = var.release
    propagate_at_launch = true
  }
}

data "aws_ami" "al2023" {
  most_recent = true
  owners      = ["amazon"]
  filter {
    name   = "name"
    values = ["al2023-ami-2023.*-x86_64"]
  }
}

data "aws_region" "current" {}
