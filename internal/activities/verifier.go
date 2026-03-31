package activities

import (
	"context"
	"fmt"

	"github.com/specflow-n8n/internal/config"
	gh "github.com/specflow-n8n/internal/github"
	"github.com/specflow-n8n/internal/llm"
	"github.com/specflow-n8n/internal/tools"
)

type VerifierActivities struct {
	Cfg config.Config
}

const verifierPrompt = `你是一位驗證專家（Verifier）。
進行三維度全面驗證：

## 1. 完整性（Completeness）
- 所有規格功能都已實作
- 所有 WHEN/THEN 場景都有測試

## 2. 正確性（Correctness）
- API 回應格式符合規格
- 業務邏輯正確

## 3. 一致性（Coherence）
- 命名風格統一
- 錯誤處理一致

## 輸出 JSON
{
  "verdict": "PASS|CONDITIONAL_PASS|FAIL",
  "completeness": "X/Y",
  "correctness": "X/Y",
  "coherence": "X/Y",
  "criticalIssues": [],
  "majorIssues": [],
  "minorIssues": [],
  "summary": "..."
}

## 判定標準
- PASS: 零 Critical, 零 Major, Minor < 3
- CONDITIONAL_PASS: 零 Critical, Major < 2
- FAIL: 有 Critical 或 Major >= 2`

func (a *VerifierActivities) Verify(ctx context.Context, input VerifierInput) (*VerifierOutput, error) {
	llmClient := llm.NewClient(a.Cfg.LLMBaseURL, a.Cfg.LLMAPIKey, a.Cfg.LLMModel)
	ghClient := gh.NewClient(a.Cfg.GitHubToken)

	agent := llm.NewAgent(llmClient, verifierPrompt, 15)

	reg := tools.NewRegistry(ghClient, nil)
	reg.AddGitHubReadTools(input.Repo, input.Branch)
	reg.ApplyTo(agent)

	prompt := fmt.Sprintf(`Repo: %s (branch: %s)

## 規格
%s

## 技術計畫
%s

## QA 報告
%s

請進行三維度驗證。`, input.Repo, input.Branch, input.Specs, input.Plan, input.QAReport)

	result, err := agent.Run(ctx, prompt)
	if err != nil {
		return nil, err
	}
	return &VerifierOutput{Summary: result}, nil
}
