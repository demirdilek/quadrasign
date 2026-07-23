# Default target executed when you just type 'make'
.DEFAULT_GOAL := help

.PHONY: up down clean certs help test k3d-up k3d-build k3d-deploy k3d-down

# Internal command to generate the help documentation
help:
	@echo "Available commands:"
	@echo "  --- Docker Compose Workflow ---"
	@echo "  make up         - Rebuild and start the complete api-prober stack"
	@echo "  make down       - Stop all containers"
	@echo "  make clean      - Stop containers, wipe data, and remove certificates"
	@echo "  make test       - Run Go unit and integration tests locally"
	@echo ""
	@echo "  --- Kubernetes / k3d Workflow ---"
	@echo "  make k3d-up     - Create a local k3d Kubernetes development cluster"
	@echo "  make k3d-build  - Build the Docker image locally and load it into k3d"
	@echo "  make k3d-deploy - Apply the Kubernetes manifests from deploy/k8s/"
	@echo "  make k3d-down   - Destroy the local k3d cluster and release resources"

certs:
	@mkdir -p certs
	@if [ ! -f certs/tls.crt ]; then \
		echo "Generating temporary self-signed TLS certificates for development..."; \
		openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
			-keyout certs/tls.key -out certs/tls.crt \
			-subj "/C=DE/CN=localhost"; \
	fi

up: certs
	docker compose up -d --build

down:
	docker compose down --remove-orphans

clean:
	docker compose down -v --remove-orphans
	rm -rf certs

test:
	go test -v ./...

k3d-up:
	k3d cluster create dev-cluster --port "8080:8080@loadbalancer"

k3d-build:
	docker build -t api-prober:latest .
	k3d image import api-prober:latest -c dev-cluster
	kubectl rollout restart deployment api-prober -n monitoring 2>/dev/null || true

k3d-deploy:
	kubectl apply -f deploy/k8s/api-prober.yaml

k3d-down:
	k3d cluster delete dev-cluster