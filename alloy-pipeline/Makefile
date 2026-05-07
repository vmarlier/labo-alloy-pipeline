# Variables
IMAGE_NAME = alloy-producer
DOCKER_COMPOSE = docker-compose.yaml

.PHONY: help build up down logs clean tidy check-traffic

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

tidy: ## Run go mod tidy in the producer folder
	cd producer && go mod tidy

build: ## Build the producer image manually
	docker build -t $(IMAGE_NAME) ./producer

logs: ## Follow logs of all producers
	docker compose -f $(DOCKER_COMPOSE) logs -f producer-a producer-b

up: ## Start the lab infrastructure
	docker compose -f $(DOCKER_COMPOSE) up -d --build

down: ## Stop and remove containers
	docker compose -f $(DOCKER_COMPOSE) down --rmi

restart: down up ## Restart the lab

clean: down ## Remove containers and images
	docker rmi $(IMAGE_NAME) || true
