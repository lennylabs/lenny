// SPDX-License-Identifier: MIT

output "queue_url" {
  description = "SQS queue URL the runners pull from."
  value       = aws_sqs_queue.jobs.url
}

output "queue_arn" {
  description = "SQS queue ARN; used by the loadctl module to grant SendMessage."
  value       = aws_sqs_queue.jobs.arn
}

output "runner_role_arn" {
  description = "IAM role the EC2 instances assume."
  value       = aws_iam_role.runner.arn
}

output "asg_name" {
  description = "ASG name; used by down-loadgen.sh to scale to zero."
  value       = aws_autoscaling_group.runner.name
}

output "metrics_collector_address" {
  description = "Reserved for the metrics collector instance. Wave 6 fills."
  value       = ""
}
