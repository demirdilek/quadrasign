# Default target executed when you just type 'make'
.DEFAULT_GOAL := help

.PHONY: up down clean help

# Internal command to generate the help documentation
help:
	@echo "Available commands:"
	@echo "  make up     - Rebuild and start the complete QuadraSign stack"
	@echo "  make down   - Stop all containers"
	@echo "  make clean  - Stop containers and completely wipe all monitored data"

up:
	docker compose up -d --build

down:
	docker compose down

clean:
	docker compose down -v