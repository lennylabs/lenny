# SPDX-License-Identifier: MIT
#
# EKS cluster module for the tier-6 e2e_cloud suite. Provisions a
# small managed-node-group EKS cluster sized for the test run, with
# the documented IRSA / OIDC issuer the gateway pod's service account
# federates against, plus a public Kubernetes API endpoint the
# operator's kubectl reaches without a bastion.
#
# Cost note: a one-cluster apply at the default shape provisions:
#
#   - 1 EKS control plane (≈ $0.10/hour)
#   - 2 t3.medium worker nodes (≈ $0.08/hour combined on-demand)
#   - 1 NAT gateway (≈ $0.045/hour + data)
#   - 3 elastic IPs and 3 public subnets
#
# Run cost is roughly $0.25/hour. The companion down.sh runs
# terraform destroy so an interrupted test does not leave the cluster
# running.

variable "create_cluster" {
  description = "When true, provision the EKS cluster + VPC alongside the KMS + S3 resources. Default false so an operator who brings their own cluster can re-use the per-release resource module."
  type        = bool
  default     = false
}

variable "kubernetes_version" {
  description = "The EKS Kubernetes control-plane version."
  type        = string
  default     = "1.31"
}

variable "node_instance_type" {
  description = "EC2 instance type for the managed node group."
  type        = string
  default     = "t3.medium"
}

variable "node_desired_size" {
  description = "Desired size for the managed node group."
  type        = number
  default     = 2
}

variable "node_max_size" {
  description = "Max size for the managed node group."
  type        = number
  default     = 4
}

variable "vpc_cidr" {
  description = "CIDR for the VPC the cluster runs in. Default 10.42.0.0/16."
  type        = string
  default     = "10.42.0.0/16"
}

data "aws_availability_zones" "available" {
  count = var.create_cluster ? 1 : 0
  state = "available"
}

# VPC with three public subnets across three AZs. EKS requires
# subnets in at least two AZs.
resource "aws_vpc" "lenny" {
  count                = var.create_cluster ? 1 : 0
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true
  tags = {
    Name                                        = "${var.release}-vpc"
    "kubernetes.io/cluster/${var.release}-eks"  = "shared"
  }
}

resource "aws_internet_gateway" "lenny" {
  count  = var.create_cluster ? 1 : 0
  vpc_id = aws_vpc.lenny[0].id
  tags = {
    Name = "${var.release}-igw"
  }
}

resource "aws_subnet" "public" {
  count                   = var.create_cluster ? 3 : 0
  vpc_id                  = aws_vpc.lenny[0].id
  cidr_block              = cidrsubnet(var.vpc_cidr, 8, count.index)
  availability_zone       = data.aws_availability_zones.available[0].names[count.index]
  map_public_ip_on_launch = true
  tags = {
    Name                                        = "${var.release}-public-${count.index}"
    "kubernetes.io/cluster/${var.release}-eks"  = "shared"
    "kubernetes.io/role/elb"                    = "1"
  }
}

resource "aws_route_table" "public" {
  count  = var.create_cluster ? 1 : 0
  vpc_id = aws_vpc.lenny[0].id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.lenny[0].id
  }
  tags = {
    Name = "${var.release}-public-rt"
  }
}

resource "aws_route_table_association" "public" {
  count          = var.create_cluster ? 3 : 0
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public[0].id
}

# IAM role + policies for the EKS control plane.
data "aws_iam_policy_document" "cluster_trust" {
  count = var.create_cluster ? 1 : 0
  statement {
    actions = ["sts:AssumeRole"]
    effect  = "Allow"
    principals {
      type        = "Service"
      identifiers = ["eks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "cluster" {
  count              = var.create_cluster ? 1 : 0
  name               = "${var.release}-cluster"
  assume_role_policy = data.aws_iam_policy_document.cluster_trust[0].json
}

resource "aws_iam_role_policy_attachment" "cluster_main" {
  count      = var.create_cluster ? 1 : 0
  role       = aws_iam_role.cluster[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"
}

# IAM role + policies for the managed node group.
data "aws_iam_policy_document" "node_trust" {
  count = var.create_cluster ? 1 : 0
  statement {
    actions = ["sts:AssumeRole"]
    effect  = "Allow"
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "node" {
  count              = var.create_cluster ? 1 : 0
  name               = "${var.release}-node"
  assume_role_policy = data.aws_iam_policy_document.node_trust[0].json
}

resource "aws_iam_role_policy_attachment" "node_worker" {
  count      = var.create_cluster ? 1 : 0
  role       = aws_iam_role.node[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy"
}

resource "aws_iam_role_policy_attachment" "node_cni" {
  count      = var.create_cluster ? 1 : 0
  role       = aws_iam_role.node[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy"
}

resource "aws_iam_role_policy_attachment" "node_ecr" {
  count      = var.create_cluster ? 1 : 0
  role       = aws_iam_role.node[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"
}

# EKS cluster.
resource "aws_eks_cluster" "lenny" {
  count    = var.create_cluster ? 1 : 0
  name     = "${var.release}-eks"
  role_arn = aws_iam_role.cluster[0].arn
  version  = var.kubernetes_version

  vpc_config {
    subnet_ids              = aws_subnet.public[*].id
    endpoint_private_access = false
    endpoint_public_access  = true
  }

  depends_on = [
    aws_iam_role_policy_attachment.cluster_main,
  ]
}

# OIDC provider for IRSA.
data "tls_certificate" "cluster" {
  count = var.create_cluster ? 1 : 0
  url   = aws_eks_cluster.lenny[0].identity[0].oidc[0].issuer
}

resource "aws_iam_openid_connect_provider" "cluster" {
  count           = var.create_cluster ? 1 : 0
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.cluster[0].certificates[0].sha1_fingerprint]
  url             = aws_eks_cluster.lenny[0].identity[0].oidc[0].issuer
}

# Managed node group.
resource "aws_eks_node_group" "default" {
  count           = var.create_cluster ? 1 : 0
  cluster_name    = aws_eks_cluster.lenny[0].name
  node_group_name = "default"
  node_role_arn   = aws_iam_role.node[0].arn
  subnet_ids      = aws_subnet.public[*].id
  instance_types  = [var.node_instance_type]

  scaling_config {
    desired_size = var.node_desired_size
    min_size     = 1
    max_size     = var.node_max_size
  }

  update_config {
    max_unavailable = 1
  }

  depends_on = [
    aws_iam_role_policy_attachment.node_worker,
    aws_iam_role_policy_attachment.node_cni,
    aws_iam_role_policy_attachment.node_ecr,
  ]
}

# EBS CSI driver addon — the tier-6 test surface (and any chart
# Deployment that needs a PVC) relies on the EBS CSI provisioner.
# A bare EKS install ships a deprecated kubernetes.io/aws-ebs
# StorageClass that fails on Kubernetes >= 1.23. The addon installs
# the modern ebs.csi.aws.com provisioner + the default
# gp2/gp3 StorageClass that drives PVCs at the documented IOPS
# tier. Trust policy: the addon uses the node IAM role's
# AmazonEBSCSIDriverPolicy attachment, attached below.
resource "aws_iam_role_policy_attachment" "node_ebs_csi" {
  count      = var.create_cluster ? 1 : 0
  role       = aws_iam_role.node[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"
}

resource "aws_eks_addon" "ebs_csi" {
  count        = var.create_cluster ? 1 : 0
  cluster_name = aws_eks_cluster.lenny[0].name
  addon_name   = "aws-ebs-csi-driver"
  depends_on = [
    aws_eks_node_group.default,
    aws_iam_role_policy_attachment.node_ebs_csi,
  ]
}

# Default StorageClass marker. The aws-ebs-csi-driver addon installs
# a `gp2` StorageClass but does NOT mark it the cluster default.
# Without a default, chart Deployments that reference an unnamed
# StorageClass on a PVC fail to bind. The Kubernetes provider would
# patch the existing class, but adding that provider just for this
# patch is overkill; the run-e2e.sh driver applies the default-
# annotation after the addon converges.

output "cluster_name" {
  description = "EKS cluster name. Empty when var.create_cluster is false."
  value       = try(aws_eks_cluster.lenny[0].name, "")
}

output "cluster_endpoint" {
  description = "EKS Kubernetes API endpoint."
  value       = try(aws_eks_cluster.lenny[0].endpoint, "")
}

output "cluster_certificate_authority_data" {
  description = "EKS cluster CA certificate (base64)."
  value       = try(aws_eks_cluster.lenny[0].certificate_authority[0].data, "")
  sensitive   = true
}

output "cluster_oidc_provider_arn" {
  description = "OIDC provider ARN. Feed this into the eks_cluster_oidc_provider_arn variable on a follow-up apply that wires IRSA."
  value       = try(aws_iam_openid_connect_provider.cluster[0].arn, "")
}

output "cluster_oidc_issuer" {
  description = "OIDC issuer URL without the https:// prefix."
  value       = try(replace(aws_eks_cluster.lenny[0].identity[0].oidc[0].issuer, "https://", ""), "")
}
