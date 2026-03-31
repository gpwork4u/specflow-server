.PHONY: all build sandbox up down restart logs status clean test vet help

# ============================================
# Variables
# ============================================
WORKERS := orchestrator golang-agent nestjs-agent frontend-agent qa-agent support-agent api
SANDBOXES := sandbox-golang sandbox-nestjs sandbox-frontend sandbox-qa
BIN_DIR := bin

# ============================================
# Help
# ============================================

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ============================================
# Build
# ============================================

all: build sandbox ## Build everything

build: $(addprefix $(BIN_DIR)/,$(WORKERS)) ## Build all worker binaries
	@echo "✅ All workers built"

$(BIN_DIR)/%: cmd/*/main.go internal/**/*.go
	@mkdir -p $(BIN_DIR)
	@echo "Building $*..."
	@go build -o $@ ./cmd/$*

sandbox: ## Build all sandbox Docker images
	@echo "Building sandbox images..."
	docker build -f docker/sandbox/golang.Dockerfile -t specflow-sandbox-golang:latest .
	docker build -f docker/sandbox/nestjs.Dockerfile -t specflow-sandbox-nestjs:latest .
	docker build -f docker/sandbox/frontend.Dockerfile -t specflow-sandbox-frontend:latest .
	docker build -f docker/sandbox/qa.Dockerfile -t specflow-sandbox-qa:latest .
	@echo "✅ All sandbox images built"

# ============================================
# Docker Compose
# ============================================

up: sandbox ## Start all services
	docker compose up -d --build
	@echo ""
	@echo "✅ SpecFlow started!"
	@echo "   API:         http://localhost:8090"
	@echo "   Temporal UI: http://localhost:8080"
	@echo "   Ollama:      http://localhost:11434"
	@echo ""
	@echo "Next: pull an LLM model:"
	@echo "   make pull-model"

down: ## Stop all services
	docker compose down
	@echo "✅ Stopped"

restart: down up ## Restart all services

logs: ## Tail logs from all services
	docker compose logs -f --tail=50

logs-%: ## Tail logs from a specific service (e.g. make logs-golang-agent)
	docker compose logs -f --tail=50 $*

status: ## Show service status
	@docker compose ps
	@echo ""
	@echo "Sandbox images:"
	@docker images --filter "reference=specflow-sandbox-*" --format "  {{.Repository}}:{{.Tag}} ({{.Size}})"

# ============================================
# LLM
# ============================================

pull-model: ## Pull the default LLM model (qwen2.5-coder:32b)
	docker compose exec ollama ollama pull qwen2.5-coder:32b

pull-model-light: ## Pull a lighter model (deepseek-coder-v2:16b)
	docker compose exec ollama ollama pull deepseek-coder-v2:16b

list-models: ## List available LLM models
	docker compose exec ollama ollama list

# ============================================
# API Shortcuts
# ============================================

start-pipeline: ## Start a pipeline (usage: make start-pipeline REPO=owner/repo REQ="requirement")
	@test -n "$(REPO)" || (echo "Usage: make start-pipeline REPO=owner/repo REQ=\"your requirement\"" && exit 1)
	curl -s -X POST http://localhost:8090/api/start \
		-H "Content-Type: application/json" \
		-d '{"repo":"$(REPO)","baseBranch":"main","userRequirement":"$(REQ)"}' | jq .

pipeline-status: ## Check pipeline status (usage: make pipeline-status ID=specflow-xxx)
	@test -n "$(ID)" || (echo "Usage: make pipeline-status ID=workflow-id" && exit 1)
	curl -s http://localhost:8090/api/status?workflowId=$(ID) | jq .

approve: ## Approve a pipeline (usage: make approve ID=specflow-xxx)
	@test -n "$(ID)" || (echo "Usage: make approve ID=workflow-id" && exit 1)
	curl -s -X POST http://localhost:8090/api/approve?workflowId=$(ID) | jq .

# ============================================
# Development
# ============================================

test: ## Run tests
	go test ./... -v

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

fmt: ## Format code
	gofmt -w .
	goimports -w .

dev-%: ## Run a single worker locally (e.g. make dev-orchestrator)
	go run ./cmd/$*

# ============================================
# Cleanup
# ============================================

clean: ## Remove build artifacts and stopped sandbox containers
	rm -rf $(BIN_DIR)
	@echo "Cleaning up sandbox containers..."
	-docker ps -a --filter "name=specflow-" --format "{{.ID}}" | xargs -r docker rm -f
	@echo "✅ Cleaned"

clean-all: clean down ## Full cleanup including Docker volumes
	docker compose down -v
	-docker rmi specflow-sandbox-golang:latest specflow-sandbox-nestjs:latest \
		specflow-sandbox-frontend:latest specflow-sandbox-qa:latest 2>/dev/null
	@echo "✅ Full cleanup done"
