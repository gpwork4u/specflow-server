package activities

import (
	"context"
	"fmt"

	"github.com/specflow-n8n/internal/config"
	gh "github.com/specflow-n8n/internal/github"
	"github.com/specflow-n8n/internal/llm"
	"github.com/specflow-n8n/internal/tools"
)

type TechLeadActivities struct {
	Cfg config.Config
}

const techLeadPrompt = `你是一位資深技術主管（Tech Lead）。
將產品規格轉化為可執行的技術計畫。

## 輸出
你必須輸出 JSON 格式的技術計畫：
{
  "tasks": [
    {
      "id": "FEAT-001",
      "title": "...",
      "description": "詳細實作說明",
      "dependencies": [],
      "agentType": "golang|nestjs|frontend",
      "wave": 1
    }
  ],
  "waves": [
    {"wave": 1, "tasks": ["FEAT-001", "FEAT-002"]},
    {"wave": 2, "tasks": ["FEAT-003"]}
  ]
}

## 任務分配規則
- agentType 決定哪個 Agent 負責：
  - "golang": Go 後端服務、CLI 工具
  - "nestjs": Node.js/NestJS 後端 API
  - "frontend": React/Vue 前端
- 基礎設施任務排 Wave 1
- 資料模型優先於業務邏輯
- 避免循環依賴
- 每個任務是一個 PR 的大小

## 分析步驟
1. browse_repo 了解現有架構
2. read_file 讀取 package.json / go.mod 等
3. 分解任務和建立依賴圖`

func (a *TechLeadActivities) Plan(ctx context.Context, input TechLeadInput) (*TechLeadOutput, error) {
	llmClient := llm.NewClient(a.Cfg.LLMBaseURL, a.Cfg.LLMAPIKey, a.Cfg.LLMModel)
	ghClient := gh.NewClient(a.Cfg.GitHubToken)

	agent := llm.NewAgent(llmClient, techLeadPrompt, 10)

	reg := tools.NewRegistry(ghClient, nil)
	reg.AddGitHubReadTools(input.Repo, input.BaseBranch)
	reg.ApplyTo(agent)

	prompt := fmt.Sprintf("Repo: %s (branch: %s)\n\n規格：\n%s",
		input.Repo, input.BaseBranch, input.Specs)

	result, err := agent.Run(ctx, prompt)
	if err != nil {
		return nil, err
	}

	return &TechLeadOutput{Plan: result}, nil
}
