package cluster

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"
)

// isClusterAlive checks if the cluster is alive by sending a request to the API server
// healthz endpoint
func isClusterAlive(apiServerURL string) bool {
	client := &http.Client{
		Timeout: 3 * time.Second,
		// Kubernetes API server uses self-signed certs by default
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Get(apiServerURL + "/healthz")
	if err != nil {
		fmt.Println("Error contacting cluster:", err)
		return false
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return true
	case http.StatusUnauthorized:
		return true
	case http.StatusForbidden:
		return true
	}
	return false
}
