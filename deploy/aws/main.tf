data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  azs = slice(data.aws_availability_zones.available.names, 0, 3)
}

# --- Networking ---------------------------------------------------------------

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.8"

  name = "${var.cluster_name}-vpc"
  cidr = var.vpc_cidr
  azs  = local.azs

  private_subnets  = [for k in range(3) : cidrsubnet(var.vpc_cidr, 4, k)]
  public_subnets   = [for k in range(3) : cidrsubnet(var.vpc_cidr, 4, k + 8)]
  database_subnets = [for k in range(3) : cidrsubnet(var.vpc_cidr, 4, k + 12)]

  enable_nat_gateway           = true
  single_nat_gateway           = true
  enable_dns_hostnames         = true
  create_database_subnet_group = true

  # Tags required by the AWS Load Balancer Controller for subnet discovery.
  public_subnet_tags = {
    "kubernetes.io/role/elb"                    = "1"
    "kubernetes.io/cluster/${var.cluster_name}" = "shared"
  }
  private_subnet_tags = {
    "kubernetes.io/role/internal-elb"           = "1"
    "kubernetes.io/cluster/${var.cluster_name}" = "shared"
  }
}

# --- EKS ----------------------------------------------------------------------

module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 20.24"

  cluster_name    = var.cluster_name
  cluster_version = var.kubernetes_version

  cluster_endpoint_public_access           = true
  enable_cluster_creator_admin_permissions = true

  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.vpc.private_subnets

  cluster_addons = {
    coredns                = {}
    eks-pod-identity-agent = {}
    kube-proxy             = {}
    vpc-cni                = {}
  }

  eks_managed_node_groups = {
    default = {
      instance_types = var.node_instance_types
      min_size       = 2
      max_size       = 10
      desired_size   = var.node_desired_size
      capacity_type  = "ON_DEMAND"
      labels         = { workload = "chronos" }
    }
  }
}

# --- RDS (Postgres) -----------------------------------------------------------

resource "random_password" "db" {
  length  = 24
  special = false
}

resource "aws_security_group" "db" {
  name        = "${var.cluster_name}-rds"
  description = "Allow Postgres access from EKS nodes"
  vpc_id      = module.vpc.vpc_id

  ingress {
    description     = "Postgres from EKS node security group"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [module.eks.node_security_group_id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_db_parameter_group" "chronos" {
  name   = "${var.cluster_name}-pg16"
  family = "postgres16"

  parameter {
    name  = "max_connections"
    value = "500"
  }
}

resource "aws_db_instance" "chronos" {
  identifier     = "${var.cluster_name}-postgres"
  engine         = "postgres"
  engine_version = "16.4"
  instance_class = var.db_instance_class

  allocated_storage     = var.db_allocated_storage
  max_allocated_storage = var.db_allocated_storage * 4
  storage_type          = "gp3"
  storage_encrypted     = true

  db_name  = var.db_name
  username = var.db_username
  password = random_password.db.result

  multi_az               = true
  db_subnet_group_name   = module.vpc.database_subnet_group_name
  vpc_security_group_ids = [aws_security_group.db.id]
  parameter_group_name   = aws_db_parameter_group.chronos.name

  backup_retention_period   = 14
  deletion_protection       = true
  skip_final_snapshot       = false
  final_snapshot_identifier = "${var.cluster_name}-final"

  performance_insights_enabled = true
}

# --- Container registry -------------------------------------------------------

resource "aws_ecr_repository" "engine" {
  name                 = "chronos-engine"
  image_tag_mutability = "MUTABLE"
  image_scanning_configuration {
    scan_on_push = true
  }
}

resource "aws_ecr_repository" "web" {
  name                 = "chronos-engine-web"
  image_tag_mutability = "MUTABLE"
  image_scanning_configuration {
    scan_on_push = true
  }
}
