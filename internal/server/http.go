package server

import (
	"bufio"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Finsys/hawser/internal/config"
	"github.com/Finsys/hawser/internal/docker"
	"github.com/Finsys/hawser/internal/log"
	"github.com/Finsys/hawser/internal/pool"
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
		log.Warnf("Could not get Docker version: %v", err)
	} else {
		log.Infof("Connected to Docker %s (API %s)", version.Version, version.APIVersion)
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
			log.Infof("Starting HTTPS server on %s", addr)
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
			log.Infof("Starting HTTP server on %s", addr)
			errChan <- server.httpServer.ListenAndServe()
		}
	}()

	// Wait for stop signal or error
	select {
	case <-stop:
		log.Info("Shutting down server...")
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
	// Check if this is an exec/start request that needs hijacking
	if isExecStartRequest(r.URL.Path, r.Method) {
		s.handleExecHijack(w, r)
		return
	}

	// Check if this is an events request - use raw socket for immediate header forwarding
	if isEventsRequest(r.URL.Path, r.Method) {
		s.handleEventsStream(w, r)
		return
	}

	ctx := r.Context()

	// Build headers for Docker request
	headers := make(map[string]string)
	for key, values := range r.Header {
		if len(values) > 0 && !isHopByHopHeader(key) {
			headers[key] = values[0]
		}
	}

	// Make request to Docker - use StreamRequest for streaming endpoints (no timeout)
	var resp *http.Response
	var err error
	if isStreamingRequest(r.URL.Path, r.Method) {
		resp, err = s.dockerClient.StreamRequest(ctx, r.Method, r.URL.RequestURI(), headers, r.Body)
	} else {
		resp, err = s.dockerClient.RequestRaw(ctx, r.Method, r.URL.RequestURI(), headers, r.Body)
	}
	if err != nil {
		// Don't log context canceled as error - client disconnected
		if errors.Is(err, context.Canceled) {
			return
		}
		log.Errorf("Docker request failed: %v", err)
		http.Error(w, "Docker request failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Debug log the Docker API call
	log.Debugf("Docker API: %s %s -> %d", r.Method, r.URL.RequestURI(), resp.StatusCode)
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
		// Flush headers immediately so clients know the connection is established
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		// Handle streaming response
		s.streamResponse(w, resp.Body)
	} else {
		// Copy response body
		io.Copy(w, resp.Body)
	}
}

// handleExecHijack handles exec/start requests
// For non-interactive exec (no stdin), returns normal HTTP response with output
// For interactive exec (with stdin from websocket), uses connection hijacking
func (s *Server) handleExecHijack(w http.ResponseWriter, r *http.Request) {
	// Read the request body first
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Open raw connection to Docker socket
	dockerConn, err := net.Dial("unix", s.cfg.DockerSocket)
	if err != nil {
		http.Error(w, "Failed to connect to Docker: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer dockerConn.Close()

	// Build and send the HTTP request to Docker
	// Include Upgrade headers for connection hijacking
	reqStr := fmt.Sprintf("%s %s HTTP/1.1\r\n", r.Method, r.URL.RequestURI())
	reqStr += "Host: localhost\r\n"
	reqStr += "Connection: Upgrade\r\n"
	reqStr += "Upgrade: tcp\r\n"
	reqStr += fmt.Sprintf("Content-Type: %s\r\n", r.Header.Get("Content-Type"))
	reqStr += fmt.Sprintf("Content-Length: %d\r\n", len(body))
	reqStr += "\r\n"

	// Send headers
	if _, err := dockerConn.Write([]byte(reqStr)); err != nil {
		http.Error(w, "Failed to send request to Docker: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Send body
	if _, err := dockerConn.Write(body); err != nil {
		http.Error(w, "Failed to send body to Docker: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Read the response status line and headers
	reader := bufio.NewReader(dockerConn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		http.Error(w, "Failed to read Docker response: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Parse status code
	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 2 {
		http.Error(w, "Invalid response from Docker", http.StatusBadGateway)
		return
	}

	statusCode := 200
	fmt.Sscanf(parts[1], "%d", &statusCode)

	// Read headers until empty line
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			http.Error(w, "Failed to read Docker headers: "+err.Error(), http.StatusBadGateway)
			return
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	// Check if this is a WebSocket upgrade request (interactive terminal)
	// If not, just return the exec output as a normal HTTP response
	if r.Header.Get("Upgrade") != "websocket" && r.Header.Get("Connection") != "Upgrade" {
		// Non-interactive exec: return output as normal HTTP response
		w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
		w.WriteHeader(http.StatusOK)

		// Flush any buffered data from reader
		if reader.Buffered() > 0 {
			buffered := make([]byte, reader.Buffered())
			reader.Read(buffered)
			w.Write(buffered)
		}

		// Copy remaining data from Docker
		io.Copy(w, dockerConn)
		return
	}

	// Interactive exec: hijack the connection for bidirectional streaming
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "Failed to hijack connection: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Disable Nagle's algorithm for lower latency on interactive terminal
	if tcpConn, ok := clientConn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
	}

	// Send response to client
	responseStr := fmt.Sprintf("HTTP/1.1 %d %s\r\n", statusCode, http.StatusText(statusCode))
	responseStr += "Content-Type: application/vnd.docker.raw-stream\r\n"
	responseStr += "Connection: Upgrade\r\n"
	responseStr += "Upgrade: tcp\r\n"
	responseStr += "\r\n"
	clientConn.Write([]byte(responseStr))

	// Flush any buffered data from reader to client
	if reader.Buffered() > 0 {
		buffered := make([]byte, reader.Buffered())
		reader.Read(buffered)
		clientConn.Write(buffered)
	}

	// Bidirectional copy
	done := make(chan struct{}, 2)

	// Docker -> Client
	go func() {
		io.Copy(clientConn, dockerConn)
		done <- struct{}{}
	}()

	// Client -> Docker (also flush any buffered client data first)
	go func() {
		if clientBuf.Reader.Buffered() > 0 {
			io.CopyN(dockerConn, clientBuf, int64(clientBuf.Reader.Buffered()))
		}
		io.Copy(dockerConn, clientConn)
		done <- struct{}{}
	}()

	// Wait for either direction to close
	<-done
}

// handleEventsStream handles /events requests using raw sockets with proper HTTP parsing
// Uses http.ReadResponse to correctly handle chunked transfer encoding from Docker
func (s *Server) handleEventsStream(w http.ResponseWriter, r *http.Request) {

	// Open raw connection to Docker socket
	dockerConn, err := net.Dial("unix", s.cfg.DockerSocket)
	if err != nil {
		http.Error(w, "Failed to connect to Docker: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer dockerConn.Close()

	// Build the HTTP request to Docker
	dockerReq, err := http.NewRequest(r.Method, "http://localhost"+r.URL.RequestURI(), nil)
	if err != nil {
		http.Error(w, "Failed to create request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	dockerReq.Header.Set("Accept", "application/json")

	// Write the request to the Docker socket
	if err := dockerReq.Write(dockerConn); err != nil {
		http.Error(w, "Failed to send request to Docker: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Read the response using Go's HTTP parser (handles chunked encoding automatically)
	reader := bufio.NewReader(dockerConn)
	resp, err := http.ReadResponse(reader, dockerReq)
	if err != nil {
		http.Error(w, "Failed to read Docker response: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy headers to our response (except Transfer-Encoding which Go handles)
	for key, values := range resp.Header {
		if key != "Transfer-Encoding" {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
	}

	// Set chunked encoding for streaming
	w.Header().Set("Transfer-Encoding", "chunked")

	// Write status code
	w.WriteHeader(resp.StatusCode)

	// Get flusher for streaming
	flusher, ok := w.(http.Flusher)
	if ok {
		flusher.Flush() // Flush headers immediately
	}

	// Stream the body using pooled buffer (resp.Body automatically decodes chunked encoding)
	bufPtr := pool.GetBuffer()
	defer pool.PutBuffer(bufPtr)
	buf := *bufPtr

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if ok {
				flusher.Flush()
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Errorf("Events stream error: %v", err)
			}
			return
		}
	}
}

// streamResponse handles streaming Docker responses
func (s *Server) streamResponse(w http.ResponseWriter, body io.Reader) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		io.Copy(w, body)
		return
	}

	// Use pooled buffer for streaming
	bufPtr := pool.GetBuffer()
	defer pool.PutBuffer(bufPtr)
	buf := *bufPtr

	for {
		n, err := body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			flusher.Flush()
		}
		if err != nil {
			// Don't log EOF or context canceled as errors - these are normal terminations
			if err != io.EOF && !errors.Is(err, context.Canceled) {
				log.Errorf("Stream error: %v", err)
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

	hawserVersion := s.cfg.Version
	if hawserVersion == "" {
		hawserVersion = "dev"
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"agentId":"%s","agentName":"%s","dockerVersion":"%s","hawserVersion":"%s","mode":"standard"}`,
		s.cfg.AgentID, s.cfg.AgentName, dockerVersion, hawserVersion)
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

			// Use constant-time comparison to prevent timing attacks
			if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Token)) != 1 {
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
		log.Debugf("--> %s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
		log.Debugf("<-- %s %s (%s)", r.Method, r.URL.Path, time.Since(start))
	})
}

// isExecStartRequest checks if this is an exec/start request that needs hijacking
func isExecStartRequest(path, method string) bool {
	return method == "POST" && strings.Contains(path, "/exec/") && strings.Contains(path, "/start")
}

// isEventsRequest checks if this is a /events request
func isEventsRequest(path, method string) bool {
	// Note: r.URL.Path never includes query string, so only HasSuffix is needed
	return method == "GET" && strings.HasSuffix(path, "/events")
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
