package activities

// ---- Shared types between workflow and activities ----

type AgentType string

const (
	AgentGolang   AgentType = "golang"
	AgentNestJS   AgentType = "nestjs"
	AgentFrontend AgentType = "frontend"
	AgentQA       AgentType = "qa"
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
	Repo            string `json:"repo"`
	BaseBranch      string `json:"baseBranch"`
	TaskID          string `json:"taskId"`
	TaskDescription string `json:"taskDescription"`
	Specs           string `json:"specs"`
	Plan            string `json:"plan"`
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
