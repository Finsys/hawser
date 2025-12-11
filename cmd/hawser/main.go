package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Finsys/hawser/internal/config"
	"github.com/Finsys/hawser/internal/edge"
	"github.com/Finsys/hawser/internal/server"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

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
		fmt.Printf("Starting in Edge mode, connecting to %s\n", cfg.DockhandServerURL)
		go func() {
			errChan <- edge.Run(cfg, stop)
		}()
	} else {
		// Standard mode: listen for incoming connections
		fmt.Printf("Starting in Standard mode on port %d\n", cfg.Port)
		go func() {
			errChan <- server.Run(cfg, stop)
		}()
	}

	// Wait for shutdown signal or error
	select {
	case <-stop:
		fmt.Println("\nShutdown signal received, stopping...")
	case err := <-errChan:
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Println("Hawser stopped")
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
	fmt.Println()
}
