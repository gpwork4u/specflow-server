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

## 設計系統
如果任務輸入中包含 Design System，你必須：
- 使用 design-system/tokens.css 中定義的 CSS Custom Properties
- 遵循 design-system/components.md 中的元件規格
- 使用指定的色彩、字型、間距 token
- 不要自行定義新的色彩或字型，使用設計系統中的 token
- 如果需要新的 token，在 tokens.css 中擴充，保持一致性
- 所有 UI 元件必須使用設計系統定義的全域元件庫
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
		Network: a.Cfg.DockerNetwork,
		Memory:  a.Cfg.SandboxMemory,
		CPUs:    a.Cfg.SandboxCPUs,
		Env: map[string]string{
			"GITHUB_TOKEN": a.Cfg.GitHubToken,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}
	// Always destroy sandbox when done
	defer sb.Destroy(context.Background())

	// 2. Set up LLM client and agent
	llmClient := llm.NewClientFromConfig(a.Cfg.LLMProviderConfig())
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
	designSection := ""
	if input.DesignSystem != "" {
		designSection = fmt.Sprintf("\n## 設計系統 (必須遵循)\n%s\n", input.DesignSystem)
	}

	prompt := fmt.Sprintf(`## 任務
Task ID: %s
%s

## 規格
%s

## 技術計畫
%s
%s
## 目標 Repo: %s (branch: %s)

請開始實作。完成後回報：
1. 建立的 branch 名稱
2. PR URL
3. 修改的檔案清單
4. 簡要摘要`,
		input.TaskID, input.TaskDescription, input.Specs, input.Plan, designSection, input.Repo, input.BaseBranch)

	result, err := agent.Run(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("engineer agent: %w", err)
	}

	output := &EngineerOutput{Summary: result}

	// Parse structured fields from LLM output
	var parsed struct {
		Branch       string   `json:"branch"`
		PRNumber     int      `json:"prNumber"`
		PRURL        string   `json:"prUrl"`
		FilesChanged []string `json:"filesChanged"`
	}
	if llm.ParseJSONFromLLM(result, &parsed) {
		output.Branch = parsed.Branch
		output.PRNumber = parsed.PRNumber
		output.PRURL = parsed.PRURL
		output.FilesChanged = parsed.FilesChanged
	}

	return output, nil
}

// FixBugs is the activity that fixes bugs found by QA.
// It works on the EXISTING feature branch (no new branch needed).
func (a *EngineerActivities) FixBugs(ctx context.Context, input BugFixInput) (*BugFixOutput, error) {
	sandboxName := fmt.Sprintf("specflow-%s-%s-fix%d", a.AgentType, input.TaskID, input.Attempt)
	sb, err := sandbox.Create(ctx, sandbox.Config{
		Image:   sandbox.AgentTypeToImage(string(a.AgentType)),
		Name:    sandboxName,
		Network: a.Cfg.DockerNetwork,
		Memory:  a.Cfg.SandboxMemory,
		CPUs:    a.Cfg.SandboxCPUs,
		Env: map[string]string{
			"GITHUB_TOKEN": a.Cfg.GitHubToken,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}
	defer sb.Destroy(context.Background())

	llmClient := llm.NewClientFromConfig(a.Cfg.LLMProviderConfig())
	ghClient := gh.NewClient(a.Cfg.GitHubToken)

	systemPrompt := a.systemPrompt() + `
## Bug Fix 模式
你正在修復 QA 發現的 bug，不是新建功能。

規則：
- 在現有的 feature branch 上修改，不要建立新的 branch
- 用 git_clone 把 repo clone 到沙盒，checkout 到 feature branch
- 逐一修復每個 bug
- 每修一個 bug 就在沙盒中跑測試確認
- 修完後用 multi_file_commit 推到遠端 feature branch
- 不要建立新的 PR，原 PR 會自動更新
`

	agent := llm.NewAgent(llmClient, systemPrompt, 25)

	reg := tools.NewRegistry(ghClient, sb)
	reg.AddGitHubReadTools(input.Repo, input.FeatureBranch)
	reg.AddGitHubWriteTools(input.Repo, input.BaseBranch)
	reg.AddSandboxTools(a.Cfg.GitHubToken)
	reg.AddPRReviewTool(input.Repo)
	reg.ApplyTo(agent)

	// Build bug list for prompt
	bugList := ""
	for i, bug := range input.Bugs {
		bugList += fmt.Sprintf("%d. [%s] %s\n", i+1, bug.Severity, bug.Description)
	}

	prompt := fmt.Sprintf(`## Bug Fix 任務 (第 %d 次嘗試)

Task ID: %s
Repo: %s
Feature Branch: %s
PR: #%d

## QA 發現的 Bug (共 %d 個)
%s

## 規格
%s

請修復以上所有 bug。完成後回報修復結果 JSON：
` + "```json\n" + `{
  "fixedBugs": ["bug 1 描述", "bug 2 描述"],
  "filesChanged": ["path/to/file1", "path/to/file2"],
  "summary": "修復摘要"
}` + "\n```",
		input.Attempt, input.TaskID, input.Repo, input.FeatureBranch, input.PRNumber,
		len(input.Bugs), bugList, input.Specs)

	result, err := agent.Run(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("bug fix agent: %w", err)
	}

	output := &BugFixOutput{Summary: result}

	var parsed struct {
		FixedBugs    []string `json:"fixedBugs"`
		FilesChanged []string `json:"filesChanged"`
	}
	if llm.ParseJSONFromLLM(result, &parsed) {
		output.FixedBugs = parsed.FixedBugs
		output.FilesChanged = parsed.FilesChanged
	}

	return output, nil
}
