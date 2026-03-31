# SpecFlow Server

基於 **Temporal** 的多 Agent 軟體交付自動化系統。每個 Agent 擁有獨立的 **Ephemeral Docker Sandbox**，透過**開源 LLM** 驅動。

靈感來自 [gpwork4u/specflow](https://github.com/gpwork4u/specflow)。

## 特色

- **Ephemeral Sandbox** — 每個任務在全新的 Docker 容器中執行，用完即銷毀，環境完全乾淨
- **Agent 隔離** — Golang / NestJS / Frontend / QA 各自擁有獨立的工具鏈和沙盒映像檔
- **開源 LLM** — 支援任何 OpenAI-compatible API (Ollama, vLLM, LM Studio, LocalAI)
- **Temporal 編排** — 持久化 workflow、自動重試、平行 fan-out、human-in-the-loop
- **GitHub API 整合** — 自動建立 branch、commit、PR，支援 webhook 觸發
- **Claude Code 工具模式** — edit_file (diff-based)、glob、grep 等精確工具

## 架構

```
                         ┌─────────────────────────┐
                         │  GitHub Webhook / API    │
                         │  :8090                   │
                         └────────────┬────────────┘
                                      │
                         ┌────────────▼────────────┐
                         │    Temporal Server       │
                         │    :7233 (gRPC)          │
                         │    :8080 (Web UI)        │
                         └────────────┬────────────┘
                                      │
          ┌───────────────────────────┼───────────────────────────┐
          │                           │                           │
   ┌──────▼──────┐           ┌────────▼────────┐         ┌───────▼───────┐
   │ Orchestrator │           │  Support Agent  │         │ LLM (Ollama)  │
   │   Worker     │           │  spec-writer    │         │ :11434        │
   │  (workflow)  │           │  tech-lead      │         └───────────────┘
   └──────────────┘           │  verifier       │
                              └─────────────────┘
          │
    ┌─────┼────────────┬───────────────┐
    │     │            │               │
┌───▼───┐┌▼──────┐┌───▼─────┐┌────────▼──┐
│Golang  ││NestJS ││Frontend ││    QA     │  ← Workers (長駐, 輕量)
│Worker  ││Worker ││ Worker  ││   Worker  │     poll task queue
└───┬────┘└──┬───┘└────┬────┘└─────┬────┘     呼叫 LLM
    │        │         │           │           建立 sandbox
    ▼        ▼         ▼           ▼
┌────────┐┌────────┐┌────────┐┌────────┐
│Sandbox │|Sandbox ││Sandbox ││Sandbox │  ← Ephemeral Container (per-task)
│Go 1.22 ││Node 20 ││Node 20 ││Playwrt │     用完即銷毀
│lint    ││NestJS  ││pnpm    ││Jest    │     完全乾淨
│delve   ││Prisma  ││Vite    ││Browser │
└────────┘└────────┘└────────┘└────────┘
```

### Worker vs Sandbox

| | Worker Container | Sandbox Container |
|---|---|---|
| 生命週期 | 長駐 (long-running) | 短暫 (per-task, ~5-10min) |
| 映像檔 | Alpine + Docker CLI | 完整工具鏈 |
| 職責 | Poll queue, 呼叫 LLM, 建立 sandbox | 執行 build/test/lint |
| 狀態 | 無狀態 | /workspace (乾淨) |
| 大小 | ~20MB | 500MB~2GB |

## Sandbox 隔離

每個 Agent 類型有專屬的 sandbox Docker image：

| Agent | Sandbox Image | 工具鏈 | Task Queue |
|-------|--------------|--------|------------|
| **Golang** | `specflow-sandbox-golang` | go 1.22, golangci-lint, goimports, delve, gotestsum | `golang-agent-queue` |
| **NestJS** | `specflow-sandbox-nestjs` | node 20, npm, @nestjs/cli, typescript, prisma, eslint | `nestjs-agent-queue` |
| **Frontend** | `specflow-sandbox-frontend` | node 20, pnpm, vite, typescript, tailwindcss, eslint | `frontend-agent-queue` |
| **QA** | `specflow-sandbox-qa` | playwright (Chromium/Firefox/WebKit), jest, supertest, k6 | `qa-agent-queue` |
| **Support** | (no sandbox) | GitHub API only | spec-writer/tech-lead/verifier |

### 工具集 (inspired by Claude Code)

每個 Coding Agent 擁有兩層工具：

**沙盒工具** (在 ephemeral container 內執行)：
| 工具 | 說明 | Claude Code 對應 |
|------|------|-----------------|
| `git_clone` | Clone repo 到 /workspace | - |
| `sandbox_read_file` | 讀取檔案 (含行號) | `Read` |
| `sandbox_write_file` | 建立新檔案 | `Write` |
| `edit_file` | Diff-based 精確編輯 | `Edit` |
| `glob` | Pattern 搜尋檔案 | `Glob` |
| `grep` | Regex 搜尋內容 | `Grep` |
| `shell` | 執行任何指令 | `Bash` |

**GitHub API 工具** (直接操作遠端 repo)：
| 工具 | 說明 |
|------|------|
| `browse_repo` | 瀏覽 repo 檔案結構 |
| `read_file` | 讀取遠端檔案 + SHA |
| `search_code` | GitHub Code Search |
| `create_branch` | 建立 feature branch |
| `write_file` | 建立/更新單一檔案 |
| `multi_file_commit` | 一次 commit 多個檔案 (Git Data API) |
| `create_pr` | 建立 Pull Request |
| `get_pr_diff` | 取得 PR diff |

## Pipeline 流程

```
1. Spec Writer  ─→  收集需求，產出 WHEN/THEN 規格
                    (spec-writer-queue)

2. Tech Lead    ─→  分析規格 + 現有程式碼，產出：
                    - 任務分解 (FEAT-001, FEAT-002, ...)
                    - 依賴圖和波次規劃
                    - 每個任務指定 agentType (golang/nestjs/frontend)
                    (tech-lead-queue)

3. Engineers    ─→  按波次平行實作：
   Wave 1:          FEAT-001 → golang-agent-queue → 建立 sandbox → build → test → commit → PR
                    FEAT-002 → nestjs-agent-queue → 建立 sandbox → build → test → commit → PR
   Wave 2:          FEAT-003 → frontend-agent-queue (depends on Wave 1)
                    每個任務在獨立的 ephemeral sandbox 中執行

4. QA           ─→  審查每個 PR、在 QA sandbox 中執行測試、回報 Bug
                    (qa-agent-queue)

5. Verifier     ─→  三維度驗證 (完整性/正確性/一致性)
                    (verifier-queue)

6. Approval     ─→  等待人工確認 (Temporal Signal, crash-safe)
```

## 快速開始

### 前置需求

- Docker + Docker Compose
- GitHub Personal Access Token (`repo`, `issues`, `pull_requests` 權限)
- GPU (建議，用於跑 LLM)

### 1. Clone 並設定

```bash
git clone git@github.com:gpwork4u/specflow-server.git
cd specflow-server

cp .env.example .env
# 編輯 .env，填入你的 GITHUB_TOKEN
```

### 2. 建構並啟動

```bash
# 方法一：使用 Makefile (推薦)
make up

# 方法二：手動
docker compose up -d --build
```

### 3. 拉取 LLM 模型

```bash
# 推薦模型
make pull-model              # qwen2.5-coder:32b (推薦, 需 ~20GB VRAM)

# 或輕量模型
make pull-model-light        # deepseek-coder-v2:16b (需 ~10GB VRAM)
```

### 4. 觸發 Pipeline

```bash
# 方法一：API
make start-pipeline REPO=your-org/your-repo REQ="建立使用者認證系統，支援 JWT"

# 方法二：curl
curl -X POST http://localhost:8090/api/start \
  -H "Content-Type: application/json" \
  -d '{
    "repo": "your-org/your-repo",
    "baseBranch": "main",
    "userRequirement": "建立使用者認證系統，支援 JWT 和 OAuth2"
  }'
```

### 5. 監控進度

```bash
# API 查詢
make pipeline-status ID=specflow-xxx

# Temporal Web UI
open http://localhost:8080

# 查看 logs
make logs                    # 全部
make logs-golang-agent       # 特定 agent
```

### 6. 人工確認

```bash
make approve ID=specflow-xxx
```

## GitHub Webhook 自動觸發

設定 webhook 後，建立帶有 `specflow` label 的 Issue 會自動觸發 pipeline。

### 設定步驟

1. 到 GitHub repo → Settings → Webhooks → Add webhook
2. **Payload URL**: `http://your-server:8090/api/webhook/github`
3. **Content type**: `application/json`
4. **Events**: 選擇 `Issues`
5. (選填) 設定 `GITHUB_WEBHOOK_SECRET` 環境變數

### 使用方式

```
建立 Issue:
  Title: 新增使用者認證功能
  Body:  支援 JWT 登入、OAuth2 (Google/GitHub)、密碼重設
  Label: specflow    ← 這個 label 觸發 pipeline
```

Pipeline 會自動啟動，可在 Temporal UI 追蹤進度。

## LLM 設定

支援任何 OpenAI-compatible API：

| 服務 | Base URL | 備註 |
|------|----------|------|
| Ollama (內建) | `http://ollama:11434/v1` | docker-compose 內建 |
| vLLM | `http://your-vllm:8000/v1` | 需自行部署 |
| LM Studio | `http://host.docker.internal:1234/v1` | 本機執行 |
| LocalAI | `http://localai:8080/v1` | 需自行部署 |

### 推薦模型

| 模型 | 參數量 | VRAM | 說明 |
|------|--------|------|------|
| `qwen2.5-coder:32b` | 32B | ~20GB | 程式碼能力強，推薦 |
| `deepseek-coder-v2:16b` | 16B | ~10GB | 輕量但能力不錯 |
| `codellama:34b` | 34B | ~20GB | Meta 出品 |
| `llama3.1:70b` | 70B | ~40GB | 通用能力最強 |

### 切換 LLM

修改 `.env`：
```bash
LLM_MODEL=your-model-name
```

或修改 `docker-compose.yml` 中的環境變數指向其他 API 服務。

## Makefile 指令

```bash
make help              # 顯示所有指令

# 建構
make build             # 編譯所有 worker binary
make sandbox           # 建構所有 sandbox Docker images
make all               # 全部建構

# 服務管理
make up                # 啟動全部服務 (含 sandbox build)
make down              # 停止全部服務
make restart           # 重啟
make status            # 查看服務狀態
make logs              # 查看全部 logs
make logs-golang-agent # 查看特定服務 logs

# LLM
make pull-model        # 拉取推薦模型
make pull-model-light  # 拉取輕量模型
make list-models       # 列出已安裝模型

# Pipeline 操作
make start-pipeline REPO=owner/repo REQ="需求描述"
make pipeline-status ID=workflow-id
make approve ID=workflow-id

# 開發
make test              # 跑測試
make vet               # go vet
make lint              # golangci-lint
make fmt               # 格式化
make dev-orchestrator  # 本機跑單一 worker

# 清理
make clean             # 清除 binary + sandbox containers
make clean-all         # 全部清除含 Docker volumes
```

## 專案結構

```
specflow-server/
├── cmd/                          # 各 Worker 入口
│   ├── api/main.go              # HTTP API 閘道 (含 webhook)
│   ├── orchestrator/main.go     # Temporal Workflow Worker
│   ├── golang-agent/main.go     # Golang Coding Agent Worker
│   ├── nestjs-agent/main.go     # NestJS Coding Agent Worker
│   ├── frontend-agent/main.go   # Frontend Coding Agent Worker
│   ├── qa-agent/main.go         # QA Agent Worker
│   └── support-agent/main.go    # Spec Writer + Tech Lead + Verifier
│
├── internal/
│   ├── workflow/specflow.go     # Temporal Workflow: 6 階段 pipeline
│   ├── activities/              # 每個 Agent 的 Activity 定義
│   │   ├── types.go             # 共用型別 (輸入/輸出)
│   │   ├── engineer.go          # Coding Agent (golang/nestjs/frontend)
│   │   ├── qa.go                # QA Agent
│   │   ├── spec_writer.go       # Spec Writer Agent
│   │   ├── tech_lead.go         # Tech Lead Agent
│   │   └── verifier.go          # Verifier Agent
│   ├── llm/
│   │   ├── client.go            # OpenAI-compatible LLM Client
│   │   └── agent.go             # Agentic Loop (think → tool → observe)
│   ├── github/client.go         # GitHub API Client (完整 CRUD)
│   ├── sandbox/
│   │   ├── sandbox.go           # Ephemeral Docker Container 管理
│   │   └── images.go            # Sandbox Image 對應表
│   ├── tools/registry.go        # Tool Registry (per-agent 工具集)
│   └── config/config.go         # 環境變數設定
│
├── docker/
│   ├── sandbox/                 # Sandbox Images (ephemeral, per-task)
│   │   ├── golang.Dockerfile   # Go 1.22 + lint + delve
│   │   ├── nestjs.Dockerfile   # Node 20 + NestJS + Prisma
│   │   ├── frontend.Dockerfile # Node 20 + pnpm + Vite
│   │   └── qa.Dockerfile       # Playwright + Jest + Browsers
│   ├── golang-agent/Dockerfile  # Worker: Alpine + Docker CLI
│   ├── nestjs-agent/Dockerfile
│   ├── frontend-agent/Dockerfile
│   ├── qa-agent/Dockerfile
│   ├── support-agent/Dockerfile
│   ├── orchestrator/Dockerfile
│   └── api/Dockerfile
│
├── agents/                      # Agent System Prompts (參考文件)
│   ├── engineer.md
│   ├── qa-engineer.md
│   ├── spec-writer.md
│   ├── tech-lead.md
│   └── verifier.md
│
├── docker-compose.yml           # 完整部署: Temporal + Workers + Ollama
├── Makefile                     # 建構/部署/操作指令
├── go.mod
├── go.sum
├── .env.example
└── .gitignore
```

## Temporal 概念對應

| SpecFlow 概念 | Temporal 對應 | 說明 |
|--------------|--------------|------|
| Pipeline | Workflow | 完整的 spec → verify 流程 |
| Agent | Activity | 在特定 Task Queue 上執行 |
| 平行開發 | Fan-out futures | 同一 Wave 的任務並行 |
| 人工審批 | Signal | 持久等待，crash-safe |
| 進度查詢 | Query | 不影響執行的狀態查詢 |
| 重試 | RetryPolicy | Temporal 自動重試失敗的 activity |
| 隔離 | Task Queue + Ephemeral Container | 每個 queue 由專屬 worker 處理，每個 task 在全新 sandbox 中執行 |

## 擴展

### 新增 Agent 類型

1. `internal/activities/types.go` — 加入新的 `AgentType`
2. `internal/activities/engineer.go` — 加入 system prompt
3. `internal/config/config.go` — 加入 Task Queue 名稱
4. `cmd/new-agent/main.go` — Worker 入口
5. `docker/sandbox/new-agent.Dockerfile` — Sandbox image
6. `docker/new-agent/Dockerfile` — Worker image (Alpine + Docker CLI)
7. `docker-compose.yml` — 加入新 service
8. `internal/workflow/specflow.go` — `agentTypeToQueue()` 加入路由
9. `internal/sandbox/images.go` — 加入 image mapping

### 水平擴展

```yaml
golang-agent:
  deploy:
    replicas: 3  # 3 個 Worker 平行處理不同任務
```

Temporal 自動負載均衡同一 Task Queue 的所有 worker。每個 worker 獨立建立 sandbox，不會互相干擾。

## API 端點

| Method | Path | 說明 |
|--------|------|------|
| POST | `/api/start` | 啟動 SpecFlow Pipeline |
| GET | `/api/status?workflowId=xxx` | 查詢 Pipeline 狀態 |
| POST | `/api/approve?workflowId=xxx` | 人工確認發布 |
| POST | `/api/webhook/github` | GitHub Webhook 自動觸發 |

## License

MIT
