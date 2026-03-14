// Package kube provides a thin Kubernetes HTTP client for in-cluster use.
// It hits the K8s REST API directly, avoiding the ~30MB client-go dependency.
// Only instantiated when runtime mode is "kubernetes".
package kube

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// In-cluster ServiceAccount paths (standard on all K8s clusters).
const (
	saTokenPath     = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	saCACertPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	saNamespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

// tokenCacheTTL is how long a file-read token is cached before re-reading.
// K8s bound SA tokens are rotated by kubelet; 60s is a safe refresh interval.
const tokenCacheTTL = 60 * time.Second

// Client is a thin Kubernetes API client that reads in-cluster credentials
// from the ServiceAccount volume mount.
type Client struct {
	host       string       // e.g. "https://kubernetes.default.svc"
	token      string       // bearer token (fallback for NewFromConfig)
	tokenPath  string       // path to SA token file (re-read on each request for rotation)
	namespace  string       // default namespace for operations
	httpClient *http.Client // configured with CA cert

	// Token caching: avoid re-reading the SA token file on every request.
	tokenMu       sync.Mutex // guards cachedToken and cachedTokenAt
	cachedToken   string     // last successfully read token
	cachedTokenAt time.Time  // when cachedToken was last refreshed
}

// NewInCluster creates a Client using the in-cluster ServiceAccount credentials.
// namespace overrides the SA-mounted namespace if non-empty.
func NewInCluster(namespace string) (*Client, error) {
	token, err := os.ReadFile(saTokenPath)
	if err != nil {
		return nil, fmt.Errorf("read SA token: %w", err)
	}

	caCert, err := os.ReadFile(saCACertPath)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	if namespace == "" {
		ns, err := os.ReadFile(saNamespacePath)
		if err != nil {
			return nil, fmt.Errorf("read namespace: %w", err)
		}
		namespace = strings.TrimSpace(string(ns))
	}

	httpClient := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    pool,
				MinVersion: tls.VersionTLS12,
			},
		},
	}

	return &Client{
		host:       "https://kubernetes.default.svc",
		token:      strings.TrimSpace(string(token)),
		tokenPath:  saTokenPath,
		namespace:  namespace,
		httpClient: httpClient,
	}, nil
}

// NewFromConfig creates a Client from explicit parameters (useful for testing).
func NewFromConfig(host, token, namespace string, httpClient *http.Client) *Client {
	return &Client{
		host:       host,
		token:      token,
		namespace:  namespace,
		httpClient: httpClient,
	}
}

// Namespace returns the client's default namespace.
func (c *Client) Namespace() string {
	return c.namespace
}

// bearerToken returns the current SA token, re-reading from file if the
// cache has expired. This handles K8s bound SA token rotation (tokens are
// projected and refreshed by kubelet).
func (c *Client) bearerToken() string {
	if c.tokenPath == "" {
		return c.token
	}

	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	// Return cached token if still fresh.
	if c.cachedToken != "" && time.Since(c.cachedTokenAt) < tokenCacheTTL {
		return c.cachedToken
	}

	data, err := os.ReadFile(c.tokenPath)
	if err != nil {
		// Log-worthy: SA token file became unreadable (permissions, mount issue).
		// Fall back to last known good token.
		if c.cachedToken != "" {
			return c.cachedToken
		}
		return c.token
	}

	c.cachedToken = strings.TrimSpace(string(data))
	c.cachedTokenAt = time.Now()
	return c.cachedToken
}

// Get retrieves a single resource by name.
func (c *Client) Get(ctx context.Context, resource, name string, result any) error {
	url := c.resourceURL(resource, name)
	return c.do(ctx, http.MethodGet, url, nil, result)
}

// Create creates a new resource.
func (c *Client) Create(ctx context.Context, resource string, body, result any) error {
	url := c.collectionURL(resource)
	return c.do(ctx, http.MethodPost, url, body, result)
}

// Update replaces an existing resource.
func (c *Client) Update(ctx context.Context, resource, name string, body, result any) error {
	url := c.resourceURL(resource, name)
	return c.do(ctx, http.MethodPut, url, body, result)
}

// Delete removes a resource by name.
func (c *Client) Delete(ctx context.Context, resource, name string) error {
	url := c.resourceURL(resource, name)
	return c.do(ctx, http.MethodDelete, url, nil, nil)
}

// List retrieves all resources matching the given label selector.
func (c *Client) List(ctx context.Context, resource string, labels map[string]string, result any) error {
	u := c.collectionURL(resource)
	if len(labels) > 0 {
		var parts []string
		for k, v := range labels {
			parts = append(parts, k+"="+v)
		}
		// URL-encode the label selector to prevent injection via crafted values.
		u += "?labelSelector=" + neturl.QueryEscape(strings.Join(parts, ","))
	}
	return c.do(ctx, http.MethodGet, u, nil, result)
}

// PodLogs returns a reader for a pod's log output (last N lines).
func (c *Client) PodLogs(ctx context.Context, name string, lines int) (io.ReadCloser, error) {
	url := fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/log?tailLines=%d",
		c.host, c.namespace, name, lines)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.bearerToken())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("pod logs %s: %s: %s", name, resp.Status, body)
	}
	return resp.Body, nil
}

// StatusError is returned when the K8s API returns an error status.
type StatusError struct {
	Code    int
	Status  string
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("k8s %d %s: %s", e.Code, e.Status, e.Message)
}

// IsNotFound returns true if the error is a K8s 404 Not Found.
func IsNotFound(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == http.StatusNotFound
}

// IsConflict returns true if the error is a K8s 409 Conflict.
func IsConflict(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == http.StatusConflict
}

// CheckAccess verifies whether the current ServiceAccount can perform a
// specific action using the SelfSubjectAccessReview API. Returns true if
// the action is allowed, false otherwise.
func (c *Client) CheckAccess(ctx context.Context, resource, verb string) (bool, error) {
	review := map[string]any{
		"apiVersion": "authorization.k8s.io/v1",
		"kind":       "SelfSubjectAccessReview",
		"spec": map[string]any{
			"resourceAttributes": map[string]any{
				"namespace": c.namespace,
				"verb":      verb,
				"resource":  resource,
			},
		},
	}

	url := c.host + "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews"
	var result struct {
		Status struct {
			Allowed bool `json:"allowed"`
		} `json:"status"`
	}
	if err := c.do(ctx, http.MethodPost, url, review, &result); err != nil {
		return false, err
	}
	return result.Status.Allowed, nil
}

// ValidateRBAC checks that the ServiceAccount has the minimum permissions
// required for K8s mode operation. Returns nil if all checks pass, or an
// error listing missing permissions.
func (c *Client) ValidateRBAC(ctx context.Context) error {
	type check struct {
		resource string
		verb     string
	}
	checks := []check{
		{"pods", "create"},
		{"pods", "get"},
		{"pods", "list"},
		{"pods", "delete"},
		{"pods/exec", "create"},
		{"pods/log", "get"},
		{"services", "create"},
		{"services", "delete"},
		{"configmaps", "create"},
		{"configmaps", "get"},
		{"configmaps", "list"},
		{"configmaps", "update"},
		{"configmaps", "delete"},
		{"secrets", "create"},
		{"secrets", "get"},
		{"secrets", "list"},
		{"secrets", "update"},
		{"secrets", "delete"},
		{"persistentvolumeclaims", "create"},
		{"persistentvolumeclaims", "delete"},
	}

	var missing []string
	for _, ch := range checks {
		allowed, err := c.CheckAccess(ctx, ch.resource, ch.verb)
		if err != nil {
			return fmt.Errorf("RBAC check failed for %s/%s: %w", ch.resource, ch.verb, err)
		}
		if !allowed {
			missing = append(missing, ch.verb+" "+ch.resource)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("ServiceAccount missing permissions: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// apiPrefix returns the API prefix for the given resource type.
func apiPrefix(resource string) string {
	switch resource {
	case "pods", "services", "configmaps", "secrets",
		"persistentvolumeclaims", "serviceaccounts":
		return "/api/v1"
	default:
		// Future: apps/v1 for deployments, etc.
		return "/api/v1"
	}
}

func (c *Client) collectionURL(resource string) string {
	return fmt.Sprintf("%s%s/namespaces/%s/%s",
		c.host, apiPrefix(resource), c.namespace, resource)
}

func (c *Client) resourceURL(resource, name string) string {
	return fmt.Sprintf("%s%s/namespaces/%s/%s/%s",
		c.host, apiPrefix(resource), c.namespace, resource, name)
}

func (c *Client) do(ctx context.Context, method, url string, body, result any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = strings.NewReader(string(data))
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.bearerToken())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Try to parse K8s Status response for a better error message.
		var status struct {
			Message string `json:"message"`
			Reason  string `json:"reason"`
			Code    int    `json:"code"`
		}
		if json.Unmarshal(respBody, &status) == nil && status.Code != 0 {
			return &StatusError{
				Code:    status.Code,
				Status:  status.Reason,
				Message: status.Message,
			}
		}
		return &StatusError{
			Code:    resp.StatusCode,
			Status:  resp.Status,
			Message: string(respBody),
		}
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
	}
	return nil
}
