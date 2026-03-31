package activities

import (
	"context"
	"fmt"

	"github.com/specflow-n8n/internal/config"
	gh "github.com/specflow-n8n/internal/github"
	"github.com/specflow-n8n/internal/llm"
	"github.com/specflow-n8n/internal/sandbox"
	"github.com/specflow-n8n/internal/tools"
)

// EngineerActivities holds activities for a coding agent.
// Each agent type (golang, nestjs, frontend) uses this with different system prompts.
type EngineerActivities struct {
	Cfg       config.Config
	AgentType AgentType
}

func (a *EngineerActivities) systemPrompt() string {
	base := `你是一位資深軟體工程師。根據規格和技術計畫實作程式碼。

## 你的工具

### 沙盒工具 (在獨立 Docker 容器中執行)
- git_clone: 將 repo clone 到 /workspace/repo
- sandbox_read_file: 讀取檔案 (含行號，方便 edit)
- sandbox_write_file: 建立新檔案
- edit_file: 精確替換檔案中的字串 (必須先 read 確認內容)
- glob: 用 pattern 搜尋檔案 (如 **/*.go, src/**/*.ts)
- grep: 用 regex 搜尋檔案內容
- shell: 執行任何指令 (build, test, lint, git)

### GitHub API 工具 (直接操作遠端 repo)
- browse_repo / read_file / search_code: 讀取遠端
- create_branch / write_file / multi_file_commit: 寫入遠端
- create_pr: 建立 Pull Request

## 推薦工作流程
1. git_clone 把 repo clone 到沙盒
2. glob + grep 找到相關檔案
3. sandbox_read_file 讀取需要修改的檔案
4. edit_file 精確修改 (或 sandbox_write_file 建新檔)
5. shell 編譯和測試，確認通過
6. create_branch 在遠端建立 feature branch
7. multi_file_commit 把通過測試的程式碼推到遠端
8. create_pr 建立 Pull Request

## 規則
- 一定要先 git_clone 再開始工作
- 修改檔案前先用 sandbox_read_file 讀取確認內容
- 用 edit_file 做精確修改，避免覆蓋整個檔案
- 一定要在沙盒中編譯和測試通過後才提交到遠端
- 每個 commit 對應一個邏輯單元
- 不做任務範圍外的修改
`

	switch a.AgentType {
	case AgentGolang:
		return base + `
## Golang 專屬指引
- 沙盒已安裝: go 1.22, golangci-lint, goimports, delve
- shell "cd /workspace/repo && go build ./..." 確認編譯
- shell "cd /workspace/repo && go test ./..." 執行測試
- shell "cd /workspace/repo && golangci-lint run" 確認 lint
- 遵循 Effective Go
- 錯誤處理使用 fmt.Errorf("context: %w", err)
- 使用 table-driven tests
`
	case AgentNestJS:
		return base + `
## NestJS 專屬指引
- 沙盒已安裝: node 20, npm, @nestjs/cli, typescript, prisma
- shell "cd /workspace/repo && npm install" 安裝依賴
- shell "cd /workspace/repo && npm run build" 確認建構
- shell "cd /workspace/repo && npm run test" 執行測試
- shell "nest generate module/controller/service" 建立元件
- DTO 使用 class-validator 裝飾器
`
	case AgentFrontend:
		return base + `
## Frontend 專屬指引
- 沙盒已安裝: node 20, pnpm, vite, typescript, tailwindcss
- shell "cd /workspace/repo && pnpm install" 安裝依賴
- shell "cd /workspace/repo && pnpm run build" 確認建構
- shell "cd /workspace/repo && pnpm run lint" 確認 lint
- 元件用 functional component + hooks
- 遵循 accessibility (a11y) 最佳實踐
`
	}
	return base
}

// Implement is the main activity that runs the coding agent loop.
// It creates an ephemeral sandbox container, runs the agent, then destroys it.
func (a *EngineerActivities) Implement(ctx context.Context, input EngineerInput) (*EngineerOutput, error) {
	// 1. Create ephemeral sandbox container
	sandboxName := fmt.Sprintf("specflow-%s-%s", a.AgentType, input.TaskID)
	sb, err := sandbox.Create(ctx, sandbox.Config{
		Image:   sandbox.AgentTypeToImage(string(a.AgentType)),
		Name:    sandboxName,
		Network: "specflow-n8n_specflow", // docker-compose network
		Env: map[string]string{
			"GITHUB_TOKEN": a.Cfg.GitHubToken,
		},
		Timeout: 10 * 60, // 10 minutes
	})
	if err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}
	// Always destroy sandbox when done
	defer sb.Destroy(context.Background())

	// 2. Set up LLM client and agent
	llmClient := llm.NewClient(a.Cfg.LLMBaseURL, a.Cfg.LLMAPIKey, a.Cfg.LLMModel)
	ghClient := gh.NewClient(a.Cfg.GitHubToken)

	agent := llm.NewAgent(llmClient, a.systemPrompt(), 25)

	// 3. Register tools — both GitHub API and sandbox execution
	reg := tools.NewRegistry(ghClient, sb)
	reg.AddGitHubReadTools(input.Repo, input.BaseBranch)
	reg.AddGitHubWriteTools(input.Repo, input.BaseBranch)
	reg.AddSandboxTools(a.Cfg.GitHubToken)
	reg.AddPRReviewTool(input.Repo)
	reg.ApplyTo(agent)

	// 4. Run the agent
	prompt := fmt.Sprintf(`## 任務
Task ID: %s
%s

## 規格
%s

## 技術計畫
%s

## 目標 Repo: %s (branch: %s)

請開始實作。完成後回報：
1. 建立的 branch 名稱
2. PR URL
3. 修改的檔案清單
4. 簡要摘要`,
		input.TaskID, input.TaskDescription, input.Specs, input.Plan, input.Repo, input.BaseBranch)

	result, err := agent.Run(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("engineer agent: %w", err)
	}

	return &EngineerOutput{
		Summary: result,
	}, nil
}
