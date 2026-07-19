# Deploying Chronos

Chronos runs the same two binaries everywhere; only the backing Postgres and
the orchestrator change.

## Local (docker-compose)

```bash
docker compose up --build
```

Brings up Postgres, the control plane, two workers, the inspector UI
(<http://localhost:8088>), Prometheus (<http://localhost:9099>) and Grafana
(<http://localhost:3000>, anonymous admin).

## Kubernetes

Manifests live in [`k8s/`](k8s) and are Kustomize-ready.

```bash
# Point the DB secret at your Postgres (RDS in production), then:
kubectl apply -k deploy/k8s
```

- `chronos-server` is a 2-replica control-plane Deployment (gRPC + REST +
  metrics). It applies schema migrations on startup.
- `chronos-worker` is an HPA-backed Deployment (3–30 replicas, CPU target 65%).
  Because the task queues are durable rows in Postgres, adding workers scales
  throughput without any coordination.
- `chronos-web` serves the inspector behind an ALB Ingress.

## AWS (EKS + RDS)

[`aws/`](aws) is a self-contained Terraform stack that provisions a VPC, an EKS
cluster with a managed node group, a Multi-AZ encrypted RDS Postgres instance,
and ECR repositories.

```bash
cd deploy/aws
terraform init
terraform apply

# Configure kubectl and create the DB secret from the outputs:
eval "$(terraform output -raw configure_kubectl)"
eval "$(terraform output -raw database_url_secret_command)"

# Build & push images to ECR, then:
kubectl apply -k ../k8s
```
