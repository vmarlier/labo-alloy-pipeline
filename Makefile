# Variables
DOCKER_COMPOSE = docker-compose.yaml

.PHONY: help build up down logs logs-app logs-pipeline logs-storage clean tidy open-grafana restart

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

tidy: ## Run go mod tidy in the producer folder
	cd producer && go mod tidy

build: ## Build all docker images
	docker compose -f $(DOCKER_COMPOSE) build

up: ## Start the lab infrastructure
	docker compose -f $(DOCKER_COMPOSE) up -d --build

down: ## Stop and remove containers
	docker compose -f $(DOCKER_COMPOSE) down

restart: down up ## Restart the lab

clean: ## Stop containers, remove volumes (resets data), and remove built local images
	docker compose -f $(DOCKER_COMPOSE) down -v --rmi local

logs: ## Follow logs of all containers
	docker compose -f $(DOCKER_COMPOSE) logs -f

logs-app: ## Follow logs of the Go producers
	docker compose -f $(DOCKER_COMPOSE) logs -f producer-a producer-b

logs-pipeline: ## Follow logs of the Alloy pipeline components
	docker compose -f $(DOCKER_COMPOSE) logs -f alloy-edge alloy-router alloy-tail-sampling alloy-span-metrics alloy-grafana

logs-storage: ## Follow logs of the storage layer
	docker compose -f $(DOCKER_COMPOSE) logs -f victoriametrics loki tempo grafana

open-grafana: ## Open Grafana in the default browser
	@echo "Opening Grafana at http://localhost:3000..."
	@if command -v open >/dev/null; then open http://localhost:3000; \
	elif command -v xdg-open >/dev/null; then xdg-open http://localhost:3000; \
	elif command -v start >/dev/null; then start http://localhost:3000; \
	else echo "Please open http://localhost:3000 in your browser"; fi
