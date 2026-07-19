output "cluster_name" {
  description = "EKS cluster name"
  value       = module.eks.cluster_name
}

output "cluster_endpoint" {
  description = "EKS API server endpoint"
  value       = module.eks.cluster_endpoint
}

output "configure_kubectl" {
  description = "Command to configure kubectl against the cluster"
  value       = "aws eks update-kubeconfig --region ${var.region} --name ${module.eks.cluster_name}"
}

output "rds_endpoint" {
  description = "RDS Postgres endpoint"
  value       = aws_db_instance.chronos.address
}

output "database_url_secret_command" {
  description = "Command to create the Chronos DB secret in-cluster"
  sensitive   = true
  value = format(
    "kubectl -n chronos create secret generic chronos-db --from-literal=CHRONOS_DATABASE_URL='postgres://%s:%s@%s:5432/%s?sslmode=require'",
    var.db_username, random_password.db.result, aws_db_instance.chronos.address, var.db_name,
  )
}

output "ecr_engine_repository_url" {
  description = "ECR repository URL for the engine image"
  value       = aws_ecr_repository.engine.repository_url
}

output "ecr_web_repository_url" {
  description = "ECR repository URL for the web image"
  value       = aws_ecr_repository.web.repository_url
}
