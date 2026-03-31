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

type QAActivities struct {
	Cfg config.Config
}

const qaPrompt = `你是一位資深 QA 工程師。
審查程式碼變更、撰寫測試、回報問題。

## 你的沙盒環境
你有一個獨立的 Docker 容器，已安裝：
- Playwright (含 Chromium, Firefox, WebKit 瀏覽器)
- Jest, ts-jest
- supertest (API 測試)
- k6 (負載測試)

## 工作流程
1. get_pr_diff: 讀取 PR 的變更內容
2. read_file: 透過 GitHub API 理解完整上下文
3. git_clone: 把 repo clone 到沙盒
4. shell: 在沙盒中安裝依賴 (npm install)
5. sandbox_write_file: 在沙盒中撰寫測試檔案
6. shell: 在沙盒中執行測試 (jest, playwright test)
7. write_file: 透過 GitHub API 把測試推回 repo

## 輸出 JSON
{
  "status": "PASS|FAIL",
  "scenariosCovered": ["..."],
  "scenariosMissing": ["..."],
  "bugs": [
    {"severity": "critical|major|minor", "description": "..."}
  ],
  "testsWritten": ["test/..."],
  "summary": "..."
}`

func (a *QAActivities) Review(ctx context.Context, input QAInput) (*QAOutput, error) {
	// Create ephemeral QA sandbox
	sandboxName := fmt.Sprintf("specflow-qa-%s", input.TaskID)
	sb, err := sandbox.Create(ctx, sandbox.Config{
		Image:   sandbox.ImageQA,
		Name:    sandboxName,
		Network: a.Cfg.DockerNetwork,
		Memory:  a.Cfg.SandboxMemory,
		CPUs:    a.Cfg.SandboxCPUs,
		Env: map[string]string{
			"GITHUB_TOKEN": a.Cfg.GitHubToken,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create qa sandbox: %w", err)
	}
	defer sb.Destroy(context.Background())

	llmClient := llm.NewClientFromConfig(a.Cfg.LLMProviderConfig())
	ghClient := gh.NewClient(a.Cfg.GitHubToken)

	agent := llm.NewAgent(llmClient, qaPrompt, 15)

	reg := tools.NewRegistry(ghClient, sb)
	reg.AddGitHubReadTools(input.Repo, input.FeatureBranch)
	reg.AddGitHubWriteTools(input.Repo, input.FeatureBranch)
	reg.AddPRReviewTool(input.Repo)
	reg.AddSandboxTools(a.Cfg.GitHubToken)
	reg.ApplyTo(agent)

	prompt := fmt.Sprintf(`## QA 任務: %s

Repo: %s
Feature Branch: %s
PR: #%d

## 規格
%s

請審查並回報結果。`, input.TaskID, input.Repo, input.FeatureBranch, input.PRNumber, input.Specs)

	result, err := agent.Run(ctx, prompt)
	if err != nil {
		return nil, err
	}

	output := &QAOutput{Summary: result}

	var parsed struct {
		Status       string   `json:"status"`
		BugsFound    []BugDef `json:"bugs"`
		TestsWritten []string `json:"testsWritten"`
	}
	if llm.ParseJSONFromLLM(result, &parsed) {
		output.Status = parsed.Status
		output.BugsFound = parsed.BugsFound
		output.TestsWritten = parsed.TestsWritten
	}

	return output, nil
}
