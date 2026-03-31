package sandbox

// Pre-built sandbox images for each agent type.
// These are the runtime environments, NOT the worker images.
//
// Worker container (long-running, lightweight):
//   - Polls Temporal task queue
//   - Receives task
//   - Spins up ephemeral sandbox container
//   - Orchestrates LLM ↔ sandbox tool calls
//   - Destroys sandbox when done
//
// Sandbox container (ephemeral, per-task):
//   - Full toolchain installed
//   - Clean /workspace every time
//   - Destroyed after task completes

const (
	ImageGolang   = "specflow-sandbox-golang:latest"
	ImageNestJS   = "specflow-sandbox-nestjs:latest"
	ImageFrontend = "specflow-sandbox-frontend:latest"
	ImageQA       = "specflow-sandbox-qa:latest"
)

// AgentTypeToImage maps agent types to their sandbox Docker images.
func AgentTypeToImage(agentType string) string {
	switch agentType {
	case "golang":
		return ImageGolang
	case "nestjs":
		return ImageNestJS
	case "frontend":
		return ImageFrontend
	case "qa":
		return ImageQA
	default:
		return ImageGolang
	}
}
