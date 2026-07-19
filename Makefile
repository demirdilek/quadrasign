# Default target executed when you just type 'make'
.DEFAULT_GOAL := help

.PHONY: up down clean certs help

# Internal command to generate the help documentation
help:
	@echo "Available commands:"
	@echo "  make up     - Rebuild and start the complete api-prober stack"
	@echo "  make down   - Stop all containers"
	@echo "  make clean  - Stop containers and completely wipe all monitored data"

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