package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/Finsys/hawser/internal/protocol"
)

// ExecTunnel handles interactive exec sessions through the WebSocket tunnel
type ExecTunnel struct {
	client    *Client
	requestID string
	execID    string
	conn      net.Conn
	mu        sync.Mutex
	closed    bool
}

// ExecRequest represents a request to start an exec session
type ExecRequest struct {
	ContainerID string   `json:"containerId"`
	Cmd         []string `json:"cmd"`
	User        string   `json:"user,omitempty"`
	Tty         bool     `json:"tty"`
	Detach      bool     `json:"detach"`
}

// ExecStartRequest is the body for starting an exec
type ExecStartRequest struct {
	Detach bool `json:"Detach"`
	Tty    bool `json:"Tty"`
}

// HandleExecRequest processes an exec tunnel request
func (c *Client) HandleExecRequest(ctx context.Context, req *protocol.RequestMessage) {
	var execReq ExecRequest
	if err := json.Unmarshal(req.Body, &execReq); err != nil {
		c.sendJSON(protocol.NewErrorMessage(req.RequestID, err.Error(), "PARSE_ERROR"))
		return
	}

	// Create exec instance
	execID, err := c.createExec(ctx, &execReq)
	if err != nil {
		c.sendJSON(protocol.NewErrorMessage(req.RequestID, err.Error(), "EXEC_CREATE_ERROR"))
		return
	}

	// Start exec with hijack
	tunnel := &ExecTunnel{
		client:    c,
		requestID: req.RequestID,
		execID:    execID,
	}

	if err := tunnel.Start(ctx, execReq.Tty); err != nil {
		c.sendJSON(protocol.NewErrorMessage(req.RequestID, err.Error(), "EXEC_START_ERROR"))
		return
	}
}

// createExec creates a new exec instance
func (c *Client) createExec(ctx context.Context, req *ExecRequest) (string, error) {
	body := map[string]interface{}{
		"AttachStdin":  true,
		"AttachStdout": true,
		"AttachStderr": true,
		"Tty":          req.Tty,
		"Cmd":          req.Cmd,
	}
	if req.User != "" {
		body["User"] = req.User
	}

	bodyJSON, _ := json.Marshal(body)

	resp, err := c.dockerClient.RequestRaw(ctx, "POST",
		fmt.Sprintf("/containers/%s/exec", req.ContainerID),
		map[string]string{"Content-Type": "application/json"},
		strings.NewReader(string(bodyJSON)),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("exec create failed: %s", string(body))
	}

	var result struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.ID, nil
}

// Start begins the exec session with hijacking
func (t *ExecTunnel) Start(ctx context.Context, tty bool) error {
	// Connect to Docker socket for hijacked connection
	conn, err := net.Dial("unix", t.client.cfg.DockerSocket)
	if err != nil {
		return fmt.Errorf("failed to connect to Docker socket: %w", err)
	}
	t.conn = conn

	// Send exec start request with upgrade
	startBody, _ := json.Marshal(ExecStartRequest{Detach: false, Tty: tty})
	req := fmt.Sprintf(
		"POST /exec/%s/start HTTP/1.1\r\n"+
			"Host: localhost\r\n"+
			"Content-Type: application/json\r\n"+
			"Connection: Upgrade\r\n"+
			"Upgrade: tcp\r\n"+
			"Content-Length: %d\r\n"+
			"\r\n%s",
		t.execID, len(startBody), startBody,
	)

	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return fmt.Errorf("failed to send exec start: %w", err)
	}

	// Read HTTP response headers
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check for 101 Switching Protocols
	response := string(buf[:n])
	if !strings.Contains(response, "101") {
		conn.Close()
		return fmt.Errorf("exec start failed: %s", response)
	}

	// Register stream
	t.client.streamsMu.Lock()
	cancelCtx, cancel := context.WithCancel(ctx)
	t.client.streams[t.requestID] = &StreamContext{
		RequestID: t.requestID,
		Cancel:    cancel,
	}
	t.client.streamsMu.Unlock()

	// Start bidirectional streaming
	go t.readLoop(cancelCtx)

	// Send initial response to indicate exec is ready
	t.client.sendJSON(protocol.NewResponseMessage(t.requestID, http.StatusOK,
		map[string]string{"X-Exec-ID": t.execID}, nil))

	return nil
}

// readLoop reads from Docker and sends to Dockhand
func (t *ExecTunnel) readLoop(ctx context.Context) {
	defer t.Close()

	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := t.conn.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			t.client.sendJSON(protocol.NewStreamMessage(t.requestID, data, "stdout"))
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("Exec read error: %v", err)
			}
			return
		}
	}
}

// Write sends data to the exec stdin
func (t *ExecTunnel) Write(data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed || t.conn == nil {
		return fmt.Errorf("tunnel closed")
	}

	_, err := t.conn.Write(data)
	return err
}

// Resize sends a terminal resize command
func (t *ExecTunnel) Resize(width, height int) error {
	ctx := context.Background()
	body := fmt.Sprintf(`{"h":%d,"w":%d}`, height, width)

	resp, err := t.client.dockerClient.RequestRaw(ctx, "POST",
		fmt.Sprintf("/exec/%s/resize?h=%d&w=%d", t.execID, height, width),
		nil, strings.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Close closes the tunnel
func (t *ExecTunnel) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return
	}
	t.closed = true

	if t.conn != nil {
		t.conn.Close()
	}

	// Remove from active streams
	t.client.streamsMu.Lock()
	delete(t.client.streams, t.requestID)
	t.client.streamsMu.Unlock()

	// Send stream end
	t.client.sendJSON(protocol.NewStreamEndMessage(t.requestID, "closed"))
}

// HandleExecInput processes input data for an active exec session
func (c *Client) HandleExecInput(requestID string, data []byte) error {
	c.streamsMu.RLock()
	stream, ok := c.streams[requestID]
	c.streamsMu.RUnlock()

	if !ok {
		return fmt.Errorf("no active exec session for request %s", requestID)
	}

	if stream.Writer != nil {
		_, err := stream.Writer.Write(data)
		return err
	}

	return nil
}

// HandleExecResize processes resize events for an active exec session
func (c *Client) HandleExecResize(requestID string, width, height int) error {
	// Find the tunnel for this request
	// This would need additional tracking in a real implementation
	return nil
}
