package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Finsys/hawser/internal/config"
	"github.com/Finsys/hawser/internal/docker"
)

// Server represents the Standard mode HTTP server
type Server struct {
	cfg          *config.Config
	dockerClient *docker.Client
	httpServer   *http.Server
}

// Run starts the Standard mode HTTP server
func Run(cfg *config.Config, stop <-chan os.Signal) error {
	// Create Docker client
	dockerClient, err := docker.NewClient(cfg.DockerSocket)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer dockerClient.Close()

	// Get Docker version for logging
	version, err := dockerClient.GetVersion(context.Background())
	if err != nil {
		log.Printf("Warning: could not get Docker version: %v", err)
	} else {
		log.Printf("Connected to Docker %s (API %s)", version.Version, version.APIVersion)
	}

	server := &Server{
		cfg:          cfg,
		dockerClient: dockerClient,
	}

	// Create HTTP handler
	mux := http.NewServeMux()
	mux.HandleFunc("/", server.handleProxy)
	mux.HandleFunc("/_hawser/health", server.handleHealth)
	mux.HandleFunc("/_hawser/info", server.handleInfo)

	// Wrap with middleware
	handler := server.authMiddleware(mux)
	handler = server.loggingMiddleware(handler)

	// Configure server
	addr := fmt.Sprintf(":%d", cfg.Port)
	server.httpServer = &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // No timeout for streaming responses
		IdleTimeout:  60 * time.Second,
	}

	// Start server
	errChan := make(chan error, 1)
	go func() {
		if cfg.TLSEnabled() {
			log.Printf("Starting HTTPS server on %s", addr)
			cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
			if err != nil {
				errChan <- fmt.Errorf("failed to load TLS certificates: %w", err)
				return
			}
			server.httpServer.TLSConfig = &tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
			}
			errChan <- server.httpServer.ListenAndServeTLS("", "")
		} else {
			log.Printf("Starting HTTP server on %s", addr)
			errChan <- server.httpServer.ListenAndServe()
		}
	}()

	// Wait for stop signal or error
	select {
	case <-stop:
		log.Println("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.httpServer.Shutdown(ctx)
	case err := <-errChan:
		if err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

// handleProxy proxies requests to the Docker API
func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Build headers for Docker request
	headers := make(map[string]string)
	for key, values := range r.Header {
		if len(values) > 0 && !isHopByHopHeader(key) {
			headers[key] = values[0]
		}
	}

	// Make request to Docker
	resp, err := s.dockerClient.RequestRaw(ctx, r.Method, r.URL.RequestURI(), headers, r.Body)
	if err != nil {
		log.Printf("Docker request failed: %v", err)
		http.Error(w, "Docker request failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write status code
	w.WriteHeader(resp.StatusCode)

	// Check if this is a streaming response
	if isStreamingRequest(r.URL.Path, r.Method) {
		// Handle streaming response
		s.streamResponse(w, resp.Body)
	} else {
		// Copy response body
		io.Copy(w, resp.Body)
	}
}

// streamResponse handles streaming Docker responses
func (s *Server) streamResponse(w http.ResponseWriter, body io.Reader) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		io.Copy(w, body)
		return
	}

	buf := make([]byte, 4096)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			flusher.Flush()
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("Stream error: %v", err)
			}
			return
		}
	}
}

// handleHealth returns health status
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Check Docker connectivity
	if err := s.dockerClient.Ping(r.Context()); err != nil {
		http.Error(w, "Docker unhealthy: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"healthy"}`))
}

// handleInfo returns agent information
func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	version, _ := s.dockerClient.GetVersion(r.Context())

	dockerVersion := "unknown"
	if version != nil {
		dockerVersion = version.Version
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"agentId":"%s","agentName":"%s","dockerVersion":"%s","mode":"standard"}`,
		s.cfg.AgentID, s.cfg.AgentName, dockerVersion)
}

// authMiddleware checks for valid token if configured
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for health endpoint
		if r.URL.Path == "/_hawser/health" {
			next.ServeHTTP(w, r)
			return
		}

		// If token is configured, require it
		if s.cfg.Token != "" {
			token := r.Header.Get("X-Hawser-Token")
			if token == "" {
				token = r.URL.Query().Get("token")
			}

			if token != s.cfg.Token {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs requests
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("--> %s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
		log.Printf("<-- %s %s (%s)", r.Method, r.URL.Path, time.Since(start))
	})
}

// isStreamingRequest checks if the request expects a streaming response
func isStreamingRequest(path, method string) bool {
	// Container logs
	if strings.Contains(path, "/logs") && method == "GET" {
		return true
	}
	// Container attach
	if strings.Contains(path, "/attach") {
		return true
	}
	// Exec start with stream
	if strings.Contains(path, "/exec/") && strings.Contains(path, "/start") {
		return true
	}
	// Events
	if strings.HasSuffix(path, "/events") {
		return true
	}
	// Build
	if strings.Contains(path, "/build") && method == "POST" {
		return true
	}
	// Pull/push images
	if (strings.Contains(path, "/images/create") || strings.Contains(path, "/images/push")) && method == "POST" {
		return true
	}
	return false
}

// isHopByHopHeader checks if a header is hop-by-hop
func isHopByHopHeader(header string) bool {
	hopByHop := []string{
		"Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailers",
		"Transfer-Encoding",
		"Upgrade",
	}
	for _, h := range hopByHop {
		if strings.EqualFold(header, h) {
			return true
		}
	}
	return false
}
