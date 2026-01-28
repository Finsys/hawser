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
	dockerSocket   string
	composeCmd     string   // "docker" for v2, "docker-compose" for v1
	composeArgs    []string // ["compose"] for v2, [] for v1
	composeChecked bool
	apiVersion     string // Docker API version to use (for version negotiation)
	stacksDir      string // Base directory for stack files
}

// NewComposeClient creates a new Compose client
func NewComposeClient(dockerSocket, stacksDir string) *ComposeClient {
	return &ComposeClient{
		dockerSocket: dockerSocket,
		stacksDir:    stacksDir,
	}
}

// SetAPIVersion sets the Docker API version to use for compose commands.
// This enables compatibility when the docker CLI version differs from the daemon.
func (c *ComposeClient) SetAPIVersion(version string) {
	c.apiVersion = version
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

// RegistryCredentials holds credentials for a Docker registry
type RegistryCredentials struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// ComposeOperation represents a compose operation request
type ComposeOperation struct {
	Operation   string                `json:"operation"` // up, down, pull, ps, logs
	ProjectName string                `json:"projectName"`
	WorkDir     string                `json:"workDir"`
	ComposeFile string                `json:"composeFile,omitempty"` // Content of compose file
	Files       map[string]string     `json:"files,omitempty"`       // All files to write (relative path -> content)
	Services    []string              `json:"services,omitempty"`    // Specific services to operate on
	Options     map[string]string     `json:"options,omitempty"`     // Additional options
	EnvVars     map[string]string     `json:"envVars,omitempty"`     // Environment variables for variable substitution
	Registries  []RegistryCredentials `json:"registries,omitempty"`  // Registry credentials for docker login
}

// ComposeResult is the result of a compose operation
type ComposeResult struct {
	Success  bool   `json:"success"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
	ExitCode int    `json:"exitCode"`
}

// loginToRegistries logs into all provided registries before compose operations
func (c *ComposeClient) loginToRegistries(ctx context.Context, registries []RegistryCredentials) {
	if len(registries) == 0 {
		return
	}

	for _, reg := range registries {
		if reg.Username == "" || reg.Password == "" {
			continue
		}

		// Extract host from URL
		var registryHost string
		if strings.HasPrefix(reg.URL, "http://") || strings.HasPrefix(reg.URL, "https://") {
			// Parse as URL to extract host
			parts := strings.SplitN(reg.URL, "://", 2)
			if len(parts) == 2 {
				registryHost = strings.Split(parts[1], "/")[0]
			}
		} else {
			registryHost = reg.URL
		}

		if registryHost == "" {
			log.Debugf("Compose: Skipping registry with empty host: %s", reg.URL)
			continue
		}

		log.Debugf("Compose: Logging into registry %s", registryHost)

		cmd := exec.CommandContext(ctx, "docker", "login", "-u", reg.Username, "--password-stdin", registryHost)
		cmd.Env = append(os.Environ(), fmt.Sprintf("DOCKER_HOST=unix://%s", c.dockerSocket))
		cmd.Stdin = strings.NewReader(reg.Password)

		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			log.Debugf("Compose: Failed to login to %s: %s", registryHost, stderr.String())
		} else {
			log.Debugf("Compose: Successfully logged into %s", registryHost)
		}
	}
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

	// Login to registries before up/pull operations
	if op.Operation == "up" || op.Operation == "pull" {
		c.loginToRegistries(ctx, op.Registries)
	}

	// Build command arguments
	args := []string{}

	// Add project name if specified
	if op.ProjectName != "" {
		args = append(args, "-p", op.ProjectName)
	}

	// Determine if we should use file-based approach or stdin
	var stdinContent string
	var stackDir string

	if len(op.Files) > 0 && c.stacksDir != "" {
		// NEW: File-based approach - write all files to stack directory
		stackDir = filepath.Join(c.stacksDir, op.ProjectName)

		// Create stack directory
		if err := os.MkdirAll(stackDir, 0755); err != nil {
			return &ComposeResult{
				Success:  false,
				Error:    fmt.Sprintf("Failed to create stack directory %s: %v. Ensure STACKS_DIR points to a writable path.", stackDir, err),
				ExitCode: 1,
			}, nil
		}

		// Write all files
		for relPath, content := range op.Files {
			filePath := filepath.Join(stackDir, relPath)

			// Create parent directories if needed
			if dir := filepath.Dir(filePath); dir != stackDir {
				if err := os.MkdirAll(dir, 0755); err != nil {
					return &ComposeResult{
						Success:  false,
						Error:    fmt.Sprintf("Failed to create directory for %s: %v", relPath, err),
						ExitCode: 1,
					}, nil
				}
			}

			// Write file
			if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
				return &ComposeResult{
					Success:  false,
					Error:    fmt.Sprintf("Failed to write file %s: %v", relPath, err),
					ExitCode: 1,
				}, nil
			}
			log.Debugf("Compose: Wrote file %s to %s", relPath, filePath)
		}

		log.Debugf("Compose: Wrote %d files to %s", len(op.Files), stackDir)

		// Find the compose file in the written files
		composeFileName := ""
		for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
			if _, exists := op.Files[name]; exists {
				composeFileName = name
				break
			}
		}

		if composeFileName != "" {
			args = append(args, "-f", filepath.Join(stackDir, composeFileName))
		} else if op.ComposeFile != "" {
			// Fallback: write compose content to docker-compose.yml
			composePath := filepath.Join(stackDir, "docker-compose.yml")
			if err := os.WriteFile(composePath, []byte(op.ComposeFile), 0644); err != nil {
				return &ComposeResult{
					Success:  false,
					Error:    fmt.Sprintf("Failed to write compose file: %v", err),
					ExitCode: 1,
				}, nil
			}
			args = append(args, "-f", composePath)
		}
	} else if op.ComposeFile != "" {
		// LEGACY: stdin-based approach (no files provided)
		stdinContent = op.ComposeFile
		args = append(args, "-f", "-")
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

	// Set working directory (use stackDir if files were written, otherwise use WorkDir)
	if stackDir != "" {
		cmd.Dir = stackDir
	} else if op.WorkDir != "" {
		cmd.Dir = op.WorkDir
	}

	// Set Docker socket environment
	cmd.Env = append(os.Environ(), fmt.Sprintf("DOCKER_HOST=unix://%s", c.dockerSocket))

	// Set API version for compatibility with newer Docker daemons
	// This allows older docker CLI to work with newer daemons
	if c.apiVersion != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("DOCKER_API_VERSION=%s", c.apiVersion))
		log.Debugf("Compose: Using API version %s", c.apiVersion)
	}

	// Add environment variables for compose variable substitution
	for key, value := range op.EnvVars {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}

	// Log the command being executed
	log.Debugf("Compose: %s %s (project=%s)", c.composeCmd, strings.Join(fullArgs, " "), op.ProjectName)

	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Pipe compose content via stdin if provided
	if stdinContent != "" {
		cmd.Stdin = strings.NewReader(stdinContent)
	}

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
		result.Error = strings.TrimSpace(stderr.String())
		if result.Error == "" {
			result.Error = err.Error()
		}
		log.Errorf("Compose failed: exit=%d error=%s", result.ExitCode, result.Error)
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
