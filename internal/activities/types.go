package activities

// ---- Shared types between workflow and activities ----

type AgentType string

const (
	AgentGolang     AgentType = "golang"
	AgentNestJS     AgentType = "nestjs"
	AgentFrontend   AgentType = "frontend"
	AgentUIDesigner AgentType = "ui-designer"
	AgentQA         AgentType = "qa"
)

// SpecWriterInput is the input for the spec writer activity.
type SpecWriterInput struct {
	Repo            string `json:"repo"`
	UserRequirement string `json:"userRequirement"`
	ProjectContext  string `json:"projectContext"`
}

type SpecWriterOutput struct {
	Specs string `json:"specs"`
}

// TechLeadInput is the input for the tech lead activity.
type TechLeadInput struct {
	Repo       string `json:"repo"`
	BaseBranch string `json:"baseBranch"`
	Specs      string `json:"specs"`
}

type TechLeadOutput struct {
	Plan  string     `json:"plan"`
	Tasks []TaskDef  `json:"tasks"`
	Waves []WaveDef  `json:"waves"`
}

type TaskDef struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	Dependencies []string  `json:"dependencies"`
	AgentType    AgentType `json:"agentType"`
	Wave         int       `json:"wave"`
}

type WaveDef struct {
	Wave  int      `json:"wave"`
	Tasks []string `json:"tasks"`
}

// EngineerInput is the input for any coding agent activity.
type EngineerInput struct {
	Repo            string   `json:"repo"`
	BaseBranch      string   `json:"baseBranch"`
	TaskID          string   `json:"taskId"`
	TaskDescription string   `json:"taskDescription"`
	Specs           string   `json:"specs"`
	Plan            string   `json:"plan"`
	DesignSystem    string   `json:"designSystem,omitempty"` // Design system doc (for frontend tasks)
	WorkingDirs     []string `json:"workingDirs,omitempty"`  // Allowed directories (enforced by tools)
}

// DefaultWorkingDirs returns the allowed directories for each agent type.
func DefaultWorkingDirs(agentType AgentType) []string {
	switch agentType {
	case AgentGolang, AgentNestJS:
		return []string{"src/", "pkg/", "internal/", "cmd/", "api/", "lib/", "config/", "migrations/"}
	case AgentFrontend:
		return []string{"web/", "frontend/", "src/components/", "src/pages/", "src/hooks/", "src/styles/", "public/"}
	case AgentUIDesigner:
		return []string{"design-system/", "src/components/ui/"}
	case AgentQA:
		return []string{"test/", "tests/", "e2e/", "__tests__/", "cypress/", "playwright/"}
	default:
		return nil // no restriction
	}
}

type EngineerOutput struct {
	Branch       string   `json:"branch"`
	PRNumber     int      `json:"prNumber"`
	PRURL        string   `json:"prUrl"`
	FilesChanged []string `json:"filesChanged"`
	Summary      string   `json:"summary"`
}

// QAInput is the input for the QA agent activity.
type QAInput struct {
	Repo          string `json:"repo"`
	BaseBranch    string `json:"baseBranch"`
	FeatureBranch string `json:"featureBranch"`
	PRNumber      int    `json:"prNumber"`
	Specs         string `json:"specs"`
	TaskID        string `json:"taskId"`
}

type QAOutput struct {
	Status         string   `json:"status"` // PASS, FAIL
	BugsFound      []BugDef `json:"bugsFound"`
	TestsWritten   []string `json:"testsWritten"`
	Summary        string   `json:"summary"`
}

type BugDef struct {
	Severity    string `json:"severity"` // critical, major, minor
	Description string `json:"description"`
}

// UIDesignerInput is the input for the UI designer activity.
type UIDesignerInput struct {
	Repo       string `json:"repo"`
	BaseBranch string `json:"baseBranch"`
	Specs      string `json:"specs"`
	Plan       string `json:"plan"`
}

// UIDesignerOutput contains the design system definition.
type UIDesignerOutput struct {
	DesignSystem string          `json:"designSystem"` // Full design system document
	ColorPalette []ColorToken    `json:"colorPalette"`
	Typography   []TypoToken     `json:"typography"`
	Components   []ComponentSpec `json:"components"`
	Summary      string          `json:"summary"`
}

type ColorToken struct {
	Name  string `json:"name"`  // e.g. "primary", "error", "surface"
	Light string `json:"light"` // hex value for light mode
	Dark  string `json:"dark"`  // hex value for dark mode
}

type TypoToken struct {
	Name       string `json:"name"`       // e.g. "h1", "body", "caption"
	FontFamily string `json:"fontFamily"`
	FontSize   string `json:"fontSize"`
	FontWeight string `json:"fontWeight"`
	LineHeight string `json:"lineHeight"`
}

type ComponentSpec struct {
	Name        string `json:"name"`        // e.g. "Button", "Card", "Input"
	Description string `json:"description"`
	Variants    string `json:"variants"`    // e.g. "primary, secondary, ghost, danger"
	Props       string `json:"props"`       // key props description
}

// BugFixInput is the input for the bug fix activity.
// Engineer receives the original branch + bug list and fixes on the same branch.
type BugFixInput struct {
	Repo          string   `json:"repo"`
	BaseBranch    string   `json:"baseBranch"`
	FeatureBranch string   `json:"featureBranch"`
	PRNumber      int      `json:"prNumber"`
	TaskID        string   `json:"taskId"`
	Bugs          []BugDef `json:"bugs"`
	Specs         string   `json:"specs"`
	Attempt       int      `json:"attempt"` // 1, 2, 3...
}

type BugFixOutput struct {
	FixedBugs    []string `json:"fixedBugs"`
	FilesChanged []string `json:"filesChanged"`
	Summary      string   `json:"summary"`
}

// VerifierInput is the input for the verifier activity.
type VerifierInput struct {
	Repo     string `json:"repo"`
	Branch   string `json:"branch"`
	Specs    string `json:"specs"`
	Plan     string `json:"plan"`
	QAReport string `json:"qaReport"`
}

type VerifierOutput struct {
	Verdict        string   `json:"verdict"` // PASS, CONDITIONAL_PASS, FAIL
	Completeness   string   `json:"completeness"`
	Correctness    string   `json:"correctness"`
	Coherence      string   `json:"coherence"`
	CriticalIssues []string `json:"criticalIssues"`
	Summary        string   `json:"summary"`
}
