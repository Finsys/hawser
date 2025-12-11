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
)

// Client wraps Docker API operations
type Client struct {
	socketPath   string
	httpClient   *http.Client
	streamClient *http.Client // Separate client for streaming (no timeout)
	apiVersion   string
}

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
		apiVersion: "v1.43", // Docker API version
	}

	// Verify connection
	if err := client.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to connect to Docker: %w", err)
	}

	return client, nil
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

	return c.httpClient.Do(req)
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

	return c.httpClient.Do(req)
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

	// Use pre-initialized stream client (no timeout, shared connection pool)
	return c.streamClient.Do(req)
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
