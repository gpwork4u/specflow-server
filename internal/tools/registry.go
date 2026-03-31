package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/specflow-n8n/internal/github"
	"github.com/specflow-n8n/internal/llm"
	"github.com/specflow-n8n/internal/sandbox"
)

// Registry holds tools available to a specific agent.
type Registry struct {
	gh          *github.Client
	sandbox     *sandbox.Sandbox // nil if no sandbox needed (e.g. spec-writer)
	tools       map[string]registeredTool
	allowedDirs []string // if set, write operations are restricted to these directories
}

type registeredTool struct {
	def     llm.Tool
	handler llm.ToolHandler
}

func NewRegistry(gh *github.Client, sb *sandbox.Sandbox) *Registry {
	return &Registry{
		gh:      gh,
		sandbox: sb,
		tools:   make(map[string]registeredTool),
	}
}

// SetAllowedDirs restricts write operations to specific directory prefixes.
// Read operations are not restricted.
func (r *Registry) SetAllowedDirs(dirs []string) {
	r.allowedDirs = dirs
}

// checkPathAllowed validates that a file path is within the allowed directories.
// Returns error if path is outside allowed dirs. Returns nil if no restrictions set.
func (r *Registry) checkPathAllowed(path string) error {
	if len(r.allowedDirs) == 0 {
		return nil
	}
	// Normalize: remove leading slash and /workspace/repo/ prefix
	normalized := strings.TrimPrefix(path, "/")
	normalized = strings.TrimPrefix(normalized, "workspace/repo/")
	normalized = strings.TrimPrefix(normalized, "workspace/")

	for _, dir := range r.allowedDirs {
		if strings.HasPrefix(normalized, dir) {
			return nil
		}
	}
	// Also allow root config files (package.json, go.mod, etc.)
	if !strings.Contains(normalized, "/") {
		return nil
	}
	return fmt.Errorf("path %q is outside allowed working directories %v. You can only modify files in: %s",
		path, r.allowedDirs, strings.Join(r.allowedDirs, ", "))
}

func (r *Registry) register(name, description string, params json.RawMessage, handler llm.ToolHandler) {
	r.tools[name] = registeredTool{
		def:     llm.Tool{Name: name, Description: description, Parameters: params},
		handler: handler,
	}
}

// ApplyTo registers all tools in this registry to an agent.
func (r *Registry) ApplyTo(agent *llm.Agent) {
	for _, t := range r.tools {
		agent.RegisterTool(t.def.Name, t.def.Description, t.def.Parameters, t.handler)
	}
}

// ---- Shared GitHub tools ----

func (r *Registry) AddGitHubReadTools(repo, branch string) {
	r.register("browse_repo", "Browse the repository file tree. Returns all files and directories.",
		jsonSchema(`{"type":"object","properties":{"path":{"type":"string","description":"Subdirectory path, empty for root"}}}`),
		func(ctx context.Context, args string) (string, error) {
			entries, err := r.gh.BrowseRepo(ctx, repo, branch)
			if err != nil {
				return "", err
			}
			var sb strings.Builder
			for _, e := range entries {
				if e.Type == "tree" {
					fmt.Fprintf(&sb, "📁 %s/\n", e.Path)
				} else {
					fmt.Fprintf(&sb, "📄 %s (%d bytes)\n", e.Path, e.Size)
				}
			}
			return sb.String(), nil
		})

	r.register("read_file", "Read file content from the repo. Returns content and SHA.",
		jsonSchema(`{"type":"object","properties":{"path":{"type":"string","description":"File path relative to repo root"},"ref":{"type":"string","description":"Branch or commit ref"}},"required":["path"]}`),
		func(ctx context.Context, args string) (string, error) {
			var p struct {
				Path string `json:"path"`
				Ref  string `json:"ref"`
			}
			json.Unmarshal([]byte(args), &p)
			ref := p.Ref
			if ref == "" {
				ref = branch
			}
			fc, err := r.gh.ReadFile(ctx, repo, p.Path, ref)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("SHA: %s\nSize: %d\n---\n%s", fc.SHA, fc.Size, fc.Content), nil
		})

	r.register("search_code", "Search for code patterns in the repository.",
		jsonSchema(`{"type":"object","properties":{"query":{"type":"string","description":"Search query"}},"required":["query"]}`),
		func(ctx context.Context, args string) (string, error) {
			var p struct{ Query string `json:"query"` }
			json.Unmarshal([]byte(args), &p)
			paths, err := r.gh.SearchCode(ctx, repo, p.Query)
			if err != nil {
				return "", err
			}
			return strings.Join(paths, "\n"), nil
		})
}

func (r *Registry) AddGitHubWriteTools(repo, baseBranch string) {
	r.register("create_branch", "Create a new branch from the base branch.",
		jsonSchema(`{"type":"object","properties":{"branch_name":{"type":"string","description":"New branch name, e.g. feat/FEAT-001-auth"}},"required":["branch_name"]}`),
		func(ctx context.Context, args string) (string, error) {
			var p struct{ BranchName string `json:"branch_name"` }
			json.Unmarshal([]byte(args), &p)
			err := r.gh.CreateBranch(ctx, repo, baseBranch, p.BranchName)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Branch '%s' created from '%s'", p.BranchName, baseBranch), nil
		})

	r.register("write_file", "Create or update a file. For updates, provide SHA from read_file.",
		jsonSchema(`{"type":"object","properties":{"path":{"type":"string"},"branch":{"type":"string"},"content":{"type":"string","description":"Complete file content"},"message":{"type":"string","description":"Commit message"},"sha":{"type":"string","description":"File SHA for updates, empty for new files"}},"required":["path","branch","content","message"]}`),
		func(ctx context.Context, args string) (string, error) {
			var p struct {
				Path    string `json:"path"`
				Branch  string `json:"branch"`
				Content string `json:"content"`
				Message string `json:"message"`
				SHA     string `json:"sha"`
			}
			json.Unmarshal([]byte(args), &p)
			if err := r.checkPathAllowed(p.Path); err != nil {
				return "", err
			}
			err := r.gh.WriteFile(ctx, repo, p.Path, p.Branch, p.Content, p.Message, p.SHA)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("File '%s' written to branch '%s'", p.Path, p.Branch), nil
		})

	r.register("multi_file_commit", "Commit multiple files in a single commit.",
		jsonSchema(`{"type":"object","properties":{"branch":{"type":"string"},"message":{"type":"string"},"files":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string","description":"File content, null to delete"}}}}},"required":["branch","message","files"]}`),
		func(ctx context.Context, args string) (string, error) {
			var p struct {
				Branch  string              `json:"branch"`
				Message string              `json:"message"`
				Files   []github.FileChange `json:"files"`
			}
			json.Unmarshal([]byte(args), &p)
			// Validate all file paths
			for _, f := range p.Files {
				if err := r.checkPathAllowed(f.Path); err != nil {
					return "", err
				}
			}
			sha, err := r.gh.MultiFileCommit(ctx, repo, p.Branch, p.Message, p.Files)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Committed %d files (sha: %s)", len(p.Files), sha[:8]), nil
		})

	r.register("create_pr", "Create a pull request.",
		jsonSchema(`{"type":"object","properties":{"title":{"type":"string"},"body":{"type":"string"},"head":{"type":"string","description":"Source branch"},"base":{"type":"string","description":"Target branch"}},"required":["title","body","head","base"]}`),
		func(ctx context.Context, args string) (string, error) {
			var p struct {
				Title string `json:"title"`
				Body  string `json:"body"`
				Head  string `json:"head"`
				Base  string `json:"base"`
			}
			json.Unmarshal([]byte(args), &p)
			pr, err := r.gh.CreatePR(ctx, repo, p.Title, p.Body, p.Head, p.Base)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("PR #%d created: %s", pr.Number, pr.URL), nil
		})
}

// ---- Sandbox tools (run inside an ephemeral Docker container) ----

// AddSandboxTools adds tools that execute commands inside the sandbox container.
// The sandbox is an ephemeral Docker container created per-task.
func (r *Registry) AddSandboxTools(githubToken string) {
	sb := r.sandbox
	if sb == nil {
		return
	}

	r.register("shell", "Execute a shell command in the ephemeral sandbox container. Use for build, test, lint, etc. The sandbox has the full toolchain installed.",
		jsonSchema(`{"type":"object","properties":{"command":{"type":"string","description":"Shell command to execute"},"workdir":{"type":"string","description":"Working directory inside sandbox, defaults to /workspace"}},"required":["command"]}`),
		func(ctx context.Context, args string) (string, error) {
			var p struct {
				Command string `json:"command"`
				Workdir string `json:"workdir"`
			}
			json.Unmarshal([]byte(args), &p)
			cmd := p.Command
			if p.Workdir != "" && p.Workdir != "/workspace" {
				cmd = fmt.Sprintf("cd %s && %s", p.Workdir, p.Command)
			}
			result, err := sb.Exec(ctx, cmd)
			if err != nil {
				// Return output even on error (useful for test failures, lint warnings)
				return result + "\nERROR: " + err.Error(), nil
			}
			return result, nil
		})

	r.register("git_clone", "Clone a repo into the sandbox's /workspace. Required before build/test commands.",
		jsonSchema(`{"type":"object","properties":{"repo":{"type":"string","description":"GitHub repo (owner/repo)"},"branch":{"type":"string","description":"Branch to checkout"}},"required":["repo"]}`),
		func(ctx context.Context, args string) (string, error) {
			var p struct {
				Repo   string `json:"repo"`
				Branch string `json:"branch"`
			}
			json.Unmarshal([]byte(args), &p)

			cloneCmd := fmt.Sprintf("git clone --depth=1 https://%s@github.com/%s.git /workspace/repo",
				githubToken, p.Repo)
			if p.Branch != "" {
				cloneCmd = fmt.Sprintf("git clone --depth=1 -b %s https://%s@github.com/%s.git /workspace/repo",
					p.Branch, githubToken, p.Repo)
			}

			result, err := sb.Exec(ctx, cloneCmd)
			if err != nil {
				// Redact token from error message
				sanitized := strings.ReplaceAll(result, githubToken, "***")
				return "", fmt.Errorf("git clone failed: %s", sanitized)
			}
			return "Cloned to /workspace/repo", nil
		})

	r.register("sandbox_write_file", "Write a file directly inside the sandbox container. Useful for creating new files before committing via GitHub API.",
		jsonSchema(`{"type":"object","properties":{"path":{"type":"string","description":"File path inside sandbox (e.g. /workspace/repo/src/main.go)"},"content":{"type":"string","description":"File content"}},"required":["path","content"]}`),
		func(ctx context.Context, args string) (string, error) {
			var p struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			json.Unmarshal([]byte(args), &p)

			if err := r.checkPathAllowed(p.Path); err != nil {
				return "", err
			}

			// Ensure parent directory exists
			sb.Exec(ctx, fmt.Sprintf("mkdir -p $(dirname %s)", p.Path))

			if err := sb.WriteFile(ctx, p.Path, p.Content); err != nil {
				return "", err
			}
			return fmt.Sprintf("Written %d bytes to %s", len(p.Content), p.Path), nil
		})

	r.register("sandbox_read_file", "Read a file from inside the sandbox container. Use this after git_clone to read local files. Shows line numbers for reference in edit operations.",
		jsonSchema(`{"type":"object","properties":{"path":{"type":"string","description":"File path inside sandbox"},"offset":{"type":"integer","description":"Start line number (0-based), default 0"},"limit":{"type":"integer","description":"Max lines to return, default 200"}},"required":["path"]}`),
		func(ctx context.Context, args string) (string, error) {
			var p struct {
				Path   string `json:"path"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			json.Unmarshal([]byte(args), &p)
			if p.Limit <= 0 {
				p.Limit = 200
			}
			// Use cat -n for line numbers, then sed for offset/limit
			cmd := fmt.Sprintf("cat -n %s | sed -n '%d,%dp'", p.Path, p.Offset+1, p.Offset+p.Limit)
			return sb.Exec(ctx, cmd)
		})

	// ---- Claude Code-inspired tools: Edit, Glob, Grep ----

	r.register("edit_file", "Make a precise edit to a file in the sandbox. Replaces old_string with new_string. The old_string MUST match exactly (including whitespace). Use sandbox_read_file first to see the exact content.",
		jsonSchema(`{"type":"object","properties":{"path":{"type":"string","description":"File path inside sandbox"},"old_string":{"type":"string","description":"Exact string to find and replace (must be unique in the file)"},"new_string":{"type":"string","description":"Replacement string"}},"required":["path","old_string","new_string"]}`),
		func(ctx context.Context, args string) (string, error) {
			var p struct {
				Path      string `json:"path"`
				OldString string `json:"old_string"`
				NewString string `json:"new_string"`
			}
			json.Unmarshal([]byte(args), &p)

			if err := r.checkPathAllowed(p.Path); err != nil {
				return "", err
			}

			// Read current file
			content, err := sb.ReadFile(ctx, p.Path)
			if err != nil {
				return "", fmt.Errorf("read file: %w", err)
			}

			// Count occurrences
			count := strings.Count(content, p.OldString)
			if count == 0 {
				return "", fmt.Errorf("old_string not found in %s. Use sandbox_read_file to check exact content", p.Path)
			}
			if count > 1 {
				return "", fmt.Errorf("old_string found %d times in %s. Provide more context to make it unique", count, p.Path)
			}

			// Replace
			newContent := strings.Replace(content, p.OldString, p.NewString, 1)
			if err := sb.WriteFile(ctx, p.Path, newContent); err != nil {
				return "", err
			}
			return fmt.Sprintf("Edited %s: replaced 1 occurrence", p.Path), nil
		})

	r.register("glob", "Find files by name pattern in the sandbox. Like Claude Code's Glob tool. Returns matching file paths.",
		jsonSchema(`{"type":"object","properties":{"pattern":{"type":"string","description":"Glob pattern, e.g. **/*.go, src/**/*.ts, *.json"},"path":{"type":"string","description":"Base directory to search from, defaults to /workspace/repo"}},"required":["pattern"]}`),
		func(ctx context.Context, args string) (string, error) {
			var p struct {
				Pattern string `json:"pattern"`
				Path    string `json:"path"`
			}
			json.Unmarshal([]byte(args), &p)
			if p.Path == "" {
				p.Path = "/workspace/repo"
			}
			cmd := fmt.Sprintf("find %s -path '%s/%s' -type f 2>/dev/null | head -50", p.Path, p.Path, p.Pattern)
			result, err := sb.Exec(ctx, cmd)
			if err != nil || result == "" {
				// Fallback to using shell globbing
				cmd = fmt.Sprintf("cd %s && ls -1 %s 2>/dev/null | head -50", p.Path, p.Pattern)
				result, _ = sb.Exec(ctx, cmd)
			}
			if result == "" {
				return "No files matched the pattern", nil
			}
			return result, nil
		})

	r.register("grep", "Search file contents by regex pattern in the sandbox. Like Claude Code's Grep tool. Returns matching lines with file paths and line numbers.",
		jsonSchema(`{"type":"object","properties":{"pattern":{"type":"string","description":"Regex pattern to search for"},"path":{"type":"string","description":"Directory or file to search in, defaults to /workspace/repo"},"glob":{"type":"string","description":"Filter files by glob pattern, e.g. *.go, *.ts"},"context":{"type":"integer","description":"Lines of context around matches, default 2"}},"required":["pattern"]}`),
		func(ctx context.Context, args string) (string, error) {
			var p struct {
				Pattern string `json:"pattern"`
				Path    string `json:"path"`
				Glob    string `json:"glob"`
				Context int    `json:"context"`
			}
			json.Unmarshal([]byte(args), &p)
			if p.Path == "" {
				p.Path = "/workspace/repo"
			}
			if p.Context <= 0 {
				p.Context = 2
			}

			cmd := fmt.Sprintf("grep -rn -C %d", p.Context)
			if p.Glob != "" {
				cmd += fmt.Sprintf(" --include='%s'", p.Glob)
			}
			cmd += fmt.Sprintf(" '%s' %s 2>/dev/null | head -100", p.Pattern, p.Path)

			result, err := sb.Exec(ctx, cmd)
			if err != nil && result == "" {
				return "No matches found", nil
			}
			if result == "" {
				return "No matches found", nil
			}
			return result, nil
		})
}

func (r *Registry) AddPRReviewTool(repo string) {
	r.register("get_pr_diff", "Get the diff of a pull request for review.",
		jsonSchema(`{"type":"object","properties":{"pr_number":{"type":"integer","description":"PR number"}},"required":["pr_number"]}`),
		func(ctx context.Context, args string) (string, error) {
			var p struct{ PRNumber int `json:"pr_number"` }
			json.Unmarshal([]byte(args), &p)
			diff, err := r.gh.GetPRDiff(ctx, repo, p.PRNumber)
			if err != nil {
				return "", err
			}
			if len(diff) > 15000 {
				diff = diff[:15000] + "\n... (truncated)"
			}
			return diff, nil
		})
}

func jsonSchema(s string) json.RawMessage {
	return json.RawMessage(s)
}
