package edge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Finsys/hawser/internal/config"
	"github.com/Finsys/hawser/internal/docker"
	"github.com/Finsys/hawser/internal/log"
	"github.com/Finsys/hawser/internal/metrics"
	"github.com/Finsys/hawser/internal/pool"
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

	// Active exec sessions
	execSessions   map[string]*ExecSession
	execSessionsMu sync.RWMutex
}

// ExecSession tracks an active exec/terminal session
type ExecSession struct {
	ExecID       string
	DockerExecID string
	Conn         *docker.HijackedConn
	Cancel       context.CancelFunc
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
		log.Warnf("Could not get Docker version: %v", err)
	} else {
		log.Infof("Connected to Docker %s (API %s)", version.Version, version.APIVersion)
	}

	client := &Client{
		cfg:          cfg,
		dockerClient: dockerClient,
		compose:      docker.NewComposeClient(cfg.DockerSocket),
		metrics:      metrics.NewCollector(dockerClient),
		stop:         stop,
		streams:      make(map[string]*StreamContext),
		execSessions: make(map[string]*ExecSession),
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
			log.Errorf("Connection failed: %v", err)
		}

		// Check if we should stop
		select {
		case <-c.stop:
			return nil
		default:
		}

		log.Infof("Reconnecting in %v...", backoff)
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
	log.Infof("Connecting to %s", c.cfg.DockhandServerURL)

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

	capabilities := []string{protocol.CapabilityExec, protocol.CapabilityMetrics, protocol.CapabilityEvents}
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
		if err := json.Unmarshal(data, &errMsg); err != nil {
			return fmt.Errorf("server error (failed to parse error message: %v)", err)
		}
		return fmt.Errorf("server error: %s", errMsg.Error)
	}

	if msgType != protocol.TypeWelcome {
		return fmt.Errorf("expected welcome message, got %s", msgType)
	}

	var welcome protocol.WelcomeMessage
	if err := json.Unmarshal(data, &welcome); err != nil {
		return err
	}

	log.Infof("Welcome received, environment ID: %d", welcome.EnvironmentID)
	return nil
}

// run handles the main message loop
func (c *Client) run() {
	done := make(chan struct{})

	// Start heartbeat
	go c.heartbeatLoop(done)

	// Start metrics sender
	go c.metricsLoop(done)

	// Start events sender
	go c.eventsLoop(done)

	// Calculate read deadline based on heartbeat interval (2x to allow for network delays)
	readDeadline := time.Duration(c.cfg.HeartbeatInterval*2) * time.Second
	if readDeadline < 60*time.Second {
		readDeadline = 60 * time.Second
	}

	// Message loop
	for {
		select {
		case <-c.stop:
			close(done)
			c.close()
			return
		default:
		}

		// Get connection reference under lock for thread safety
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()

		if conn == nil {
			log.Errorf("Connection is nil")
			close(done)
			return
		}

		// Set read deadline to detect dead connections
		conn.SetReadDeadline(time.Now().Add(readDeadline))

		_, data, err := conn.ReadMessage()
		if err != nil {
			log.Errorf("Read error: %v", err)
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
		log.Errorf("Failed to parse message: %v", err)
		return
	}

	log.Debugf("Received message type: %s", msgType)

	switch msgType {
	case protocol.TypeRequest:
		var req protocol.RequestMessage
		if err := json.Unmarshal(data, &req); err != nil {
			log.Errorf("Failed to parse request: %v", err)
			return
		}
		c.handleRequest(&req)

	case protocol.TypePing:
		var ping protocol.PingMessage
		if err := json.Unmarshal(data, &ping); err != nil {
			return
		}
		if err := c.sendJSON(protocol.NewPongMessage(time.Now().Unix())); err != nil {
			log.Warnf("Failed to send pong: %v", err)
		}

	case protocol.TypeStreamEnd:
		var end protocol.StreamEndMessage
		if err := json.Unmarshal(data, &end); err != nil {
			return
		}
		c.cancelStream(end.RequestID)

	case protocol.TypeExecStart:
		var execStart protocol.ExecStartMessage
		if err := json.Unmarshal(data, &execStart); err != nil {
			log.Errorf("Failed to parse exec_start: %v", err)
			return
		}
		go c.handleExecStart(&execStart)

	case protocol.TypeExecInput:
		var execInput protocol.ExecInputMessage
		if err := json.Unmarshal(data, &execInput); err != nil {
			log.Errorf("Failed to parse exec_input: %v", err)
			return
		}
		c.handleExecInput(&execInput)

	case protocol.TypeExecResize:
		var execResize protocol.ExecResizeMessage
		if err := json.Unmarshal(data, &execResize); err != nil {
			log.Errorf("Failed to parse exec_resize: %v", err)
			return
		}
		c.handleExecResize(&execResize)

	case protocol.TypeExecEnd:
		var execEnd protocol.ExecEndMessage
		if err := json.Unmarshal(data, &execEnd); err != nil {
			log.Errorf("Failed to parse exec_end: %v", err)
			return
		}
		c.handleExecEnd(&execEnd)

	default:
		log.Warnf("Unknown message type: %s", msgType)
	}
}

// handleRequest processes Docker API requests
func (c *Client) handleRequest(req *protocol.RequestMessage) {
	log.Debugf("Docker API request: %s %s (streaming=%v)", req.Method, req.Path, req.Streaming)

	// Build headers
	headers := make(map[string]string)
	for k, v := range req.Headers {
		headers[k] = v
	}

	// Streaming requests use a background context (no timeout) since they can run indefinitely
	if req.Streaming {
		c.handleStreamingRequest(req, headers)
		return
	}

	// Non-streaming requests use a timeout context
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.cfg.RequestTimeout)*time.Second)
	defer cancel()

	// Check if this is a compose operation
	if req.Path == "/_hawser/compose" {
		c.handleComposeRequest(ctx, req)
		return
	}

	// Make Docker request
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
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
	log.Debugf("Docker API response: %s %s -> %d", req.Method, req.Path, resp.StatusCode)
	c.sendJSON(protocol.NewResponseMessage(req.RequestID, resp.StatusCode, respHeaders, respBody))
}

// handleStreamingRequest handles streaming Docker responses (logs, events, etc.)
// Uses a background context with cancel since streaming can run indefinitely
func (c *Client) handleStreamingRequest(req *protocol.RequestMessage, headers map[string]string) {
	ctx, cancel := context.WithCancel(context.Background())

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

	// Stream data back using pooled buffer
	bufPtr := pool.GetBuffer()
	defer pool.PutBuffer(bufPtr)
	buf := *bufPtr

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
				log.Errorf("Stream read error: %v", err)
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

	log.Debugf("Compose operation: %s on %s", op.Operation, op.ProjectName)

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
			if err := c.sendJSON(protocol.NewPingMessage(time.Now().Unix())); err != nil {
				log.Warnf("Failed to send ping: %v", err)
			}
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
				log.Warnf("Failed to collect metrics: %v", err)
				continue
			}
			if err := c.sendJSON(protocol.NewMetricsMessage(time.Now().Unix(), *hostMetrics)); err != nil {
				log.Warnf("Failed to send metrics: %v", err)
			}
		}
	}
}

// DockerEvent represents a Docker event from the events API
type DockerEvent struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Actor  struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
	Time     int64 `json:"time"`
	TimeNano int64 `json:"timeNano"`
}

// Container actions we care about
var containerActions = map[string]bool{
	"create":        true,
	"start":         true,
	"stop":          true,
	"die":           true,
	"kill":          true,
	"restart":       true,
	"pause":         true,
	"unpause":       true,
	"destroy":       true,
	"rename":        true,
	"update":        true,
	"oom":           true,
	"health_status": true,
}

// Scanner image patterns to exclude
var scannerImages = []string{
	"anchore/grype",
	"aquasec/trivy",
	"ghcr.io/anchore/grype",
	"ghcr.io/aquasecurity/trivy",
}

// Container name prefixes to exclude
var excludedPrefixes = []string{
	"dockhand-browse-",
}

// isScannerImage checks if the image is a vulnerability scanner
func isScannerImage(image string) bool {
	lower := strings.ToLower(image)
	for _, pattern := range scannerImages {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// isExcludedContainer checks if the container should be excluded
func isExcludedContainer(name string) bool {
	for _, prefix := range excludedPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// eventsLoop streams Docker container events to Dockhand
func (c *Client) eventsLoop(done <-chan struct{}) {
	initialDelay := 5 * time.Second
	reconnectDelay := initialDelay
	maxReconnectDelay := 60 * time.Second
	minSuccessDuration := 30 * time.Second // Reset backoff if connected for this long

	for {
		select {
		case <-done:
			return
		default:
		}

		startTime := time.Now()
		err := c.streamEvents(done)
		connectionDuration := time.Since(startTime)

		if err != nil {
			log.Warnf("Docker events stream error: %v", err)
		}

		// Reset backoff if we were connected successfully for a while
		if connectionDuration >= minSuccessDuration {
			reconnectDelay = initialDelay
		}

		// Check if we should stop before reconnecting
		select {
		case <-done:
			return
		case <-time.After(reconnectDelay):
			// Exponential backoff
			reconnectDelay *= 2
			if reconnectDelay > maxReconnectDelay {
				reconnectDelay = maxReconnectDelay
			}
		}
	}
}

// streamEvents connects to Docker events API and streams events
func (c *Client) streamEvents(done <-chan struct{}) error {
	ctx, cancel := context.WithCancel(context.Background())

	// Monitor goroutine - cancels context when done is signaled
	// WaitGroup ensures goroutine exits before function returns
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-done:
			cancel()
		case <-ctx.Done():
			// Context was cancelled, exit goroutine cleanly
		}
	}()

	// Ensure cleanup: cancel context (signals goroutine) and wait for it to exit
	defer func() {
		cancel()
		wg.Wait()
	}()

	// Connect to Docker events stream with type=container filter
	resp, err := c.dockerClient.StreamRequest(ctx, "GET", "/v1.43/events?type=container", nil, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to events: %w", err)
	}
	defer resp.Body.Close()

	log.Println("Connected to Docker events stream")

	decoder := json.NewDecoder(resp.Body)
	for {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return nil
		default:
		}

		var event DockerEvent
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				return fmt.Errorf("events stream closed")
			}
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("decode error: %w", err)
		}

		// Only process container events
		if event.Type != "container" {
			continue
		}

		// Get action without any suffix (e.g., "health_status: healthy" -> "health_status")
		action := strings.Split(event.Action, ":")[0]
		action = strings.TrimSpace(action)

		// Skip actions we don't care about
		if !containerActions[action] {
			continue
		}

		// Get container info
		containerID := event.Actor.ID
		containerName := event.Actor.Attributes["name"]
		image := event.Actor.Attributes["image"]

		// Skip scanner containers
		if isScannerImage(image) {
			continue
		}

		// Skip internal Dockhand containers
		if isExcludedContainer(containerName) {
			continue
		}

		// Convert timestamp to ISO 8601
		timestamp := time.Unix(0, event.TimeNano).UTC().Format(time.RFC3339Nano)

		// Create and send container event message
		eventMsg := protocol.NewContainerEventMessage(protocol.ContainerEvent{
			ContainerID:     containerID,
			ContainerName:   containerName,
			Image:           image,
			Action:          action,
			ActorAttributes: event.Actor.Attributes,
			Timestamp:       timestamp,
		})

		log.Debugf("Container event: %s %s (%s)", action, containerName, image)
		if err := c.sendJSON(eventMsg); err != nil {
			log.Warnf("Failed to send container event: %v", err)
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

	// Debug: log what we're sending
	data, err := json.Marshal(v)
	if err != nil {
		log.Errorf("Failed to marshal message: %v", err)
		return err
	}

	// Extra debug for response messages
	if resp, ok := v.(*protocol.ResponseMessage); ok {
		log.Debugf("Sending response: requestId=%s, statusCode=%d, isBinary=%v, bodyLen=%d",
			resp.RequestID, resp.StatusCode, resp.IsBinary, len(resp.Body))
	}

	log.Debugf("Sending message: %d bytes, preview: %s", len(data), string(data[:min(len(data), 200)]))

	return c.conn.WriteJSON(v)
}

// handleExecStart starts a new exec session
func (c *Client) handleExecStart(msg *protocol.ExecStartMessage) {
	log.Infof("Starting exec session: %s in container %s (cmd: %s, user: %s)", msg.ExecID, msg.ContainerID, msg.Cmd, msg.User)

	ctx, cancel := context.WithCancel(context.Background())

	// Create exec instance
	log.Debugf("Creating Docker exec for session %s", msg.ExecID)
	execResp, err := c.dockerClient.CreateExec(ctx, &docker.ExecConfig{
		ContainerID: msg.ContainerID,
		Cmd:         []string{msg.Cmd},
		User:        msg.User,
		Tty:         true,
	})
	if err != nil {
		log.Errorf("Failed to create exec: %v", err)
		c.sendJSON(protocol.NewErrorMessage(msg.ExecID, err.Error(), "EXEC_CREATE_ERROR"))
		cancel()
		return
	}

	log.Debugf("Created Docker exec: %s for session %s", execResp.ID, msg.ExecID)

	// Start exec with hijack
	log.Debugf("Starting exec attach for session %s", msg.ExecID)
	hijacked, err := c.dockerClient.StartExecAttach(ctx, execResp.ID)
	if err != nil {
		log.Errorf("Failed to start exec: %v", err)
		c.sendJSON(protocol.NewErrorMessage(msg.ExecID, err.Error(), "EXEC_START_ERROR"))
		cancel()
		return
	}
	log.Debugf("Exec attach successful for session %s", msg.ExecID)

	// Store session FIRST so resize/input messages don't fail
	session := &ExecSession{
		ExecID:       msg.ExecID,
		DockerExecID: execResp.ID,
		Conn:         hijacked,
		Cancel:       cancel,
	}

	c.execSessionsMu.Lock()
	c.execSessions[msg.ExecID] = session
	c.execSessionsMu.Unlock()

	log.Debugf("Stored exec session %s", msg.ExecID)

	// Send ready message
	c.sendJSON(protocol.NewExecReadyMessage(msg.ExecID))

	// Resize terminal to initial size (after session is stored)
	if msg.Cols > 0 && msg.Rows > 0 {
		if err := c.dockerClient.ResizeExec(ctx, execResp.ID, msg.Rows, msg.Cols); err != nil {
			log.Warnf("Failed to resize exec: %v", err)
		}
	}

	// Start reading output from Docker
	go c.readExecOutput(session)
}

// readExecOutput reads output from exec session and sends to Dockhand
func (c *Client) readExecOutput(session *ExecSession) {
	defer func() {
		c.execSessionsMu.Lock()
		delete(c.execSessions, session.ExecID)
		c.execSessionsMu.Unlock()

		if session.Conn != nil && session.Conn.Conn != nil {
			session.Conn.Conn.Close()
		}
		session.Cancel()

		c.sendJSON(protocol.NewExecEndMessage(session.ExecID, "container_exit"))
		log.Infof("Exec session ended: %s", session.ExecID)
	}()

	// First, send any leftover data from the HTTP header parsing
	if len(session.Conn.Leftover) > 0 {
		c.sendJSON(protocol.NewExecOutputMessage(session.ExecID, session.Conn.Leftover))
	}

	// Use pooled buffer for reading exec output
	bufPtr := pool.GetBuffer()
	defer pool.PutBuffer(bufPtr)
	buf := *bufPtr

	for {
		n, err := session.Conn.Conn.Read(buf)
		if n > 0 {
			// Send output to Dockhand
			c.sendJSON(protocol.NewExecOutputMessage(session.ExecID, buf[:n]))
		}
		if err != nil {
			if err != io.EOF {
				log.Debugf("Exec read error: %v", err)
			}
			return
		}
	}
}

// handleExecInput handles terminal input from user
func (c *Client) handleExecInput(msg *protocol.ExecInputMessage) {
	// Retry a few times since input may arrive before exec session is fully stored
	var session *ExecSession
	var ok bool
	for i := 0; i < 10; i++ {
		c.execSessionsMu.RLock()
		session, ok = c.execSessions[msg.ExecID]
		c.execSessionsMu.RUnlock()

		if ok {
			break
		}

		// Wait a bit for session to be created
		time.Sleep(50 * time.Millisecond)
	}

	if !ok {
		log.Warnf("Exec session not found after retries: %s", msg.ExecID)
		return
	}

	// Decode base64 input
	data, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		log.Errorf("Failed to decode exec input: %v", err)
		return
	}

	// Write to Docker
	if session.Conn != nil && session.Conn.Conn != nil {
		if _, err := session.Conn.Conn.Write(data); err != nil {
			log.Errorf("Failed to write exec input: %v", err)
		}
	}
}

// handleExecResize handles terminal resize
func (c *Client) handleExecResize(msg *protocol.ExecResizeMessage) {
	// Retry a few times since resize may arrive before exec session is fully stored
	var session *ExecSession
	var ok bool
	for i := 0; i < 10; i++ {
		c.execSessionsMu.RLock()
		session, ok = c.execSessions[msg.ExecID]
		c.execSessionsMu.RUnlock()

		if ok {
			break
		}

		// Wait a bit for session to be created
		time.Sleep(50 * time.Millisecond)
	}

	if !ok {
		log.Warnf("Exec session not found for resize after retries: %s", msg.ExecID)
		return
	}

	if err := c.dockerClient.ResizeExec(context.Background(), session.DockerExecID, msg.Rows, msg.Cols); err != nil {
		log.Warnf("Failed to resize exec: %v", err)
	}
}

// handleExecEnd handles end of exec session
func (c *Client) handleExecEnd(msg *protocol.ExecEndMessage) {
	c.execSessionsMu.Lock()
	session, ok := c.execSessions[msg.ExecID]
	if ok {
		delete(c.execSessions, msg.ExecID)
		// Close connection and cancel while holding the lock to prevent race
		// with readExecOutput which may still be reading from the connection
		log.Infof("Closing exec session: %s (reason: %s)", msg.ExecID, msg.Reason)
		if session.Conn != nil && session.Conn.Conn != nil {
			session.Conn.Conn.Close()
		}
		session.Cancel()
	}
	c.execSessionsMu.Unlock()
}

// close closes the WebSocket connection
func (c *Client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Close all exec sessions
	c.execSessionsMu.Lock()
	for id, session := range c.execSessions {
		if session.Conn != nil && session.Conn.Conn != nil {
			session.Conn.Conn.Close()
		}
		session.Cancel()
		delete(c.execSessions, id)
	}
	c.execSessionsMu.Unlock()

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}
