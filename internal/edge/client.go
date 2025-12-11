package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Finsys/hawser/internal/config"
	"github.com/Finsys/hawser/internal/docker"
	"github.com/Finsys/hawser/internal/metrics"
	"github.com/Finsys/hawser/internal/protocol"
	"github.com/gorilla/websocket"
)

// Client represents the Edge mode WebSocket client
type Client struct {
	cfg          *config.Config
	dockerClient *docker.Client
	compose      *docker.ComposeClient
	metrics      *metrics.Collector
	conn         *websocket.Conn
	mu           sync.Mutex
	stop         <-chan os.Signal

	// Active streams for exec/logs
	streams   map[string]*StreamContext
	streamsMu sync.RWMutex
}

// StreamContext tracks an active streaming request
type StreamContext struct {
	RequestID string
	Cancel    context.CancelFunc
	Writer    io.Writer
}

// Run starts the Edge mode client with auto-reconnect
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

	client := &Client{
		cfg:          cfg,
		dockerClient: dockerClient,
		compose:      docker.NewComposeClient(cfg.DockerSocket),
		metrics:      metrics.NewCollector(dockerClient),
		stop:         stop,
		streams:      make(map[string]*StreamContext),
	}

	return client.runWithReconnect()
}

// runWithReconnect implements auto-reconnect with exponential backoff
func (c *Client) runWithReconnect() error {
	backoff := time.Duration(c.cfg.ReconnectDelay) * time.Second
	maxBackoff := time.Duration(c.cfg.MaxReconnectDelay) * time.Second

	for {
		select {
		case <-c.stop:
			return nil
		default:
		}

		err := c.connect()
		if err == nil {
			backoff = time.Duration(c.cfg.ReconnectDelay) * time.Second
			c.run()
		} else {
			log.Printf("Connection failed: %v", err)
		}

		// Check if we should stop
		select {
		case <-c.stop:
			return nil
		default:
		}

		log.Printf("Reconnecting in %v...", backoff)
		select {
		case <-time.After(backoff):
		case <-c.stop:
			return nil
		}

		// Exponential backoff
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// connect establishes WebSocket connection to Dockhand
func (c *Client) connect() error {
	log.Printf("Connecting to %s", c.cfg.DockhandServerURL)

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.Dial(c.cfg.DockhandServerURL, nil)
	if err != nil {
		return fmt.Errorf("WebSocket dial failed: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	// Send hello message
	if err := c.sendHello(); err != nil {
		conn.Close()
		return fmt.Errorf("failed to send hello: %w", err)
	}

	// Wait for welcome message
	if err := c.waitForWelcome(); err != nil {
		conn.Close()
		return fmt.Errorf("failed to receive welcome: %w", err)
	}

	log.Println("Connected to Dockhand server")
	return nil
}

// sendHello sends the hello message to Dockhand
func (c *Client) sendHello() error {
	version, _ := c.dockerClient.GetVersion(context.Background())
	dockerVersion := "unknown"
	if version != nil {
		dockerVersion = version.Version
	}

	hostname, _ := os.Hostname()

	capabilities := []string{protocol.CapabilityExec, protocol.CapabilityMetrics}
	if c.compose.IsAvailable() {
		capabilities = append(capabilities, protocol.CapabilityCompose)
	}

	hello := protocol.NewHelloMessage(
		c.cfg.AgentID,
		c.cfg.AgentName,
		c.cfg.Token,
		dockerVersion,
		hostname,
		capabilities,
	)

	return c.sendJSON(hello)
}

// waitForWelcome waits for the welcome message from Dockhand
func (c *Client) waitForWelcome() error {
	c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer c.conn.SetReadDeadline(time.Time{})

	_, data, err := c.conn.ReadMessage()
	if err != nil {
		return err
	}

	msgType, err := protocol.ParseMessageType(data)
	if err != nil {
		return err
	}

	if msgType == protocol.TypeError {
		var errMsg protocol.ErrorMessage
		json.Unmarshal(data, &errMsg)
		return fmt.Errorf("server error: %s", errMsg.Error)
	}

	if msgType != protocol.TypeWelcome {
		return fmt.Errorf("expected welcome message, got %s", msgType)
	}

	var welcome protocol.WelcomeMessage
	if err := json.Unmarshal(data, &welcome); err != nil {
		return err
	}

	log.Printf("Welcome received, environment ID: %d", welcome.EnvironmentID)
	return nil
}

// run handles the main message loop
func (c *Client) run() {
	done := make(chan struct{})

	// Start heartbeat
	go c.heartbeatLoop(done)

	// Start metrics sender
	go c.metricsLoop(done)

	// Message loop
	for {
		select {
		case <-c.stop:
			close(done)
			c.close()
			return
		default:
		}

		_, data, err := c.conn.ReadMessage()
		if err != nil {
			log.Printf("Read error: %v", err)
			close(done)
			return
		}

		go c.handleMessage(data)
	}
}

// handleMessage processes incoming messages
func (c *Client) handleMessage(data []byte) {
	msgType, err := protocol.ParseMessageType(data)
	if err != nil {
		log.Printf("Failed to parse message: %v", err)
		return
	}

	switch msgType {
	case protocol.TypeRequest:
		var req protocol.RequestMessage
		if err := json.Unmarshal(data, &req); err != nil {
			log.Printf("Failed to parse request: %v", err)
			return
		}
		c.handleRequest(&req)

	case protocol.TypePing:
		var ping protocol.PingMessage
		if err := json.Unmarshal(data, &ping); err != nil {
			return
		}
		c.sendJSON(protocol.NewPongMessage(time.Now().Unix()))

	case protocol.TypeStreamEnd:
		var end protocol.StreamEndMessage
		if err := json.Unmarshal(data, &end); err != nil {
			return
		}
		c.cancelStream(end.RequestID)

	default:
		log.Printf("Unknown message type: %s", msgType)
	}
}

// handleRequest processes Docker API requests
func (c *Client) handleRequest(req *protocol.RequestMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.cfg.RequestTimeout)*time.Second)
	defer cancel()

	// Check if this is a compose operation
	if req.Path == "/_hawser/compose" {
		c.handleComposeRequest(ctx, req)
		return
	}

	// Build headers
	headers := make(map[string]string)
	for k, v := range req.Headers {
		headers[k] = v
	}

	// Make Docker request
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}

	if req.Streaming {
		c.handleStreamingRequest(ctx, req, headers)
		return
	}

	// Regular request
	resp, err := c.dockerClient.RequestRaw(ctx, req.Method, req.Path, headers, body)
	if err != nil {
		c.sendJSON(protocol.NewErrorMessage(req.RequestID, err.Error(), "DOCKER_ERROR"))
		return
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.sendJSON(protocol.NewErrorMessage(req.RequestID, err.Error(), "READ_ERROR"))
		return
	}

	// Build response headers
	respHeaders := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			respHeaders[k] = v[0]
		}
	}

	// Send response
	c.sendJSON(protocol.NewResponseMessage(req.RequestID, resp.StatusCode, respHeaders, respBody))
}

// handleStreamingRequest handles streaming Docker responses
func (c *Client) handleStreamingRequest(ctx context.Context, req *protocol.RequestMessage, headers map[string]string) {
	ctx, cancel := context.WithCancel(ctx)

	// Register stream
	c.streamsMu.Lock()
	c.streams[req.RequestID] = &StreamContext{
		RequestID: req.RequestID,
		Cancel:    cancel,
	}
	c.streamsMu.Unlock()

	defer func() {
		c.streamsMu.Lock()
		delete(c.streams, req.RequestID)
		c.streamsMu.Unlock()
		c.sendJSON(protocol.NewStreamEndMessage(req.RequestID, "completed"))
	}()

	resp, err := c.dockerClient.StreamRequest(ctx, req.Method, req.Path, headers, nil)
	if err != nil {
		c.sendJSON(protocol.NewErrorMessage(req.RequestID, err.Error(), "DOCKER_ERROR"))
		return
	}
	defer resp.Body.Close()

	// Stream data back
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := resp.Body.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			c.sendJSON(protocol.NewStreamMessage(req.RequestID, data, ""))
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("Stream read error: %v", err)
			}
			return
		}
	}
}

// handleComposeRequest handles Docker Compose operations
func (c *Client) handleComposeRequest(ctx context.Context, req *protocol.RequestMessage) {
	var op docker.ComposeOperation
	if err := json.Unmarshal(req.Body, &op); err != nil {
		c.sendJSON(protocol.NewErrorMessage(req.RequestID, err.Error(), "PARSE_ERROR"))
		return
	}

	result, err := c.compose.Execute(ctx, &op)
	if err != nil {
		c.sendJSON(protocol.NewErrorMessage(req.RequestID, err.Error(), "COMPOSE_ERROR"))
		return
	}

	respBody, _ := json.Marshal(result)
	statusCode := http.StatusOK
	if !result.Success {
		statusCode = http.StatusInternalServerError
	}

	c.sendJSON(protocol.NewResponseMessage(req.RequestID, statusCode, nil, respBody))
}

// cancelStream cancels an active stream
func (c *Client) cancelStream(requestID string) {
	c.streamsMu.RLock()
	stream, ok := c.streams[requestID]
	c.streamsMu.RUnlock()

	if ok && stream.Cancel != nil {
		stream.Cancel()
	}
}

// heartbeatLoop sends periodic heartbeats
func (c *Client) heartbeatLoop(done <-chan struct{}) {
	ticker := time.NewTicker(time.Duration(c.cfg.HeartbeatInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			c.sendJSON(protocol.NewPingMessage(time.Now().Unix()))
		}
	}
}

// metricsLoop sends periodic metrics
func (c *Client) metricsLoop(done <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			hostMetrics, err := c.metrics.Collect()
			if err != nil {
				log.Printf("Failed to collect metrics: %v", err)
				continue
			}
			c.sendJSON(protocol.NewMetricsMessage(time.Now().Unix(), *hostMetrics))
		}
	}
}

// sendJSON sends a JSON message to the server
func (c *Client) sendJSON(v interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return fmt.Errorf("not connected")
	}

	return c.conn.WriteJSON(v)
}

// close closes the WebSocket connection
func (c *Client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}
