package router

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/routatic/proxy/internal/catalog"
	"github.com/routatic/proxy/internal/config"
)

// selectorTestCatalog loads the shared fixture catalog used by selector tests.
func selectorTestCatalog(t *testing.T) *catalog.IndexedCatalog {
	t.Helper()
	cat, err := catalog.Load(filepath.Join("testdata", "selector_catalog.json"))
	if err != nil {
		t.Fatalf("load selector catalog: %v", err)
	}
	return cat
}

func TestSelectCheapest_SelectsCheapestModel(t *testing.T) {
	cfg := &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{APIKey: "go-key"},
		OpenRouter: config.OpenRouterConfig{APIKey: "or-key"},
	}
	selector := NewSelector(selectorTestCatalog(t), cfg)

	got, err := selector.SelectCheapest("default", ScenarioConstraints{})
	if err != nil {
		t.Fatalf("SelectCheapest returned error: %v", err)
	}

	want := "cheap-no-tools"
	if got.ModelID != want {
		t.Errorf("SelectCheapest(default) = %q, want %q", got.ModelID, want)
	}
	if got.Provider != "opencode-go" {
		t.Errorf("SelectCheapest(default) provider = %q, want %q", got.Provider, "opencode-go")
	}
	if got.CostInputPerM+got.CostOutputPerM != 2.0 {
		t.Errorf("SelectCheapest(default) total cost = %v, want 2.0", got.CostInputPerM+got.CostOutputPerM)
	}
}

func TestSelectCheapest_FiltersByToolsConstraint(t *testing.T) {
	cfg := &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{APIKey: "go-key"},
	}
	selector := NewSelector(selectorTestCatalog(t), cfg)

	got, err := selector.SelectCheapest("default", ScenarioConstraints{Tools: true})
	if err != nil {
		t.Fatalf("SelectCheapest returned error: %v", err)
	}

	want := "cheap-tools"
	if got.ModelID != want {
		t.Errorf("SelectCheapest(default, tools) = %q, want %q", got.ModelID, want)
	}
	if !got.Tools {
		t.Errorf("SelectCheapest(default, tools).Tools = false, want true")
	}
}

func TestSelectCheapest_FiltersByVisionConstraint(t *testing.T) {
	cfg := &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{APIKey: "go-key"},
		OpenRouter: config.OpenRouterConfig{APIKey: "or-key"},
	}
	selector := NewSelector(selectorTestCatalog(t), cfg)

	got, err := selector.SelectCheapest("default", ScenarioConstraints{Vision: true})
	if err != nil {
		t.Fatalf("SelectCheapest returned error: %v", err)
	}

	want := "vision-model"
	if got.ModelID != want {
		t.Errorf("SelectCheapest(default, vision) = %q, want %q", got.ModelID, want)
	}
	if !got.Vision {
		t.Errorf("SelectCheapest(default, vision).Vision = false, want true")
	}
}

func TestSelectCheapest_FiltersByReasoningConstraint(t *testing.T) {
	cfg := &config.Config{
		OpenRouter: config.OpenRouterConfig{APIKey: "or-key"},
	}
	selector := NewSelector(selectorTestCatalog(t), cfg)

	got, err := selector.SelectCheapest("default", ScenarioConstraints{Reasoning: true})
	if err != nil {
		t.Fatalf("SelectCheapest returned error: %v", err)
	}

	want := "reasoning-model"
	if got.ModelID != want {
		t.Errorf("SelectCheapest(default, reasoning) = %q, want %q", got.ModelID, want)
	}
	if !got.Reasoning {
		t.Errorf("SelectCheapest(default, reasoning).Reasoning = false, want true")
	}
}

func TestSelectCheapest_FiltersByContextConstraint(t *testing.T) {
	cfg := &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{APIKey: "go-key"},
		OpenRouter: config.OpenRouterConfig{APIKey: "or-key"},
	}
	selector := NewSelector(selectorTestCatalog(t), cfg)

	got, err := selector.SelectCheapest("default", ScenarioConstraints{Context: 500000})
	if err != nil {
		t.Fatalf("SelectCheapest returned error: %v", err)
	}

	want := "large-context"
	if got.ModelID != want {
		t.Errorf("SelectCheapest(default, context=500000) = %q, want %q", got.ModelID, want)
	}
	if got.ContextWindow < 500000 {
		t.Errorf("SelectCheapest(default, context=500000).ContextWindow = %d, want >= 500000", got.ContextWindow)
	}
}

func TestSelectCheapest_FiltersByScenarioRequirements(t *testing.T) {
	cfg := &config.Config{
		OpenRouter: config.OpenRouterConfig{APIKey: "or-key"},
	}
	selector := NewSelector(selectorTestCatalog(t), cfg)

	tests := []struct {
		scenario string
		want     string
	}{
		{"vision_required", "vision-model"},
		{"reasoning_required", "reasoning-model"},
	}

	for _, tt := range tests {
		t.Run(tt.scenario, func(t *testing.T) {
			got, err := selector.SelectCheapest(tt.scenario, ScenarioConstraints{})
			if err != nil {
				t.Fatalf("SelectCheapest returned error: %v", err)
			}
			if got.ModelID != tt.want {
				t.Errorf("SelectCheapest(%q) = %q, want %q", tt.scenario, got.ModelID, tt.want)
			}
		})
	}
}

func TestSelectCheapest_EnabledProvidersOnly(t *testing.T) {
	cat := selectorTestCatalog(t)

	tests := []struct {
		name        string
		cfg         *config.Config
		scenario    string
		constraints ScenarioConstraints
		want        string
		wantErr     bool
	}{
		{
			name: "provider-specific key enables only that provider",
			cfg: &config.Config{
				OpenCodeGo: config.OpenCodeGoConfig{APIKey: "go-key"},
			},
			scenario:    "default",
			constraints: ScenarioConstraints{},
			want:        "cheap-no-tools",
		},
		{
			name: "openrouter-only key excludes opencode-go models",
			cfg: &config.Config{
				OpenRouter: config.OpenRouterConfig{APIKey: "or-key"},
			},
			scenario:    "default",
			constraints: ScenarioConstraints{},
			want:        "large-context",
		},
		{
			name: "global key enables all providers",
			cfg: &config.Config{
				APIKey: "global-key",
			},
			scenario:    "default",
			constraints: ScenarioConstraints{},
			want:        "cheap-no-tools",
		},
		{
			name: "disabled catalog provider is ignored even with key",
			cfg: &config.Config{
				APIKeys: []string{"global-key"},
			},
			scenario:    "default",
			constraints: ScenarioConstraints{},
			want:        "cheap-no-tools",
		},
		{
			name: "no keys disables all providers",
			cfg: &config.Config{
				OpenCodeGo: config.OpenCodeGoConfig{},
				OpenRouter: config.OpenRouterConfig{},
			},
			scenario:    "default",
			constraints: ScenarioConstraints{},
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := NewSelector(cat, tt.cfg)
			got, err := selector.SelectCheapest(tt.scenario, tt.constraints)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("SelectCheapest expected error, got model %q", got.ModelID)
				}
				return
			}
			if err != nil {
				t.Fatalf("SelectCheapest returned error: %v", err)
			}
			if got.ModelID != tt.want {
				t.Errorf("SelectCheapest(%q) = %q, want %q", tt.scenario, got.ModelID, tt.want)
			}
		})
	}
}

func TestSelectCheapest_PreferredProvidersFilter(t *testing.T) {
	cfg := &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{APIKey: "go-key"},
		OpenRouter: config.OpenRouterConfig{APIKey: "or-key"},
	}
	selector := NewSelector(selectorTestCatalog(t), cfg)

	got, err := selector.SelectCheapest("preferred_only", ScenarioConstraints{})
	if err != nil {
		t.Fatalf("SelectCheapest returned error: %v", err)
	}

	// openrouter models only, cheapest is large-context at cost 3.0
	if got.Provider != "openrouter" {
		t.Errorf("SelectCheapest(preferred_only) provider = %q, want %q", got.Provider, "openrouter")
	}
	if got.ModelID != "large-context" {
		t.Errorf("SelectCheapest(preferred_only) = %q, want %q", got.ModelID, "large-context")
	}
}

func TestSelectCheapest_NoCandidateReturnsError(t *testing.T) {
	cfg := &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{APIKey: "go-key"},
	}
	selector := NewSelector(selectorTestCatalog(t), cfg)

	_, err := selector.SelectCheapest("default", ScenarioConstraints{Reasoning: true})
	if err == nil {
		t.Fatal("SelectCheapest expected error for unmatched constraints, got nil")
	}
	if !errors.Is(err, ErrNoCandidateModel) {
		t.Errorf("SelectCheapest error = %v, want ErrNoCandidateModel", err)
	}
}

func TestSelectCheapest_UnknownScenarioReturnsError(t *testing.T) {
	cfg := &config.Config{
		OpenCodeGo: config.OpenCodeGoConfig{APIKey: "go-key"},
	}
	selector := NewSelector(selectorTestCatalog(t), cfg)

	_, err := selector.SelectCheapest("does-not-exist", ScenarioConstraints{})
	if err == nil {
		t.Fatal("SelectCheapest expected error for unknown scenario, got nil")
	}
}
