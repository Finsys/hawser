package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Finsys/hawser/internal/log"
)

// Client wraps Docker API operations
type Client struct {
	socketPath   string
	httpClient   *http.Client
	streamClient *http.Client // Separate client for streaming (no timeout)
	apiVersion   string
}

// Default API version if negotiation fails
const defaultAPIVersion = "v1.44"

// NewClient creates a new Docker client
func NewClient(socketPath string) (*Client, error) {
	// Create HTTP transport for Unix socket
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("unix", socketPath)
		},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	}

	// Create streaming transport (same settings, reused for all streaming requests)
	streamTransport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("unix", socketPath)
		},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     0, // No idle timeout for streaming connections
	}

	client := &Client{
		socketPath: socketPath,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		streamClient: &http.Client{
			Transport: streamTransport,
			Timeout:   0, // No timeout for streaming
		},
		apiVersion: defaultAPIVersion, // Will be negotiated below
	}

	// Verify connection
	if err := client.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to connect to Docker: %w", err)
	}

	// Negotiate API version with Docker daemon
	if err := client.negotiateAPIVersion(context.Background()); err != nil {
		log.Warnf("Failed to negotiate API version, using default %s: %v", defaultAPIVersion, err)
	}

	return client, nil
}

// negotiateAPIVersion queries Docker for supported API version and uses a compatible one
func (c *Client) negotiateAPIVersion(ctx context.Context) error {
	version, err := c.GetVersion(ctx)
	if err != nil {
		return err
	}

	// Use Docker's reported API version if available
	if version.APIVersion != "" {
		c.apiVersion = "v" + version.APIVersion
		log.Debugf("Negotiated API version: %s (Docker %s)", c.apiVersion, version.Version)
	}

	return nil
}

// Ping checks Docker daemon connectivity
func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.Request(ctx, "GET", "/_ping", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ping failed with status %d", resp.StatusCode)
	}

	return nil
}

// GetVersion returns Docker version information
func (c *Client) GetVersion(ctx context.Context) (*VersionInfo, error) {
	resp, err := c.Request(ctx, "GET", "/version", nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var version VersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil {
		return nil, err
	}

	return &version, nil
}

// Request makes an HTTP request to the Docker API
func (c *Client) Request(ctx context.Context, method, path string, headers map[string]string, body io.Reader) (*http.Response, error) {
	// Build URL - for Unix socket, host is ignored but required
	url := fmt.Sprintf("http://localhost/%s%s", c.apiVersion, path)
	if strings.HasPrefix(path, "/_ping") || strings.HasPrefix(path, "/version") {
		// These endpoints don't use versioned path
		url = fmt.Sprintf("http://localhost%s", path)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	// Set default headers
	req.Header.Set("Content-Type", "application/json")

	// Set custom headers
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	log.Debugf("Docker API: %s %s", method, url)
	resp, err := c.httpClient.Do(req)
	if err == nil {
		log.Debugf("Docker API response: %s %s -> %d", method, path, resp.StatusCode)
	}
	return resp, err
}

// RequestRaw makes a request without API versioning (for proxying)
func (c *Client) RequestRaw(ctx context.Context, method, path string, headers map[string]string, body io.Reader) (*http.Response, error) {
	url := fmt.Sprintf("http://localhost%s", path)

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	// Set custom headers
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	log.Debugf("Docker API (raw): %s %s", method, path)
	resp, err := c.httpClient.Do(req)
	if err == nil {
		log.Debugf("Docker API response: %s %s -> %d", method, path, resp.StatusCode)
	}
	return resp, err
}

// StreamRequest makes a streaming request (for logs, exec, events)
// Uses the pre-initialized streamClient which has no timeout and proper connection pooling
func (c *Client) StreamRequest(ctx context.Context, method, path string, headers map[string]string, body io.Reader) (*http.Response, error) {
	url := fmt.Sprintf("http://localhost%s", path)

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	log.Debugf("Docker API (stream): %s %s", method, path)
	// Use pre-initialized stream client (no timeout, shared connection pool)
	resp, err := c.streamClient.Do(req)
	if err == nil {
		log.Debugf("Docker API stream started: %s %s -> %d", method, path, resp.StatusCode)
	}
	return resp, err
}

// GetDataRoot returns Docker's data root directory
func (c *Client) GetDataRoot(ctx context.Context) (string, error) {
	resp, err := c.Request(ctx, "GET", "/info", nil, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var info struct {
		DockerRootDir string `json:"DockerRootDir"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}

	if info.DockerRootDir == "" {
		return "/var/lib/docker", nil
	}

	return info.DockerRootDir, nil
}

// VersionInfo contains Docker version information
type VersionInfo struct {
	Version       string `json:"Version"`
	APIVersion    string `json:"ApiVersion"`
	MinAPIVersion string `json:"MinAPIVersion"`
	GitCommit     string `json:"GitCommit"`
	GoVersion     string `json:"GoVersion"`
	Os            string `json:"Os"`
	Arch          string `json:"Arch"`
	KernelVersion string `json:"KernelVersion"`
	BuildTime     string `json:"BuildTime"`
}

// Close closes the Docker client and all its connections
func (c *Client) Close() error {
	c.httpClient.CloseIdleConnections()
	c.streamClient.CloseIdleConnections()
	return nil
}

// ExecConfig holds the configuration for creating an exec instance
type ExecConfig struct {
	ContainerID string
	Cmd         []string
	User        string
	Tty         bool
}

// ExecCreateResponse is the response from exec create
type ExecCreateResponse struct {
	ID string `json:"Id"`
}

// CreateExec creates a new exec instance in a container
func (c *Client) CreateExec(ctx context.Context, config *ExecConfig) (*ExecCreateResponse, error) {
	body := map[string]interface{}{
		"AttachStdin":  true,
		"AttachStdout": true,
		"AttachStderr": true,
		"Tty":          config.Tty,
		"Cmd":          config.Cmd,
	}
	if config.User != "" {
		body["User"] = config.User
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	path := fmt.Sprintf("/containers/%s/exec", config.ContainerID)
	resp, err := c.Request(ctx, "POST", path, nil, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("exec create failed: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var result ExecCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// HijackedConn represents a hijacked connection for exec
type HijackedConn struct {
	Conn     net.Conn
	Reader   *io.Reader
	Leftover []byte // Any data read past the HTTP headers
}

// StartExecAttach starts an exec instance and returns a hijacked connection
func (c *Client) StartExecAttach(ctx context.Context, execID string) (*HijackedConn, error) {
	// Connect directly to the Unix socket
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Docker socket: %w", err)
	}

	// Set read deadline for initial header parsing to prevent indefinite hangs
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// Build the HTTP request manually for hijacking
	path := fmt.Sprintf("/%s/exec/%s/start", c.apiVersion, execID)
	body := `{"Detach":false,"Tty":true}`
	request := fmt.Sprintf(
		"POST %s HTTP/1.1\r\n"+
			"Host: localhost\r\n"+
			"Content-Type: application/json\r\n"+
			"Connection: Upgrade\r\n"+
			"Upgrade: tcp\r\n"+
			"Content-Length: %d\r\n"+
			"\r\n"+
			"%s",
		path, len(body), body,
	)

	// Send the request
	if _, err := conn.Write([]byte(request)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send exec start request: %w", err)
	}

	// Read HTTP response headers - we need to read until we find the end of headers (\r\n\r\n)
	// Use a buffered approach to handle headers that might span multiple reads
	headerBuf := make([]byte, 0, 4096)
	tempBuf := make([]byte, 1024)
	headerEnd := -1

	for {
		n, err := conn.Read(tempBuf)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("failed to read exec start response: %w", err)
		}
		headerBuf = append(headerBuf, tempBuf[:n]...)

		// Look for end of HTTP headers
		if idx := strings.Index(string(headerBuf), "\r\n\r\n"); idx != -1 {
			headerEnd = idx + 4
			break
		}

		// Safety check - headers shouldn't be this long
		if len(headerBuf) > 8192 {
			conn.Close()
			return nil, fmt.Errorf("HTTP headers too long")
		}
	}

	response := string(headerBuf[:headerEnd])
	log.Debugf("Exec start response: %s", strings.Split(response, "\r\n")[0])

	// Check for successful upgrade (101 Switching Protocols, 101 UPGRADED) or 200 OK
	if !strings.Contains(response, "101 ") && !strings.Contains(response, "200 OK") {
		conn.Close()
		return nil, fmt.Errorf("exec start failed: %s", response)
	}

	// Check if we read any data beyond the headers (leftover data after \r\n\r\n)
	var leftover []byte
	if headerEnd < len(headerBuf) {
		leftover = headerBuf[headerEnd:]
	}

	// Clear the read deadline for streaming - connection can now stream indefinitely
	conn.SetReadDeadline(time.Time{})

	return &HijackedConn{
		Conn:     conn,
		Leftover: leftover,
	}, nil
}

// ResizeExec resizes the exec terminal
func (c *Client) ResizeExec(ctx context.Context, execID string, height, width int) error {
	path := fmt.Sprintf("/exec/%s/resize?h=%d&w=%d", execID, height, width)
	resp, err := c.Request(ctx, "POST", path, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Drain body to enable HTTP connection reuse
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("resize failed with status %d", resp.StatusCode)
	}

	return nil
}
