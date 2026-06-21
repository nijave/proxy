// Package main is the hosted/entrypoint for routatic-proxy on Railway/serverless platforms.
// It runs with zero local configuration - all settings come from environment variables
// and per-request cloud API calls.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/routatic/proxy/internal/auth"
	"github.com/routatic/proxy/internal/config"
)

const (
	defaultPort           = 3456
	defaultCloudBaseURL   = "https://api.routatic.cloud"
	authIntrospectionPath = "/v1/auth/introspect"
	configSnapshotPath    = "/v1/config/snapshot"
	metricsEndpointPath   = "/v1/metrics/ingest"
	defaultCloudTimeout   = 30 * time.Second
)

// contextKey is a typed key for context values to avoid collisions
type contextKey string

//nolint:gosec // These are context keys, not credentials
const (
	workspaceContextKey contextKey = "workspace_id"
)

// HostedConfig holds the minimal configuration for hosted mode.
// All values come from environment variables.
type HostedConfig struct {
	Port         int
	CloudBaseURL string
	ServiceToken string
	LogLevel     string
}

// loadHostedConfig creates configuration from environment variables.
// No config files are read - everything comes from env vars.
func loadHostedConfig() (*HostedConfig, error) {
	cfg := &HostedConfig{
		Port:         getEnvInt("PORT", defaultPort),
		CloudBaseURL: getEnv("ROUTATIC_CLOUD_BASE_URL", defaultCloudBaseURL),
		ServiceToken: getEnv("ROUTATIC_SERVICE_TOKEN", ""),
		LogLevel:     getEnv("LOG_LEVEL", "info"),
	}

	if cfg.ServiceToken == "" {
		return nil, fmt.Errorf("ROUTATIC_SERVICE_TOKEN is required")
	}

	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultValue
}

// CloudAuthProvider validates API keys via the Routatic Cloud introspection endpoint
type CloudAuthProvider struct {
	baseURL      string
	serviceToken string
	httpClient   *http.Client
}

// NewCloudAuthProvider creates a new cloud-based auth provider
func NewCloudAuthProvider(baseURL, serviceToken string) *CloudAuthProvider {
	return &CloudAuthProvider{
		baseURL:      baseURL,
		serviceToken: serviceToken,
		httpClient:   &http.Client{Timeout: defaultCloudTimeout},
	}
}

// AuthResponse represents the cloud auth introspection response
type AuthResponse struct {
	Active      bool   `json:"active"`
	WorkspaceID string `json:"workspace_id"`
	KeyID       string `json:"key_id"`
	Role        string `json:"role"`
}

// ValidateAPIKey validates an API key with the cloud service
func (p *CloudAuthProvider) ValidateAPIKey(ctx context.Context, apiKey string) (*AuthResponse, error) {
	url := p.baseURL + authIntrospectionPath

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.serviceToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling auth endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("auth failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var authResp AuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if !authResp.Active {
		return nil, fmt.Errorf("API key is not active")
	}

	return &authResp, nil
}

// CloudConfigProvider fetches configuration from the Routatic Cloud
type CloudConfigProvider struct {
	baseURL      string
	serviceToken string
	httpClient   *http.Client
	cache        *config.CachedConfigProvider
}

// NewCloudConfigProvider creates a new cloud-based config provider
func NewCloudConfigProvider(baseURL, serviceToken string) *CloudConfigProvider {
	rawProvider := &cloudConfigRawProvider{
		baseURL:      baseURL,
		serviceToken: serviceToken,
		httpClient:   &http.Client{Timeout: defaultCloudTimeout},
	}

	// Wrap with caching (5 minute TTL)
	cached := config.NewCachedConfigProvider(rawProvider, 5*time.Minute)

	return &CloudConfigProvider{
		baseURL:      baseURL,
		serviceToken: serviceToken,
		httpClient:   rawProvider.httpClient,
		cache:        cached,
	}
}

// HealthCheck is a no-op for hosted mode - health checking is handled externally.
// The underlying raw provider implements the actual interface method.
func (p *CloudConfigProvider) HealthCheck(ctx context.Context) error {
	return p.cache.HealthCheck(ctx)
}

// cloudConfigRawProvider is the underlying provider that actually fetches from cloud
type cloudConfigRawProvider struct {
	baseURL      string
	serviceToken string
	httpClient   *http.Client
}

func (p *cloudConfigRawProvider) GetEffectiveConfig(ctx context.Context, authCtx *auth.AuthContext) (*config.RuntimeConfig, error) {
	// Get workspace from auth context or use default
	workspaceID := "default"
	if authCtx != nil {
		workspaceID = authCtx.WorkspaceID
	}

	url := p.baseURL + configSnapshotPath + "?workspace=" + workspaceID

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.serviceToken)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching config: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("config fetch failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var runtimeConfig config.RuntimeConfig
	if err := json.NewDecoder(resp.Body).Decode(&runtimeConfig); err != nil {
		return nil, fmt.Errorf("decoding config: %w", err)
	}

	return &runtimeConfig, nil
}

// GetConfigByRef fetches configuration by specific workspace reference.
// Uses ref.WorkspaceID if provided, otherwise delegates to GetEffectiveConfig behavior.
func (p *cloudConfigRawProvider) GetConfigByRef(ctx context.Context, ref config.ConfigRef) (*config.RuntimeConfig, error) {
	// Use workspace from ref if provided, otherwise default
	workspaceID := ref.WorkspaceID
	if workspaceID == "" {
		workspaceID = "default"
	}

	// Build URL with optional version query param for cache validation
	url := p.baseURL + configSnapshotPath + "?workspace=" + workspaceID
	if ref.Version != "" {
		url += "&version=" + ref.Version
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.serviceToken)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching config: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("config fetch failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var runtimeConfig config.RuntimeConfig
	if err := json.NewDecoder(resp.Body).Decode(&runtimeConfig); err != nil {
		return nil, fmt.Errorf("decoding config: %w", err)
	}

	return &runtimeConfig, nil
}

func (p *cloudConfigRawProvider) Invalidate(ctx context.Context, workspaceID, version string) error {
	return nil // Cloud provider manages its own caching
}

// HealthCheck is a no-op for hosted mode - health checking is handled externally.
// Required to satisfy ConfigProvider interface.
func (p *cloudConfigRawProvider) HealthCheck(ctx context.Context) error {
	return nil
}

// MetricsReporter sends usage metrics to the cloud
type MetricsReporter struct {
	baseURL      string
	serviceToken string
	httpClient   *http.Client
}

// NewMetricsReporter creates a new metrics reporter
func NewMetricsReporter(baseURL, serviceToken string) *MetricsReporter {
	return &MetricsReporter{
		baseURL:      baseURL,
		serviceToken: serviceToken,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

// ReportRequest sends request metrics to the cloud
func (r *MetricsReporter) ReportRequest(ctx context.Context, req MetricsRequest) error {
	url := r.baseURL + metricsEndpointPath

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling metrics: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+r.serviceToken)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("sending metrics: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("metrics endpoint returned status %d", resp.StatusCode)
	}

	return nil
}

// MetricsRequest represents usage metrics to report
type MetricsRequest struct {
	WorkspaceID  string    `json:"workspace_id"`
	KeyID        string    `json:"key_id"`
	ModelID      string    `json:"model_id"`
	TokensUsed   int       `json:"tokens_used"`
	TokensInput  int       `json:"tokens_input"`
	TokensOutput int       `json:"tokens_output"`
	DurationMs   int64     `json:"duration_ms"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

// HostedServer wraps the standard server with cloud-specific functionality
type HostedServer struct {
	cfg             *HostedConfig
	authProvider    *CloudAuthProvider
	configProvider  *CloudConfigProvider
	metricsReporter *MetricsReporter
	httpServer      *http.Server
}

// NewHostedServer creates a new hosted server instance
func NewHostedServer(cfg *HostedConfig) (*HostedServer, error) {
	authProvider := NewCloudAuthProvider(cfg.CloudBaseURL, cfg.ServiceToken)
	configProvider := NewCloudConfigProvider(cfg.CloudBaseURL, cfg.ServiceToken)
	metricsReporter := NewMetricsReporter(cfg.CloudBaseURL, cfg.ServiceToken)

	return &HostedServer{
		cfg:             cfg,
		authProvider:    authProvider,
		configProvider:  configProvider,
		metricsReporter: metricsReporter,
	}, nil
}

// Start initializes and starts the HTTP server
func (s *HostedServer) Start() error {
	// Set up logging
	levelVar := new(slog.LevelVar)
	levelVar.Set(parseLogLevel(s.cfg.LogLevel))

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: levelVar,
	}))
	slog.SetDefault(logger)

	// Create HTTP mux
	mux := http.NewServeMux()

	// Main proxy endpoint - authenticates and proxies requests
	mux.HandleFunc("/v1/messages", s.handleProxy)
	mux.HandleFunc("/v1/chat/completions", s.handleProxy)

	// Create server
	addr := fmt.Sprintf(":%d", s.cfg.Port)
	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 0, // No timeout for streaming
		IdleTimeout:  300 * time.Second,
	}

	slog.Info("starting hosted routatic-proxy",
		"port", s.cfg.Port,
		"cloud_base_url", s.cfg.CloudBaseURL,
	)

	return s.httpServer.ListenAndServe()
}

// handleProxy authenticates and proxies requests
func (s *HostedServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()

	// Extract API key from Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		http.Error(w, "missing authorization", http.StatusUnauthorized)
		return
	}

	// Extract Bearer token
	var apiKey string
	if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
		apiKey = authHeader[7:]
	} else {
		apiKey = authHeader
	}

	// Validate API key with cloud
	authResp, err := s.authProvider.ValidateAPIKey(ctx, apiKey)
	if err != nil {
		slog.Error("auth validation failed", "error", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Store workspace in context for config provider
	ctx = context.WithValue(ctx, workspaceContextKey, authResp.WorkspaceID)

	// Fetch config for this workspace
	runtimeConfig, err := s.configProvider.cache.GetEffectiveConfig(ctx, nil)
	if err != nil {
		slog.Error("config fetch failed", "error", err)
		http.Error(w, "configuration error", http.StatusInternalServerError)
		return
	}

	// TODO: Proxy the request using the runtime configuration
	// This would integrate with the existing proxy handlers
	slog.Info("request authenticated",
		"workspace", authResp.WorkspaceID,
		"key_id", authResp.KeyID,
		"config_version", runtimeConfig.Version,
	)

	// Report metrics after request completes
	defer func() {
		duration := time.Since(start).Milliseconds()
		metrics := MetricsRequest{
			WorkspaceID: authResp.WorkspaceID,
			KeyID:       authResp.KeyID,
			DurationMs:  duration,
			Timestamp:   time.Now(),
		}

		if err := s.metricsReporter.ReportRequest(context.Background(), metrics); err != nil {
			slog.Error("failed to report metrics", "error", err)
		}
	}()

	// For now, return a stub response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{
		"status":    "ok",
		"workspace": authResp.WorkspaceID,
		"message":   "proxy endpoint - integrate with existing handlers",
	}); err != nil {
		slog.Error("failed to encode response", "error", err)
	}
}

// parseLogLevel parses a log level string
func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func main() {
	cfg, err := loadHostedConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	server, err := NewHostedServer(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := server.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
