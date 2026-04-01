#!/usr/bin/env bash
set -euo pipefail

# SpecFlow Server — One-line startup script
# Usage: ./start.sh [OPTIONS]
#
# Options:
#   --no-gpu        Skip GPU allocation for Ollama
#   --model NAME    LLM model to pull (default: qwen2.5-coder:32b)
#   --light         Use lighter model (deepseek-coder-v2:16b)
#   --skip-pull     Skip model pull (assume already downloaded)
#   --detach        Run in background (default)
#   --logs          Follow logs after startup

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

MODEL="${LLM_MODEL:-qwen2.5-coder:32b}"
SKIP_PULL=false
FOLLOW_LOGS=false
NO_GPU=false
COMPOSE_PROFILES=""

# Parse args
while [[ $# -gt 0 ]]; do
  case $1 in
    --no-gpu)     NO_GPU=true; shift ;;
    --model)      MODEL="$2"; shift 2 ;;
    --light)      MODEL="deepseek-coder-v2:16b"; shift ;;
    --skip-pull)  SKIP_PULL=true; shift ;;
    --logs)       FOLLOW_LOGS=true; shift ;;
    *)            echo "Unknown option: $1"; exit 1 ;;
  esac
done

# ---- Pre-flight checks ----
echo "=== SpecFlow Server ==="
echo ""

# Check Docker
if ! command -v docker &>/dev/null; then
  echo "ERROR: Docker is not installed. Please install Docker first."
  exit 1
fi

if ! docker info &>/dev/null; then
  echo "ERROR: Docker daemon is not running."
  exit 1
fi

# Check .env
if [ ! -f .env ]; then
  if [ -f .env.example ]; then
    echo "No .env file found. Creating from .env.example..."
    cp .env.example .env
    echo ""
    echo "!!! IMPORTANT: Edit .env and set your GITHUB_TOKEN !!!"
    echo "    vim .env"
    echo ""
    read -p "Press Enter after setting GITHUB_TOKEN (or Ctrl+C to abort)..." _
  else
    echo "ERROR: No .env or .env.example found."
    exit 1
  fi
fi

# Validate GITHUB_TOKEN
source .env 2>/dev/null || true
if [ -z "${GITHUB_TOKEN:-}" ]; then
  echo "ERROR: GITHUB_TOKEN is not set in .env"
  echo "  Get one at: https://github.com/settings/tokens"
  echo "  Required scopes: repo, issues, pull_requests"
  exit 1
fi

echo "GitHub Token: ${GITHUB_TOKEN:0:8}...***"
echo "LLM Model:    $MODEL"
echo ""

# ---- Handle GPU ----
if [ "$NO_GPU" = true ]; then
  echo "GPU: disabled (--no-gpu)"
  # Create override to remove GPU reservation
  cat > docker-compose.override.yml <<'OVERRIDE'
services:
  ollama:
    deploy:
      resources: {}
OVERRIDE
else
  # Check if NVIDIA GPU is available
  if command -v nvidia-smi &>/dev/null && nvidia-smi &>/dev/null; then
    echo "GPU: NVIDIA detected"
    rm -f docker-compose.override.yml
  else
    echo "GPU: not detected, running Ollama on CPU (will be slow)"
    cat > docker-compose.override.yml <<'OVERRIDE'
services:
  ollama:
    deploy:
      resources: {}
OVERRIDE
  fi
fi
echo ""

# ---- Step 1: Build sandbox images ----
echo ">>> Building sandbox images..."
docker build -q -f docker/sandbox/golang.Dockerfile -t specflow-sandbox-golang:latest . &
docker build -q -f docker/sandbox/nestjs.Dockerfile -t specflow-sandbox-nestjs:latest . &
docker build -q -f docker/sandbox/frontend.Dockerfile -t specflow-sandbox-frontend:latest . &
docker build -q -f docker/sandbox/qa.Dockerfile -t specflow-sandbox-qa:latest . &
wait
echo "    Sandbox images built."
echo ""

# ---- Step 2: Start services ----
echo ">>> Starting services..."
docker compose up -d --build --remove-orphans 2>&1 | grep -v "^$"
echo ""

# ---- Step 3: Wait for Temporal ----
echo ">>> Waiting for Temporal server..."
for i in $(seq 1 30); do
  if docker compose exec -T temporal temporal operator cluster health 2>/dev/null | grep -q SERVING; then
    echo "    Temporal is ready."
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "    Warning: Temporal health check timed out (may still be starting)"
  fi
  sleep 2
done
echo ""

# ---- Step 4: Pull LLM model ----
if [ "$SKIP_PULL" = false ]; then
  echo ">>> Pulling LLM model: $MODEL"
  echo "    (this may take a while on first run...)"
  docker compose exec -T ollama ollama pull "$MODEL" 2>&1 | tail -1
  echo "    Model ready."
  echo ""
fi

# ---- Done ----
echo "==========================================="
echo "  SpecFlow Server is running!"
echo ""
echo "  Dashboard:    http://localhost:8090"
echo "  Temporal UI:  http://localhost:8080"
echo "  API:          http://localhost:8090/api/health"
echo ""
echo "  Quick start:"
echo "    curl -X POST http://localhost:8090/api/start \\"
echo "      -H 'Content-Type: application/json' \\"
echo "      -d '{\"repo\":\"your/repo\",\"baseBranch\":\"main\",\"userRequirement\":\"your requirement\"}'"
echo ""
echo "==========================================="

if [ "$FOLLOW_LOGS" = true ]; then
  echo ""
  echo ">>> Following logs (Ctrl+C to stop)..."
  docker compose logs -f --tail=20
fi
