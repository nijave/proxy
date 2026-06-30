package catalog

// Catalog is the parsed contents of a models.dev catalog.
type Catalog struct {
	Providers map[string]Provider `json:"providers"`
	Models    map[string]Model    `json:"models"`
	Scenarios map[string]Scenario `json:"scenarios"`
}

// Provider describes a model hosting endpoint.
type Provider struct {
	Name                   string `json:"name"`
	BaseURL                string `json:"base_url"`
	APIKey                 string `json:"api_key"`
	Enabled                *bool  `json:"enabled,omitempty"`
	AnthropicToolsDisabled bool   `json:"anthropic_tools_disabled"`
}

// Model describes a model available through one or more providers.
type Model struct {
	Name           string   `json:"name"`
	DisplayName    string   `json:"display_name"`
	Providers      []string `json:"providers"`
	ContextWindow  int64    `json:"context_window"`
	CostInputPerM  float64  `json:"cost_input_per_m"`
	CostOutputPerM float64  `json:"cost_output_per_m"`
	Tools          bool     `json:"tools"`
	Vision         bool     `json:"vision"`
	Reasoning      bool     `json:"reasoning"`
}

// Scenario describes a workload that selects a model by capability.
type Scenario struct {
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	RequiresTools      *bool    `json:"requires_tools,omitempty"`
	RequiresVision     *bool    `json:"requires_vision,omitempty"`
	RequiresReasoning  *bool    `json:"requires_reasoning,omitempty"`
	MinContextWindow   int64    `json:"min_context_window"`
	PreferredProviders []string `json:"preferred_providers"`
}

// Selector is a parsed model reference such as model@provider,
// lab/model@provider, or a short id.
type Selector struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Alias    string `json:"alias"`
}

// ResolvedModel is a fully materialized provider/model pair ready for use.
type ResolvedModel struct {
	Provider               string  `json:"provider"`
	ModelID                string  `json:"model_id"`
	CanonicalName          string  `json:"canonical_name"`
	DisplayName            string  `json:"display_name"`
	BaseURL                string  `json:"base_url"`
	APIKey                 string  `json:"api_key"`
	AnthropicToolsDisabled bool    `json:"anthropic_tools_disabled"`
	ContextWindow          int64   `json:"context_window"`
	CostInputPerM          float64 `json:"cost_input_per_m"`
	CostOutputPerM         float64 `json:"cost_output_per_m"`
	Tools                  bool    `json:"tools"`
	Vision                 bool    `json:"vision"`
	Reasoning              bool    `json:"reasoning"`
}
