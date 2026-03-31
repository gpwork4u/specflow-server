package activities

import (
	"context"
	"fmt"
	"time"

	"github.com/specflow-n8n/internal/config"
	gh "github.com/specflow-n8n/internal/github"
	"github.com/specflow-n8n/internal/llm"
	"github.com/specflow-n8n/internal/sandbox"
	"github.com/specflow-n8n/internal/tools"
)

type UIDesignerActivities struct {
	Cfg config.Config
}

const uiDesignerPrompt = `你是一位資深 UI/UX 設計師（UI Designer Agent）。
你的職責是根據產品規格和技術計畫，定義完整的設計系統（Design System），
讓 Frontend Agent 可以直接套用。

## 你需要產出的設計系統

### 1. 色彩系統 (Color Palette)
- Primary / Secondary / Accent 色系
- Semantic 色: success, warning, error, info
- Neutral 色: background, surface, border, text 各層級
- Light mode + Dark mode 兩套
- 以 CSS Custom Properties (--color-xxx) 定義

### 2. 文字排版 (Typography)
- 字型選擇（中文 + 英文 fallback）
- 標題層級 (h1~h6)、內文、caption、label 的 size/weight/height
- 以 CSS class 或 Tailwind config 定義

### 3. 間距系統 (Spacing)
- 4px 基準的間距 scale (4, 8, 12, 16, 24, 32, 48, 64)
- Component 內部 padding 規則
- Layout gap 規則

### 4. 全域元件庫 (Global Component Library)
你必須產出可直接使用的元件程式碼，不只是規格文件。
根據專案的框架（React/Vue），建立以下全域元件：

基礎元件（必須）：
- Button: variants (primary, secondary, ghost, danger, link), sizes (xs, sm, md, lg)
- Input / Select / Textarea: 含 label, helper text, error state, disabled
- Badge / Tag: 狀態標籤、分類標籤
- Avatar: 圖片、文字 fallback、size 變體
- Spinner / Skeleton: loading 狀態

回饋元件（必須）：
- Toast / Notification: success, error, warning, info
- Modal / Dialog: header, body, footer, close 按鈕
- Alert: inline 提示訊息

布局元件（必須）：
- Card: 標準內容容器
- Table: 排版、排序指示、分頁控制
- Tabs: 標籤切換
- Sidebar / Header: 導航結構

表單元件（必須）：
- Form / FormField: 含 validation 狀態顯示
- Checkbox / Radio / Switch: 開關控制
- DatePicker: 日期選擇（如需要）

每個元件必須：
- 使用 tokens.css 中的設計 token
- 支援 props type 定義 (TypeScript)
- 包含基本的 a11y 屬性 (aria-label, role, tabIndex)
- 有清楚的 export

### 5. 輸出檔案
你需要在 sandbox 中建立以下檔案，然後透過 GitHub API 提交：
- design-system/tokens.css — CSS Custom Properties (色彩、字型、間距)
- design-system/tailwind.preset.js — Tailwind CSS preset (如果專案用 Tailwind)
- design-system/components.md — 元件規格文件（API、variants、props）
- design-system/README.md — 設計系統總覽與使用指南
- src/components/ui/Button.tsx (或 .vue) — Button 元件
- src/components/ui/Input.tsx — Input 元件
- src/components/ui/Card.tsx — Card 元件
- src/components/ui/Modal.tsx — Modal 元件
- src/components/ui/Toast.tsx — Toast 元件
- src/components/ui/Table.tsx — Table 元件
- src/components/ui/Badge.tsx — Badge 元件
- src/components/ui/index.ts — 統一 export 所有元件

## 工作流程
1. browse_repo + read_file 了解現有專案的 UI 框架和風格
2. 分析規格中的 UI 需求
3. git_clone 到 sandbox
4. sandbox_write_file 建立設計系統檔案
5. shell 驗證 CSS 語法正確
6. create_branch + multi_file_commit + create_pr

## 輸出 JSON
完成後回報：
` + "```json\n" + `{
  "branch": "design/design-system",
  "prNumber": 1,
  "prUrl": "https://...",
  "colorPalette": [
    {"name": "primary", "light": "#3b82f6", "dark": "#60a5fa"},
    {"name": "error", "light": "#ef4444", "dark": "#f87171"}
  ],
  "typography": [
    {"name": "h1", "fontFamily": "Inter, sans-serif", "fontSize": "2.25rem", "fontWeight": "700", "lineHeight": "1.2"}
  ],
  "components": [
    {"name": "Button", "description": "主要操作按鈕", "variants": "primary, secondary, ghost, danger", "props": "size, loading, disabled, icon"}
  ],
  "summary": "..."
}` + "\n```"

func (a *UIDesignerActivities) Design(ctx context.Context, input UIDesignerInput) (*UIDesignerOutput, error) {
	// Create ephemeral sandbox
	sandboxName := fmt.Sprintf("specflow-ui-designer-%d", time.Now().UnixNano())
	sb, err := sandbox.Create(ctx, sandbox.Config{
		Image:   sandbox.ImageFrontend, // reuse frontend sandbox (Node + CSS tools)
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

	agent := llm.NewAgent(llmClient, uiDesignerPrompt, 20)

	reg := tools.NewRegistry(ghClient, sb)
	reg.AddGitHubReadTools(input.Repo, input.BaseBranch)
	reg.AddGitHubWriteTools(input.Repo, input.BaseBranch)
	reg.AddSandboxTools(a.Cfg.GitHubToken)
	reg.ApplyTo(agent)

	prompt := fmt.Sprintf(`## 設計系統任務

Repo: %s (branch: %s)

## 產品規格
%s

## 技術計畫
%s

請分析需求並建立完整的設計系統。`, input.Repo, input.BaseBranch, input.Specs, input.Plan)

	result, err := agent.Run(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("ui designer agent: %w", err)
	}

	output := &UIDesignerOutput{DesignSystem: result, Summary: result}

	var parsed struct {
		ColorPalette []ColorToken    `json:"colorPalette"`
		Typography   []TypoToken     `json:"typography"`
		Components   []ComponentSpec `json:"components"`
	}
	if llm.ParseJSONFromLLM(result, &parsed) {
		output.ColorPalette = parsed.ColorPalette
		output.Typography = parsed.Typography
		output.Components = parsed.Components
	}

	return output, nil
}
