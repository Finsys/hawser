package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"time"
)

// runHealthcheck probes the local /_hawser/health endpoint and exits 0 on
// success or 1 on failure. It is used as the container HEALTHCHECK command so
// the image does not need to ship wget/curl. TLS mode is auto-detected: when
// TLS_CERT is set, https is used with certificate verification disabled (the
// listener cert is typically self-signed and only needs to prove it is up).
func runHealthcheck() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "2376"
	}

	scheme := "http"
	client := &http.Client{Timeout: 5 * time.Second}
	if os.Getenv("TLS_CERT") != "" {
		scheme = "https"
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // liveness probe only
		}
	}

	resp, err := client.Get(fmt.Sprintf("%s://localhost:%s/_hawser/health", scheme, port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "healthcheck: unexpected status %d\n", resp.StatusCode)
		os.Exit(1)
	}
}
