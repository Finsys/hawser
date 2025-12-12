package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Finsys/hawser/internal/log"
)

// ComposeClient handles Docker Compose operations
type ComposeClient struct {
	dockerSocket  string
	composeCmd    string   // "docker" for v2, "docker-compose" for v1
	composeArgs   []string // ["compose"] for v2, [] for v1
	composeChecked bool
}

// NewComposeClient creates a new Compose client
func NewComposeClient(dockerSocket string) *ComposeClient {
	return &ComposeClient{
		dockerSocket: dockerSocket,
	}
}

// detectComposeCommand checks which compose command is available
// Tries docker compose (v2) first, then docker-compose (v1)
func (c *ComposeClient) detectComposeCommand() error {
	if c.composeChecked {
		return nil
	}

	// Try docker compose (v2) first
	cmd := exec.Command("docker", "compose", "version")
	if err := cmd.Run(); err == nil {
		c.composeCmd = "docker"
		c.composeArgs = []string{"compose"}
		c.composeChecked = true
		log.Debugf("Using docker compose (v2)")
		return nil
	}

	// Try docker-compose (v1)
	cmd = exec.Command("docker-compose", "version")
	if err := cmd.Run(); err == nil {
		c.composeCmd = "docker-compose"
		c.composeArgs = []string{}
		c.composeChecked = true
		log.Debugf("Using docker-compose (v1)")
		return nil
	}

	return fmt.Errorf("Docker Compose is not installed. Please install either 'docker compose' (v2) or 'docker-compose' (v1)")
}

// ComposeOperation represents a compose operation request
type ComposeOperation struct {
	Operation   string            `json:"operation"` // up, down, pull, ps, logs
	ProjectName string            `json:"projectName"`
	WorkDir     string            `json:"workDir"`
	ComposeFile string            `json:"composeFile,omitempty"` // Content of compose file
	Services    []string          `json:"services,omitempty"`    // Specific services to operate on
	Options     map[string]string `json:"options,omitempty"`     // Additional options
}

// ComposeResult is the result of a compose operation
type ComposeResult struct {
	Success  bool   `json:"success"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
	ExitCode int    `json:"exitCode"`
}

// Execute runs a Docker Compose operation
func (c *ComposeClient) Execute(ctx context.Context, op *ComposeOperation) (*ComposeResult, error) {
	// Detect compose command on first use
	if err := c.detectComposeCommand(); err != nil {
		return &ComposeResult{
			Success:  false,
			Error:    err.Error(),
			ExitCode: 1,
		}, nil
	}

	// Build command arguments
	args := []string{}

	// Add project name if specified
	if op.ProjectName != "" {
		args = append(args, "-p", op.ProjectName)
	}

	// Handle compose file content
	var tempFile string
	if op.ComposeFile != "" {
		// Try multiple directories for temp file (some environments have read-only /tmp)
		tempDirs := []string{
			os.TempDir(),
			".",
			os.Getenv("HOME"),
		}

		var writeErr error
		for _, dir := range tempDirs {
			if dir == "" {
				continue
			}
			tempFile = filepath.Join(dir, fmt.Sprintf("hawser-compose-%s.yml", op.ProjectName))
			if writeErr = os.WriteFile(tempFile, []byte(op.ComposeFile), 0644); writeErr == nil {
				break
			}
			log.Debugf("Failed to write compose file to %s: %v", dir, writeErr)
		}
		if writeErr != nil {
			return nil, fmt.Errorf("failed to write compose file (tried %v): %w", tempDirs, writeErr)
		}
		defer os.Remove(tempFile)
		args = append(args, "-f", tempFile)
	}

	// Add operation-specific arguments
	switch op.Operation {
	case "up":
		args = append(args, "up", "-d", "--remove-orphans")
	case "down":
		args = append(args, "down", "--remove-orphans")
	case "pull":
		args = append(args, "pull")
	case "ps":
		args = append(args, "ps", "--format", "json")
	case "logs":
		args = append(args, "logs", "--tail", "100")
		if tail, ok := op.Options["tail"]; ok {
			args[len(args)-1] = tail
		}
	case "restart":
		args = append(args, "restart")
	case "stop":
		args = append(args, "stop")
	case "start":
		args = append(args, "start")
	default:
		return nil, fmt.Errorf("unsupported compose operation: %s", op.Operation)
	}

	// Add specific services if specified
	args = append(args, op.Services...)

	// Build full command args: composeArgs + args
	fullArgs := append(c.composeArgs, args...)

	// Execute compose command
	cmd := exec.CommandContext(ctx, c.composeCmd, fullArgs...)

	// Set working directory
	if op.WorkDir != "" {
		cmd.Dir = op.WorkDir
	}

	// Set Docker socket environment
	cmd.Env = append(os.Environ(), fmt.Sprintf("DOCKER_HOST=unix://%s", c.dockerSocket))

	// Log the command being executed
	log.Debugf("Compose: %s %s (project=%s)", c.composeCmd, strings.Join(fullArgs, " "), op.ProjectName)

	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := &ComposeResult{
		Success:  err == nil,
		Output:   stdout.String(),
		ExitCode: 0,
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
		result.Error = stderr.String()
		if result.Error == "" {
			result.Error = err.Error()
		}
		log.Debugf("Compose failed: exit=%d error=%s", result.ExitCode, result.Error)
	} else {
		log.Debugf("Compose completed: %s (project=%s)", op.Operation, op.ProjectName)
	}

	// For ps command, include stderr in output if it contains JSON
	if op.Operation == "ps" && stderr.Len() > 0 {
		// Check if stderr contains valid JSON (compose sometimes outputs to stderr)
		if strings.HasPrefix(strings.TrimSpace(stderr.String()), "[") {
			result.Output = stderr.String()
		}
	}

	return result, nil
}

// ParseComposePS parses the JSON output of docker compose ps
func ParseComposePS(output string) ([]ComposeService, error) {
	var services []ComposeService
	if err := json.Unmarshal([]byte(output), &services); err != nil {
		return nil, err
	}
	return services, nil
}

// ComposeService represents a service from docker compose ps
type ComposeService struct {
	ID         string   `json:"ID"`
	Name       string   `json:"Name"`
	Service    string   `json:"Service"`
	State      string   `json:"State"`
	Status     string   `json:"Status"`
	Health     string   `json:"Health,omitempty"`
	Image      string   `json:"Image"`
	Publishers []string `json:"Publishers,omitempty"`
}

// IsAvailable checks if docker compose is available
func (c *ComposeClient) IsAvailable() bool {
	return c.detectComposeCommand() == nil
}

// GetVersion returns docker compose version
func (c *ComposeClient) GetVersion() (string, error) {
	if err := c.detectComposeCommand(); err != nil {
		return "", err
	}

	var cmd *exec.Cmd
	if c.composeCmd == "docker" {
		cmd = exec.Command("docker", "compose", "version", "--short")
	} else {
		cmd = exec.Command("docker-compose", "version", "--short")
	}
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
