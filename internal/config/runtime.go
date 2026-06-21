// Package config provides configuration management for the routatic-proxy.
package config

import "encoding/json"

// RuntimeConfig is compiled for fast request handling.
// It represents the fully-resolved configuration for a specific workspace/version.
type RuntimeConfig struct {
	WorkspaceID     string                       `json:"workspace_id"`
	Version         string                       `json:"version"` // no ETag for v1
	Supermodels     map[string]Supermodel        `json:"supermodels"`
	Providers       map[string]ProviderConfig    `json:"providers"`
	RoutingPolicies []RoutingPolicy              `json:"routing_policies"`
	CapabilityIndex map[string]ModelCapabilities `json:"capability_index"`
	LoggingPolicy   LoggingPolicy                `json:"logging_policy"`
	Enforcement     EnforcementPolicy            `json:"enforcement"`
}

// Supermodel defines a high-level model abstraction that maps to one or more
// concrete model configurations based on routing scenarios.
type Supermodel struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description,omitempty"`
	Default     ModelConfig               `json:"default"`
	Scenarios   map[string]ScenarioConfig `json:"scenarios,omitempty"`
}

// ScenarioConfig defines model configuration for a specific routing scenario.
type ScenarioConfig struct {
	ModelID         string          `json:"model_id"`
	Provider        string          `json:"provider"`
	Temperature     float64         `json:"temperature,omitempty"`
	MaxTokens       int             `json:"max_tokens,omitempty"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	ContextWindow   int             `json:"context_window,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	Thinking        json.RawMessage `json:"thinking,omitempty"`
}

// ProviderConfig defines an upstream LLM provider.
// This is similar to OpenCodeGoConfig/OpenCodeZenConfig but generalized.
type ProviderConfig struct {
	Name             string            `json:"name"`
	Type             string            `json:"type"` // "opencode-go", "opencode-zen", "aws-bedrock", etc.
	BaseURL          string            `json:"base_url"`
	AnthropicBaseURL string            `json:"anthropic_base_url,omitempty"`
	ResponsesBaseURL string            `json:"responses_base_url,omitempty"`
	GeminiBaseURL    string            `json:"gemini_base_url,omitempty"`
	APIKey           string            `json:"api_key,omitempty"`
	APIKeys          []string          `json:"api_keys,omitempty"`
	TimeoutMs        int               `json:"timeout_ms"`
	StreamTimeoutMs  int               `json:"stream_timeout_ms,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
}

// RoutingPolicy defines rules for selecting models based on request characteristics.
type RoutingPolicy struct {
	Name          string            `json:"name"`
	Priority      int               `json:"priority"` // Higher = evaluated first
	Conditions    RoutingConditions `json:"conditions"`
	TargetModel   string            `json:"target_model"`
	FallbackChain []string          `json:"fallback_chain,omitempty"`
}

// RoutingConditions defines matching criteria for routing policies.
type RoutingConditions struct {
	Scenarios        []string `json:"scenarios,omitempty"` // "long_context", "complex", "think", "vision", etc.
	MinTokens        int      `json:"min_tokens,omitempty"`
	MaxTokens        int      `json:"max_tokens,omitempty"`
	HasVision        *bool    `json:"has_vision,omitempty"`
	HasTools         *bool    `json:"has_tools,omitempty"`
	Streaming        *bool    `json:"streaming,omitempty"`
	TokenThreshold   int      `json:"token_threshold,omitempty"`   // Min token count for this policy
	ContextThreshold int      `json:"context_threshold,omitempty"` // Min context size needed
}

// ModelCapabilities describes what a model can do.
type ModelCapabilities struct {
	ModelID           string   `json:"model_id"`
	Provider          string   `json:"provider"`
	MaxContextWindow  int      `json:"max_context_window"`
	MaxOutputTokens   int      `json:"max_output_tokens"`
	SupportsTools     bool     `json:"supports_tools"`
	SupportsVision    bool     `json:"supports_vision"`
	SupportsStreaming bool     `json:"supports_streaming"`
	SupportsThinking  bool     `json:"supports_thinking"`
	ReasoningEfforts  []string `json:"reasoning_efforts,omitempty"` // "low", "medium", "high"
	WireFormats       []string `json:"wire_formats,omitempty"`      // "openai", "anthropic", "responses", "gemini"
}

// LoggingPolicy controls request/response logging behavior.
type LoggingPolicy struct {
	Level            string   `json:"level"` // "debug", "info", "warn", "error"
	LogRequests      bool     `json:"log_requests"`
	LogResponses     bool     `json:"log_responses"`
	LogLatency       bool     `json:"log_latency"`
	LogRateLimits    bool     `json:"log_rate_limits"`
	PIIMasking       bool     `json:"pii_masking"`
	SensitiveHeaders []string `json:"sensitive_headers,omitempty"`
}

// EnforcementPolicy defines security and compliance enforcement rules.
type EnforcementPolicy struct {
	RequireAuth           bool `json:"require_auth"`
	EnforceModelAllowlist bool `json:"enforce_model_allowlist"`
	EnforceBudgets        bool `json:"enforce_budgets"`
	EnforceRateLimits     bool `json:"enforce_rate_limits"`
}

// EffectiveAPIKeys returns the pool of API keys for rotation.
// APIKeys takes precedence; falls back to the single APIKey field.
func (pc *ProviderConfig) EffectiveAPIKeys() []string {
	if len(pc.APIKeys) > 0 {
		return pc.APIKeys
	}
	if pc.APIKey != "" {
		return []string{pc.APIKey}
	}
	return nil
}
