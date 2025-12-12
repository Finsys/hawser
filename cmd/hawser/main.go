package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Finsys/hawser/internal/config"
	"github.com/Finsys/hawser/internal/edge"
	"github.com/Finsys/hawser/internal/log"
	"github.com/Finsys/hawser/internal/server"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	// Define flags
	showHelp := flag.Bool("help", false, "Show help message")
	showVersion := flag.Bool("version", false, "Show version information")

	// Parse flags but allow unknown flags for subcommands like "standard"
	flag.CommandLine.SetOutput(os.Stdout)
	flag.Parse()

	if *showHelp {
		printHelp()
		os.Exit(0)
	}

	if *showVersion {
		fmt.Printf("hawser version %s (%s)\n", version, commit)
		os.Exit(0)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger before anything else
	log.Init(cfg.LogLevel)

	// Set version info from ldflags
	cfg.Version = version
	cfg.Commit = commit

	// Print startup banner
	printBanner(cfg)

	// Setup graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	errChan := make(chan error, 1)

	if cfg.EdgeMode() {
		// Edge mode: connect outbound to Dockhand server
		log.Infof("Starting in Edge mode, connecting to %s", cfg.DockhandServerURL)
		go func() {
			errChan <- edge.Run(cfg, stop)
		}()
	} else {
		// Standard mode: listen for incoming connections
		log.Infof("Starting in Standard mode on port %d", cfg.Port)
		go func() {
			errChan <- server.Run(cfg, stop)
		}()
	}

	// Wait for shutdown signal or error
	select {
	case <-stop:
		log.Info("Shutdown signal received, stopping...")
	case err := <-errChan:
		if err != nil {
			log.Errorf("Error: %v", err)
			os.Exit(1)
		}
	}

	log.Info("Hawser stopped")
}

func printBanner(cfg *config.Config) {
	fmt.Println("╭─────────────────────────────────────╮")
	fmt.Println("│           HAWSER AGENT              │")
	fmt.Println("│     Remote Docker Agent for         │")
	fmt.Println("│           Dockhand                  │")
	fmt.Println("╰─────────────────────────────────────╯")
	fmt.Printf("Version: %s (%s)\n", version, commit)
	fmt.Printf("Agent ID: %s\n", cfg.AgentID)
	fmt.Printf("Agent Name: %s\n", cfg.AgentName)
	fmt.Printf("Docker Socket: %s\n", cfg.DockerSocket)
	fmt.Printf("Log Level: %s\n", log.GetLevel())
	fmt.Println()
}

func printHelp() {
	fmt.Println("Hawser - Remote Docker Agent for Dockhand")
	fmt.Printf("Version: %s (%s)\n\n", version, commit)
	fmt.Println("USAGE:")
	fmt.Println("  hawser [OPTIONS]")
	fmt.Println("  hawser standard [OPTIONS]    Run in Standard mode (HTTP server)")
	fmt.Println()
	fmt.Println("OPTIONS:")
	fmt.Println("  --help       Show this help message")
	fmt.Println("  --version    Show version information")
	fmt.Println()
	fmt.Println("STANDARD MODE OPTIONS:")
	fmt.Println("  --port PORT  Port to listen on (default: 2376)")
	fmt.Println()
	fmt.Println("ENVIRONMENT VARIABLES:")
	fmt.Println("  Edge Mode (connects outbound to Dockhand):")
	fmt.Println("    DOCKHAND_SERVER_URL  WebSocket URL (e.g., wss://dockhand.example.com/api/hawser/connect)")
	fmt.Println("    TOKEN                Authentication token from Dockhand")
	fmt.Println()
	fmt.Println("  Standard Mode (listens for incoming connections):")
	fmt.Println("    PORT        Port to listen on (default: 2376)")
	fmt.Println("    TOKEN       Optional authentication token")
	fmt.Println("    TLS_CERT    Path to TLS certificate")
	fmt.Println("    TLS_KEY     Path to TLS private key")
	fmt.Println()
	fmt.Println("  Common:")
	fmt.Println("    DOCKER_SOCKET   Path to Docker socket (default: /var/run/docker.sock)")
	fmt.Println("    AGENT_NAME      Human-readable name for this agent")
	fmt.Println("    LOG_LEVEL       Logging level: debug, info, warn, error (default: info)")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  # Standard mode on default port")
	fmt.Println("  hawser standard")
	fmt.Println()
	fmt.Println("  # Standard mode with custom port and token")
	fmt.Println("  TOKEN=secret hawser standard --port 2375")
	fmt.Println()
	fmt.Println("  # Edge mode")
	fmt.Println("  DOCKHAND_SERVER_URL=wss://dockhand.example.com/api/hawser/connect \\")
	fmt.Println("  TOKEN=your-token \\")
	fmt.Println("  hawser")
	fmt.Println()
	fmt.Println("For more information, visit: https://github.com/Finsys/hawser")
}
