package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Config holds all configuration for the Hawser agent
type Config struct {
	// Edge Mode (active connection to Dockhand)
	DockhandServerURL string // e.g., wss://dockhand.example.com/api/hawser/connect
	Token             string // Agent authentication token

	// Standard Mode (passive HTTP server)
	Port    int    // Default: 2375
	TLSCert string // Optional TLS certificate path
	TLSKey  string // Optional TLS key path

	// Docker connection
	DockerSocket string // Default: /var/run/docker.sock
	DockerHost   string // Alternative: tcp://localhost:2375

	// Agent identification
	AgentID   string // Auto-generated UUID if not set
	AgentName string // Human-readable name

	// Timeouts and intervals (seconds)
	HeartbeatInterval int // Default: 30
	RequestTimeout    int // Default: 30
	ReconnectDelay    int // Initial reconnect delay, default: 1
	MaxReconnectDelay int // Max reconnect delay, default: 60
}

// Load reads configuration from environment variables and flags
func Load() (*Config, error) {
	cfg := &Config{
		// Edge mode
		DockhandServerURL: os.Getenv("DOCKHAND_SERVER_URL"),
		Token:             os.Getenv("TOKEN"),

		// Standard mode
		Port:    getEnvInt("PORT", 2375),
		TLSCert: os.Getenv("TLS_CERT"),
		TLSKey:  os.Getenv("TLS_KEY"),

		// Docker
		DockerSocket: getEnvString("DOCKER_SOCKET", detectDockerSocket()),
		DockerHost:   os.Getenv("DOCKER_HOST"),

		// Agent identification
		AgentID:   getEnvString("AGENT_ID", generateAgentID()),
		AgentName: getEnvString("AGENT_NAME", getHostname()),

		// Timeouts
		HeartbeatInterval: getEnvInt("HEARTBEAT_INTERVAL", 30),
		RequestTimeout:    getEnvInt("REQUEST_TIMEOUT", 30),
		ReconnectDelay:    getEnvInt("RECONNECT_DELAY", 1),
		MaxReconnectDelay: getEnvInt("MAX_RECONNECT_DELAY", 60),
	}

	// Validate configuration
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// EdgeMode returns true if the agent should run in edge mode
func (c *Config) EdgeMode() bool {
	return c.DockhandServerURL != "" && c.Token != ""
}

// TLSEnabled returns true if TLS is configured for standard mode
func (c *Config) TLSEnabled() bool {
	return c.TLSCert != "" && c.TLSKey != ""
}

// GetDockerEndpoint returns the Docker endpoint to connect to
func (c *Config) GetDockerEndpoint() string {
	if c.DockerHost != "" {
		return c.DockerHost
	}
	return "unix://" + c.DockerSocket
}

func (c *Config) validate() error {
	// If edge mode, validate URL and token
	if c.DockhandServerURL != "" {
		if c.Token == "" {
			return fmt.Errorf("TOKEN is required when DOCKHAND_SERVER_URL is set")
		}
		if !strings.HasPrefix(c.DockhandServerURL, "ws://") && !strings.HasPrefix(c.DockhandServerURL, "wss://") {
			return fmt.Errorf("DOCKHAND_SERVER_URL must start with ws:// or wss://")
		}
	}

	// Validate TLS configuration
	if (c.TLSCert != "" && c.TLSKey == "") || (c.TLSCert == "" && c.TLSKey != "") {
		return fmt.Errorf("both TLS_CERT and TLS_KEY must be set together")
	}

	// Validate port
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("PORT must be between 1 and 65535")
	}

	// Validate docker socket exists (if using socket)
	if c.DockerHost == "" {
		if _, err := os.Stat(c.DockerSocket); os.IsNotExist(err) {
			return fmt.Errorf("Docker socket not found at %s", c.DockerSocket)
		}
	}

	return nil
}

func detectDockerSocket() string {
	// Check common socket paths
	paths := []string{
		"/var/run/docker.sock",           // Standard Linux
		os.Getenv("HOME") + "/.docker/run/docker.sock", // Docker Desktop Mac
		os.Getenv("HOME") + "/.orbstack/run/docker.sock", // OrbStack
		"/run/docker.sock",               // Alternative Linux
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	// Default to standard path even if not found (will fail validation)
	return "/var/run/docker.sock"
}

func generateAgentID() string {
	return uuid.New().String()
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "hawser-agent"
	}
	return hostname
}

func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}
