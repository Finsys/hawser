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
)

// ComposeClient handles Docker Compose operations
type ComposeClient struct {
	dockerSocket string
}

// NewComposeClient creates a new Compose client
func NewComposeClient(dockerSocket string) *ComposeClient {
	return &ComposeClient{
		dockerSocket: dockerSocket,
	}
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
	// Build command arguments
	args := []string{}

	// Add project name if specified
	if op.ProjectName != "" {
		args = append(args, "-p", op.ProjectName)
	}

	// Handle compose file content
	var tempFile string
	if op.ComposeFile != "" {
		// Write compose content to temp file
		tempDir := os.TempDir()
		tempFile = filepath.Join(tempDir, fmt.Sprintf("hawser-compose-%s.yml", op.ProjectName))
		if err := os.WriteFile(tempFile, []byte(op.ComposeFile), 0644); err != nil {
			return nil, fmt.Errorf("failed to write compose file: %w", err)
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

	// Execute docker compose command
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...)

	// Set working directory
	if op.WorkDir != "" {
		cmd.Dir = op.WorkDir
	}

	// Set Docker socket environment
	cmd.Env = append(os.Environ(), fmt.Sprintf("DOCKER_HOST=unix://%s", c.dockerSocket))

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
	cmd := exec.Command("docker", "compose", "version")
	return cmd.Run() == nil
}

// GetVersion returns docker compose version
func (c *ComposeClient) GetVersion() (string, error) {
	cmd := exec.Command("docker", "compose", "version", "--short")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
