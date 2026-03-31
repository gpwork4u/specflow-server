package sandbox

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Sandbox represents an ephemeral Docker container for one task execution.
// Each task gets a fresh container → clean environment → no state leakage.
type Sandbox struct {
	ContainerID string
	Image       string
	Name        string
	WorkDir     string
	mu          sync.Mutex
}

// Config for creating a new sandbox.
type Config struct {
	// Docker image to use (e.g. "specflow-golang-sandbox:latest")
	Image string

	// Unique name for this sandbox container
	Name string

	// Environment variables to inject
	Env map[string]string

	// Network to connect to (for accessing Ollama, etc.)
	Network string

	// Timeout for the entire sandbox lifecycle
	Timeout time.Duration
}

// Create spins up a new ephemeral Docker container.
// The container stays running until Destroy() is called.
func Create(ctx context.Context, cfg Config) (*Sandbox, error) {
	args := []string{
		"run", "-d",
		"--name", cfg.Name,
		"--workdir", "/workspace",
		// Auto-remove when stopped
		"--rm",
		// Resource limits per sandbox
		"--memory", "2g",
		"--cpus", "2",
	}

	// Network
	if cfg.Network != "" {
		args = append(args, "--network", cfg.Network)
	}

	// Environment variables
	for k, v := range cfg.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	// Keep container alive with a sleep process
	args = append(args, cfg.Image, "sleep", "infinity")

	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker run: %s %w", string(out), err)
	}

	containerID := strings.TrimSpace(string(out))

	return &Sandbox{
		ContainerID: containerID,
		Image:       cfg.Image,
		Name:        cfg.Name,
		WorkDir:     "/workspace",
	}, nil
}

// Exec runs a command inside the sandbox container.
// Returns stdout+stderr combined output.
func (s *Sandbox) Exec(ctx context.Context, command string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cmd := exec.CommandContext(ctx, "docker", "exec", s.ContainerID, "sh", "-c", command)
	out, err := cmd.CombinedOutput()
	result := string(out)

	// Truncate very large output
	if len(result) > 20000 {
		result = result[:20000] + "\n... (truncated)"
	}

	if err != nil {
		return result, fmt.Errorf("exec: %s (exit: %w)", result, err)
	}
	return result, nil
}

// WriteFile writes content to a file inside the sandbox.
func (s *Sandbox) WriteFile(ctx context.Context, path, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Use docker cp via stdin
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", s.ContainerID,
		"sh", "-c", fmt.Sprintf("cat > %s", path))
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("write file %s: %s %w", path, string(out), err)
	}
	return nil
}

// ReadFile reads a file from inside the sandbox.
func (s *Sandbox) ReadFile(ctx context.Context, path string) (string, error) {
	return s.Exec(ctx, fmt.Sprintf("cat %s", path))
}

// CopyOut copies a file from the sandbox to the host.
func (s *Sandbox) CopyOut(ctx context.Context, containerPath, hostPath string) error {
	cmd := exec.CommandContext(ctx, "docker", "cp",
		fmt.Sprintf("%s:%s", s.ContainerID, containerPath), hostPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy out: %s %w", string(out), err)
	}
	return nil
}

// Logs returns the container logs.
func (s *Sandbox) Logs(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "logs", s.ContainerID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}

// Destroy stops and removes the container.
// Since we use --rm, stopping is enough.
func (s *Sandbox) Destroy(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "stop", "-t", "5", s.ContainerID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Force kill if graceful stop fails
		exec.CommandContext(ctx, "docker", "rm", "-f", s.ContainerID).Run()
		return fmt.Errorf("destroy: %s %w", string(out), err)
	}
	return nil
}

// Ensure io.Reader is available for WriteFile stdin
var _ io.Reader = (*strings.Reader)(nil)
