package docker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Finsys/hawser/internal/log"
)

// validEnvKeyRegex matches safe environment variable names: letters, digits, underscores.
// Rejects dangerous keys like LD_PRELOAD, PATH, DOCKER_HOST etc. via denylist below.
var validEnvKeyRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// deniedEnvKeys are environment variable names that could be used for code execution
// or to redirect Docker operations to an attacker-controlled endpoint.
var deniedEnvKeys = map[string]bool{
	"LD_PRELOAD":      true,
	"LD_LIBRARY_PATH": true,
	"PATH":            true,
	"DOCKER_HOST":     true,
	"DOCKER_CONFIG":   true,
	"DOCKER_CERT_PATH": true,
	"DOCKER_TLS_VERIFY": true,
	"DOCKER_CONTEXT":  true,
	"HOME":            true,
	"SHELL":           true,
	"BASH_ENV":        true,
	"ENV":             true,
	"CDPATH":          true,
	"IFS":             true,
}

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
	Operation       string                `json:"operation"` // up, down, pull, ps, logs
	ProjectName     string                `json:"projectName"`
	WorkDir         string                `json:"workDir"`
	ComposeFile     string                `json:"composeFile,omitempty"`     // Content of compose file
	ComposeFileName string                `json:"composeFileName,omitempty"` // Explicit compose filename to use (e.g., "docker-compose.prod.yml")
	Files           map[string]string     `json:"files,omitempty"`           // All files to write (relative path -> content)
	Services        []string              `json:"services,omitempty"`        // Specific services to operate on
	Options         map[string]string     `json:"options,omitempty"`         // Additional options
	EnvVars         map[string]string     `json:"envVars,omitempty"`         // Environment variables for variable substitution
	Registries      []RegistryCredentials `json:"registries,omitempty"`      // Registry credentials for docker login
	ForceRecreate   bool                  `json:"forceRecreate,omitempty"`   // Force recreation of containers (--force-recreate)
	RemoveVolumes   bool                  `json:"removeVolumes,omitempty"`   // Remove volumes on down (--volumes)
	ServiceName     string                `json:"serviceName,omitempty"`     // Target specific service only (with --no-deps)
	Build           bool                  `json:"build,omitempty"`           // Build images before starting (--build)
	NoBuildCache    bool                  `json:"noBuildCache,omitempty"`    // Build without cache (--no-cache)
	PullPolicy      string                `json:"pullPolicy,omitempty"`      // Pull policy: 'always' | 'missing' | 'never'
	FilesToDelete   []FileToDelete        `json:"filesToDelete,omitempty"`   // Git deletion sync (#966): hash-verified file removals
	RemoveFiles     bool                  `json:"removeFiles,omitempty"`     // On down: remove the stack directory entirely (#1162, stack deletion only)
}

// ComposeResult is the result of a compose operation
type ComposeResult struct {
	Success  bool   `json:"success"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
	ExitCode int    `json:"exitCode"`
	// Git deletion sync report. Every requested FilesToDelete entry appears in
	// exactly one of these — Dockhand uses their presence to detect support.
	DeletedFiles []string      `json:"deletedFiles,omitempty"`
	SkippedFiles []SkippedFile `json:"skippedFiles,omitempty"`
}

// dockerEnv builds a minimal, clean environment for invoking the docker CLI,
// so host vars (DOCKER_CONTEXT, DOCKER_TLS_VERIFY, ...) can't redirect docker.
// dockerConfigDir, when set, is where credentials are read from / written to.
func (c *ComposeClient) dockerEnv(dockerConfigDir string) []string {
	env := []string{fmt.Sprintf("DOCKER_HOST=unix://%s", c.dockerSocket)}
	if dockerConfigDir != "" {
		env = append(env, "DOCKER_CONFIG="+dockerConfigDir)
	}
	for _, key := range []string{"PATH", "HOME", "USER"} {
		if val, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+val)
		}
	}
	if c.apiVersion != "" {
		env = append(env, "DOCKER_API_VERSION="+c.apiVersion)
	}
	return env
}

// loginToRegistries runs `docker login` for each registry before a compose
// up/pull. dockerConfigDir is the writable Docker config the credentials are
// stored in; it must match the one passed to the compose command (see Execute).
func (c *ComposeClient) loginToRegistries(ctx context.Context, registries []RegistryCredentials, dockerConfigDir string) {
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
		cmd.Env = c.dockerEnv(dockerConfigDir)
		cmd.Stdin = strings.NewReader(reg.Password)

		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			// Surface at warn level: a failed login here is the usual cause of a
			// later "unauthorized" pull, and is otherwise silently swallowed.
			log.Warnf("Compose: Failed to login to %s: %s", registryHost, strings.TrimSpace(stderr.String()))
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

	// Use an ephemeral Docker config under stacksDir so login and compose share
	// credentials without writing to $HOME (unwritable under ProtectHome=true).
	var dockerConfigDir string
	if (op.Operation == "up" || op.Operation == "pull") && len(op.Registries) > 0 && c.stacksDir != "" {
		if err := os.MkdirAll(c.stacksDir, 0755); err != nil {
			log.Warnf("Compose: could not create stacks dir %s, registry auth may fail: %v", c.stacksDir, err)
		} else if dir, err := os.MkdirTemp(c.stacksDir, ".docker-"); err == nil {
			dockerConfigDir = dir
			defer os.RemoveAll(dockerConfigDir)
		} else {
			log.Warnf("Compose: could not create docker config dir, registry auth may fail: %v", err)
		}
	}

	// Login to registries before up/pull operations
	if op.Operation == "up" || op.Operation == "pull" {
		c.loginToRegistries(ctx, op.Registries, dockerConfigDir)
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

		// Resolve stackDir to absolute path for path traversal validation
		absStackDir, err := filepath.Abs(stackDir)
		if err != nil {
			return &ComposeResult{
				Success:  false,
				Error:    fmt.Sprintf("Failed to resolve stack directory: %v", err),
				ExitCode: 1,
			}, nil
		}
		stackDir = absStackDir

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

			// Path traversal protection: ensure resolved path stays within stackDir
			absFilePath, err := filepath.Abs(filePath)
			if err != nil {
				return &ComposeResult{
					Success:  false,
					Error:    fmt.Sprintf("Failed to resolve path for %s: %v", relPath, err),
					ExitCode: 1,
				}, nil
			}
			if !strings.HasPrefix(absFilePath, stackDir+string(os.PathSeparator)) && absFilePath != stackDir {
				return &ComposeResult{
					Success:  false,
					Error:    fmt.Sprintf("Path traversal rejected: %s escapes stack directory", relPath),
					ExitCode: 1,
				}, nil
			}

			// Create parent directories if needed
			if dir := filepath.Dir(absFilePath); dir != stackDir {
				if err := os.MkdirAll(dir, 0755); err != nil {
					return &ComposeResult{
						Success:  false,
						Error:    fmt.Sprintf("Failed to create directory for %s: %v", relPath, err),
						ExitCode: 1,
					}, nil
				}
			}

			// Decode content: base64-prefixed values contain binary data
			var fileBytes []byte
			if strings.HasPrefix(content, "base64:") {
				decoded, err := base64.StdEncoding.DecodeString(content[7:])
				if err != nil {
					return &ComposeResult{
						Success:  false,
						Error:    fmt.Sprintf("Failed to decode base64 content for %s: %v", relPath, err),
						ExitCode: 1,
					}, nil
				}
				fileBytes = decoded
			} else {
				fileBytes = []byte(content)
			}

			// Write file using validated absolute path
			if err := os.WriteFile(absFilePath, fileBytes, 0644); err != nil {
				return &ComposeResult{
					Success:  false,
					Error:    fmt.Sprintf("Failed to write file %s: %v", relPath, err),
					ExitCode: 1,
				}, nil
			}
			log.Debugf("Compose: Wrote file %s to %s", relPath, absFilePath)
		}

		log.Debugf("Compose: Wrote %d files to %s", len(op.Files), stackDir)

		// Determine compose file name:
		// 1. If ComposeFileName is explicitly provided, use it
		// 2. Otherwise, auto-detect from standard filenames
		composeFileName := ""
		if op.ComposeFileName != "" {
			// Explicit compose filename provided - use it directly
			composeFileName = op.ComposeFileName
			log.Debugf("Compose: Using explicit compose filename: %s", composeFileName)
		} else {
			// Auto-detect compose file from written files
			for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
				if _, exists := op.Files[name]; exists {
					composeFileName = name
					break
				}
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

	// Git deletion sync (#966): remove files that were deleted from the git
	// repository. Runs before compose up so removed config files are not
	// mounted. Every entry is hash-verified and containment-checked by the
	// applier — user data and locally modified files are never touched.
	var deletedFiles []string
	var skippedFiles []SkippedFile
	if op.Operation == "up" && len(op.FilesToDelete) > 0 {
		delDir := stackDir
		if delDir == "" && c.stacksDir != "" && op.ProjectName != "" {
			if abs, err := filepath.Abs(filepath.Join(c.stacksDir, op.ProjectName)); err == nil {
				delDir = abs
			}
		}
		if delDir != "" {
			deletedFiles, skippedFiles = applyFileDeletions(delDir, op.FilesToDelete, false)
		} else {
			for _, f := range op.FilesToDelete {
				skippedFiles = append(skippedFiles, SkippedFile{Path: f.Path, Reason: "apply-failed"})
			}
			log.Warnf("Deletion sync: no stack directory available, %d deletion(s) deferred", len(op.FilesToDelete))
		}
	}

	// Add env files from the stack directory.
	// Order matters: .env first (base repo values), .env.dockhand second (user overrides).
	// Later --env-file entries override earlier ones in Docker Compose.
	if stackDir != "" {
		envPath := filepath.Join(stackDir, ".env")
		if _, err := os.Stat(envPath); err == nil {
			args = append(args, "--env-file", envPath)
			log.Debugf("Compose: Adding --env-file %s", envPath)
		}
		envDockhandPath := filepath.Join(stackDir, ".env.dockhand")
		if _, err := os.Stat(envDockhandPath); err == nil {
			args = append(args, "--env-file", envDockhandPath)
			log.Debugf("Compose: Adding --env-file %s", envDockhandPath)
		}
	}

	// Add operation-specific arguments
	switch op.Operation {
	case "up":
		args = append(args, "up", "-d", "--remove-orphans")
		if op.Build {
			args = append(args, "--build")
		}
		if op.NoBuildCache {
			args = append(args, "--no-cache")
		}
		if op.PullPolicy != "" {
			args = append(args, "--pull", op.PullPolicy)
		}
		if op.ForceRecreate {
			args = append(args, "--force-recreate")
		}
		// If targeting a specific service, add --no-deps to avoid affecting other services
		if op.ServiceName != "" {
			if strings.HasPrefix(op.ServiceName, "-") {
				return &ComposeResult{
					Success:  false,
					Error:    fmt.Sprintf("Invalid service name: %q", op.ServiceName),
					ExitCode: 1,
				}, nil
			}
			args = append(args, "--no-deps", op.ServiceName)
		}
	case "down":
		args = append(args, "down", "--remove-orphans")
		if op.RemoveVolumes {
			args = append(args, "--volumes")
		}
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

	// Add specific services if specified (legacy field for backward compatibility)
	// Reject values starting with "-" to prevent flag injection
	for _, svc := range op.Services {
		if strings.HasPrefix(svc, "-") {
			return &ComposeResult{
				Success:  false,
				Error:    fmt.Sprintf("Invalid service name: %q", svc),
				ExitCode: 1,
			}, nil
		}
		args = append(args, svc)
	}

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

	// Clean environment so host vars and the stack's own EnvVars (below) can't
	// redirect docker. dockerConfigDir carries the credentials written by
	// loginToRegistries; the user EnvVars loop rejects DOCKER_CONFIG
	// (see deniedEnvKeys) and so cannot override it.
	cmd.Env = c.dockerEnv(dockerConfigDir)
	if c.apiVersion != "" {
		log.Debugf("Compose: Using API version %s", c.apiVersion)
	}

	// Add environment variables for compose variable substitution
	for key, value := range op.EnvVars {
		// Validate env var key format (alphanumeric + underscore only)
		if !validEnvKeyRegex.MatchString(key) {
			return &ComposeResult{
				Success:  false,
				Error:    fmt.Sprintf("Invalid environment variable name: %q", key),
				ExitCode: 1,
			}, nil
		}
		// Block dangerous env vars that could enable code execution or redirect Docker
		if deniedEnvKeys[strings.ToUpper(key)] {
			log.Warnf("Compose: Blocked dangerous environment variable: %s", key)
			continue
		}
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
		Success:      err == nil,
		Output:       stdout.String(),
		ExitCode:     0,
		DeletedFiles: deletedFiles,
		SkippedFiles: skippedFiles,
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

	// Stack deletion (#1162): after a successful down with removeFiles, delete
	// EXACTLY the files Dockhand listed — hash-verified, containment-checked —
	// then remove emptied directories (non-recursive). The agent never decides
	// what to delete on its own: an empty list deletes nothing, files modified
	// on this host are kept, and the stack directory itself disappears only
	// when nothing else (e.g. volume data) lives in it.
	// A plain "down" (removeFiles=false) never touches files.
	if op.Operation == "down" && result.Success && op.RemoveFiles && c.stacksDir != "" && op.ProjectName != "" {
		base, baseErr := filepath.Abs(c.stacksDir)
		dir, dirErr := filepath.Abs(filepath.Join(c.stacksDir, op.ProjectName))
		if baseErr == nil && dirErr == nil && dir != base && strings.HasPrefix(dir, base+string(os.PathSeparator)) {
			if len(op.FilesToDelete) > 0 {
				removed, kept := applyFileDeletions(dir, op.FilesToDelete, true)
				result.DeletedFiles = removed
				result.SkippedFiles = kept
			}
			if rmErr := os.Remove(dir); rmErr == nil {
				log.Infof("Removed stack directory %s (stack deleted)", dir)
			} else if _, statErr := os.Stat(dir); statErr == nil {
				log.Infof("Stack directory kept (contains files not written by Dockhand): %s", dir)
			}
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
