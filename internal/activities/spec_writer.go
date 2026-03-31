package activities

import (
	"context"
	"fmt"

	"github.com/specflow-n8n/internal/config"
	gh "github.com/specflow-n8n/internal/github"
	"github.com/specflow-n8n/internal/llm"
	"github.com/specflow-n8n/internal/tools"
)

type SpecWriterActivities struct {
	Cfg config.Config
}

const specWriterPrompt = `你是一位資深的產品規格專家（Spec Writer）。
將使用者的需求轉化為精確的技術規格文件。

## 輸出格式
對每個功能產出：
1. 功能名稱和描述
2. API 規格 (路徑、方法、Request/Response)
3. 資料模型 (欄位、型別、約束)
4. WHEN/THEN 場景 (正常 + 錯誤路徑)
5. 非功能需求

## 規則
- 先用 browse_repo 了解現有專案
- 用 read_file 讀取 README 和重要設定檔
- 規格要具體到可以直接實作的程度
- 覆蓋邊界情況和錯誤處理`

func (a *SpecWriterActivities) WriteSpec(ctx context.Context, input SpecWriterInput) (*SpecWriterOutput, error) {
	llmClient := llm.NewClient(a.Cfg.LLMBaseURL, a.Cfg.LLMAPIKey, a.Cfg.LLMModel)
	ghClient := gh.NewClient(a.Cfg.GitHubToken)

	agent := llm.NewAgent(llmClient, specWriterPrompt, 10)

	reg := tools.NewRegistry(ghClient, nil)
	reg.AddGitHubReadTools(input.Repo, "main")
	reg.ApplyTo(agent)

	prompt := fmt.Sprintf("Repo: %s\n\n需求：\n%s\n\n額外上下文：\n%s",
		input.Repo, input.UserRequirement, input.ProjectContext)

	result, err := agent.Run(ctx, prompt)
	if err != nil {
		return nil, err
	}
	return &SpecWriterOutput{Specs: result}, nil
}
