// SPDX-License-Identifier: MIT
// AWS loadgen module variables.

variable "release" {
  description = "The Lenny release name; used as the resource prefix."
  type        = string
}

variable "vpc_id" {
  description = "The EKS cluster's VPC ID."
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnet IDs the ASG places instances in."
  type        = list(string)
}

variable "instance_type" {
  description = "EC2 instance type for runners."
  type        = string
  default     = "c6i.2xlarge"
}

variable "desired_capacity" {
  description = "Initial ASG desired count."
  type        = number
  default     = 2
}

variable "max_size" {
  description = "ASG maximum."
  type        = number
  default     = 8
}

variable "runner_image_uri" {
  description = "ECR URI of the lenny-loadrunner image."
  type        = string
}

variable "reports_bucket" {
  description = "S3 bucket the runners write per-runner k6 JSON to."
  type        = string
}

variable "loadctl_url" {
  description = "Base URL the runner uses for ack/progress/registration callbacks (https://loadctl.example.com)."
  type        = string
}

variable "runner_token" {
  description = "Bearer token the runner sends with every loadctl callback. Must appear in loadctl's runner_tokens list."
  type        = string
  sensitive   = true
}

variable "report_storage_url" {
  description = "Object-storage URL the runner uploads per-scenario k6 summaries to (s3://bucket/prefix)."
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags applied to every resource."
  type        = map(string)
  default     = {}
}
