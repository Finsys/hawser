package config

import (
	"fmt"
	"net/netip"
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
	CACert            string // Optional CA certificate path (for self-signed Dockhand)
	TLSSkipVerify     bool   // Skip TLS verification (insecure, for testing)

	// Standard Mode (passive HTTP server)
	Port                int    // Default: 2376
	BindAddress         string // Default: 0.0.0.0 (all interfaces)
	TLSCert             string // Optional TLS certificate path
	TLSKey              string // Optional TLS key path
	AllowInsecureNoAuth bool   // Allow standard mode to bind a non-loopback address without a token (insecure)

	// Docker connection
	DockerSocket string // Default: /var/run/docker.sock
	DockerHost   string // Alternative: tcp://localhost:2375

	// Agent identification
	AgentID   string // Auto-generated UUID if not set
	AgentName string // Human-readable name

	// Timeouts and intervals (seconds)
	HeartbeatInterval int // Default: 30
	RequestTimeout    int // Default: 30
	ComposeTimeout    int // Default: 900 (compose operations can run far longer than a normal request)
	ReconnectDelay    int // Initial reconnect delay, default: 1
	MaxReconnectDelay int // Max reconnect delay, default: 60
	WelcomeTimeout    int // Timeout waiting for welcome message after hello, default: 30

	// Logging
	LogLevel string // debug, info, warn, error. Default: info

	// Stack files directory
	StacksDir string // Directory for stack files, default: /data/stacks

	// Version info (set by main.go from ldflags)
	Version string
	Commit  string
}

// Load reads configuration from environment variables and flags
func Load() (*Config, error) {
	cfg := &Config{
		// Edge mode
		DockhandServerURL: os.Getenv("DOCKHAND_SERVER_URL"),
		Token:             os.Getenv("TOKEN"),
		CACert:            os.Getenv("CA_CERT"),
		TLSSkipVerify:     getEnvBool("TLS_SKIP_VERIFY", false),

		// Standard mode
		Port:                getEnvInt("PORT", 2376),
		BindAddress:         getEnvString("BIND_ADDRESS", "0.0.0.0"),
		TLSCert:             os.Getenv("TLS_CERT"),
		TLSKey:              os.Getenv("TLS_KEY"),
		AllowInsecureNoAuth: getEnvBool("ALLOW_INSECURE_NO_AUTH", false),

		// Docker
		DockerSocket: getEnvString("DOCKER_SOCKET", detectDockerSocket()),
		DockerHost:   os.Getenv("DOCKER_HOST"),

		// Agent identification
		AgentID:   getEnvString("AGENT_ID", generateAgentID()),
		AgentName: getEnvString("AGENT_NAME", getHostname()),

		// Timeouts
		HeartbeatInterval: getEnvInt("HEARTBEAT_INTERVAL", 30),
		RequestTimeout:    getEnvInt("REQUEST_TIMEOUT", 30),
		ComposeTimeout:    getEnvInt("COMPOSE_TIMEOUT", 900),
		ReconnectDelay:    getEnvInt("RECONNECT_DELAY", 1),
		MaxReconnectDelay: getEnvInt("MAX_RECONNECT_DELAY", 60),
		WelcomeTimeout:    getEnvInt("WELCOME_TIMEOUT", 30),

		// Logging
		LogLevel: getEnvString("LOG_LEVEL", "info"),

		// Stack files directory
		StacksDir: getEnvString("STACKS_DIR", "/data/stacks"),
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
	} else {
		// Standard mode: refuse to expose an unauthenticated Docker proxy on a
		// reachable address. Without a token the HTTP server forwards every
		// request straight to the Docker socket, so a non-loopback bind grants
		// root-equivalent access to anyone who can reach the port.
		if c.Token == "" && !isLoopbackBind(c.BindAddress) && !c.AllowInsecureNoAuth {
			return fmt.Errorf(
				"refusing to start: standard mode is bound to %q with no TOKEN set, which exposes an unauthenticated Docker API to the network. "+
					"Set TOKEN, bind to a loopback address (BIND_ADDRESS=127.0.0.1), or explicitly set ALLOW_INSECURE_NO_AUTH=true to override",
				c.BindAddress)
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

// isLoopbackBind reports whether a standard-mode bind address only accepts
// connections from the local host. An empty address binds all interfaces and is
// treated as non-loopback. Hostnames other than "localhost" cannot be resolved
// safely here and are treated as non-loopback (fail closed).
func isLoopbackBind(addr string) bool {
	if addr == "" {
		return false
	}
	if addr == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return false
	}
	return ip.IsLoopback()
}

func detectDockerSocket() string {
	// Check common socket paths
	paths := []string{
		"/var/run/docker.sock",                           // Standard Linux
		os.Getenv("HOME") + "/.docker/run/docker.sock",   // Docker Desktop Mac
		os.Getenv("HOME") + "/.orbstack/run/docker.sock", // OrbStack
		"/run/docker.sock",                               // Alternative Linux
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

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		switch strings.ToLower(value) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return defaultValue
}
