package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/routatic/proxy/internal/storage"
)

// providerSubmodelFixture mirrors the models.dev shape where Cloudflare Workers
// AI and Amazon Bedrock expose their catalogs only inside providers[<id>].models
// and the top-level models map holds none of them.
const providerSubmodelFixture = `{
  "providers": {
    "cloudflare-workers-ai": {
      "name": "Cloudflare Workers AI",
      "models": {"@cf/ibm-granite/granite-4.0-h-micro": {"id": "@cf/ibm-granite/granite-4.0-h-micro", "name": "Granite 4.0 H Micro", "tool_call": true}}
    },
    "amazon-bedrock": {
      "name": "Amazon Bedrock",
      "models": {"anthropic.claude-sonnet-4-20250514-v1:0": {"id": "anthropic.claude-sonnet-4-20250514-v1:0", "name": "Claude Sonnet 4", "tool_call": true}}
    }
  },
  "models": {}
}`

// TestMigrateFromJSON_IngestsProviderSubmodels covers the runtime path
// (catalog sync -> MigrateFromJSON -> LoadFromSQLite -> Resolve): a catalog
// whose Cloudflare and Bedrock models live only in provider sub-maps must
// resolve to the canonical "cloudflare" and "aws-bedrock" providers.
func TestMigrateFromJSON_IngestsProviderSubmodels(t *testing.T) {
	tmp := t.TempDir()
	catalogDir := filepath.Join(tmp, "catalog")
	if err := os.MkdirAll(catalogDir, 0755); err != nil {
		t.Fatalf("mkdir catalog: %v", err)
	}
	jsonPath := filepath.Join(catalogDir, "catalog.json")
	if err := os.WriteFile(jsonPath, []byte(providerSubmodelFixture), 0644); err != nil {
		t.Fatalf("write catalog: %v", err)
	}

	storageCfg := storage.DefaultConfig
	storageCfg.DatabasePath = filepath.Join(tmp, "data.db")
	db, err := storage.Open(storageCfg)
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if _, err := MigrateFromJSON(ctx, db, jsonPath); err != nil {
		t.Fatalf("MigrateFromJSON: %v", err)
	}

	idx, err := LoadFromSQLite(ctx, db)
	if err != nil {
		t.Fatalf("LoadFromSQLite: %v", err)
	}

	cf, err := idx.Resolve(Selector{Provider: "cloudflare", Model: "@cf/ibm-granite/granite-4.0-h-micro"})
	if err != nil {
		t.Fatalf("resolve cloudflare model: %v", err)
	}
	if cf.Provider != "cloudflare" {
		t.Errorf("cloudflare resolved.Provider = %q, want %q", cf.Provider, "cloudflare")
	}

	bedrock, err := idx.Resolve(Selector{Provider: "aws-bedrock", Model: "anthropic.claude-sonnet-4-20250514-v1:0"})
	if err != nil {
		t.Fatalf("resolve bedrock model: %v", err)
	}
	if bedrock.Provider != "aws-bedrock" {
		t.Errorf("bedrock resolved.Provider = %q, want %q", bedrock.Provider, "aws-bedrock")
	}
}

// models.dev stores reseller/gateway providers' catalogs inside
// providers[<id>].models, keyed by provider id ("cloudflare-workers-ai",
// "amazon-bedrock") that differs from routatic's canonical provider names
// ("cloudflare", "aws-bedrock"). These tests exercise the real Load path to
// prove those models are ingested into the flat Models map under the canonical
// provider prefix and resolve to the canonical provider.

func TestLoad_IngestsCloudflareWorkersAIProviderModels(t *testing.T) {
	const modelID = "@cf/ibm-granite/granite-4.0-h-micro"
	path := writeTempCatalog(t, Catalog{
		Providers: map[string]Provider{
			"cloudflare-workers-ai": {
				Name: "Cloudflare Workers AI",
				Models: map[string]Model{
					modelID: {ID: modelID, Name: "Granite 4.0 H Micro", ToolCall: true},
				},
			},
		},
		Models: map[string]Model{},
	})

	idx, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantKey := "cloudflare/" + modelID
	if _, ok := idx.Models[wantKey]; !ok {
		t.Fatalf("expected flat Models to contain %q; got keys %v", wantKey, keysOf(idx.Models))
	}
	if _, ok := idx.Providers["cloudflare"]; !ok {
		t.Fatalf("expected Providers to contain canonical %q; got %v", "cloudflare", keysOfProviders(idx.Providers))
	}

	resolved, err := idx.Resolve(Selector{Provider: "cloudflare", Model: modelID})
	if err != nil {
		t.Fatalf("resolve cloudflare model: %v", err)
	}
	if resolved.Provider != "cloudflare" {
		t.Errorf("resolved.Provider = %q, want %q", resolved.Provider, "cloudflare")
	}
}

func TestLoad_IngestsAmazonBedrockProviderModels(t *testing.T) {
	const modelID = "anthropic.claude-sonnet-4-20250514-v1:0"
	path := writeTempCatalog(t, Catalog{
		Providers: map[string]Provider{
			"amazon-bedrock": {
				Name: "Amazon Bedrock",
				Models: map[string]Model{
					modelID: {ID: modelID, Name: "Claude Sonnet 4", ToolCall: true},
				},
			},
		},
		Models: map[string]Model{},
	})

	idx, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantKey := "aws-bedrock/" + modelID
	if _, ok := idx.Models[wantKey]; !ok {
		t.Fatalf("expected flat Models to contain %q; got keys %v", wantKey, keysOf(idx.Models))
	}

	resolved, err := idx.Resolve(Selector{Provider: "aws-bedrock", Model: modelID})
	if err != nil {
		t.Fatalf("resolve bedrock model: %v", err)
	}
	if resolved.Provider != "aws-bedrock" {
		t.Errorf("resolved.Provider = %q, want %q", resolved.Provider, "aws-bedrock")
	}
}

func TestLoad_SkipsSubmodelsForUnknownProvider(t *testing.T) {
	path := writeTempCatalog(t, Catalog{
		Providers: map[string]Provider{
			// A provider routatic cannot dispatch to; its sub-map models
			// must not pollute the flat catalog.
			"some-random-lab": {
				Name: "Some Random Lab",
				Models: map[string]Model{
					"foo-1": {ID: "foo-1", Name: "Foo 1"},
				},
			},
			// A known provider so the catalog is non-empty and valid.
			"opencode-go": {
				Name:   "opencode-go",
				Models: map[string]Model{"deepseek-v4-flash": {ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash"}},
			},
		},
		Models: map[string]Model{},
	})

	idx, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := idx.Models["some-random-lab/foo-1"]; ok {
		t.Errorf("unexpected ingestion of unknown-provider sub-model: %v", keysOf(idx.Models))
	}
	if _, ok := idx.Models["opencode-go/deepseek-v4-flash"]; !ok {
		t.Errorf("expected known-provider sub-model to be ingested; got %v", keysOf(idx.Models))
	}
}

func TestLoad_FlatModelWinsOverProviderSubmodel(t *testing.T) {
	const key = "opencode-go/deepseek-v4-flash"
	path := writeTempCatalog(t, Catalog{
		Providers: map[string]Provider{
			"opencode-go": {
				Name:   "opencode-go",
				Models: map[string]Model{"deepseek-v4-flash": {ID: "deepseek-v4-flash", Name: "SUBMAP"}},
			},
		},
		Models: map[string]Model{
			key: {ID: key, Name: "FLAT"},
		},
	})

	idx, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := idx.Models[key].Name; got != "FLAT" {
		t.Errorf("flat model overwritten by sub-map: Name = %q, want %q", got, "FLAT")
	}
}

func TestLoad_NormalizesIdentityProviderName(t *testing.T) {
	// opencode-go's models also live only in its sub-map, and models.dev labels
	// the provider "OpenCode Go". Resolution must report the canonical
	// "opencode-go" so the dispatcher (which matches on the canonical name)
	// recognizes it — not the models.dev display name.
	path := writeTempCatalog(t, Catalog{
		Providers: map[string]Provider{
			"opencode-go": {
				Name:   "OpenCode Go",
				Models: map[string]Model{"deepseek-v4-flash": {ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash"}},
			},
		},
		Models: map[string]Model{},
	})

	idx, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resolved, err := idx.Resolve(Selector{Provider: "opencode-go", Model: "deepseek-v4-flash"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Provider != "opencode-go" {
		t.Errorf("resolved.Provider = %q, want %q", resolved.Provider, "opencode-go")
	}
}

func TestLoad_ExcludesCloudflareAIGatewaySubmodels(t *testing.T) {
	// cloudflare-ai-gateway model ids are already namespaced ("anthropic/...",
	// "workers-ai/@cf/..."), so they must NOT be folded under the canonical
	// cloudflare provider, where the workers-ai/ prefixing would corrupt them.
	path := writeTempCatalog(t, Catalog{
		Providers: map[string]Provider{
			"cloudflare-ai-gateway": {
				Name:   "Cloudflare AI Gateway",
				Models: map[string]Model{"anthropic/claude-opus-4.8": {ID: "anthropic/claude-opus-4.8", Name: "Claude Opus 4.8"}},
			},
			// A known provider so the catalog validates.
			"opencode-go": {
				Name:   "opencode-go",
				Models: map[string]Model{"deepseek-v4-flash": {ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash"}},
			},
		},
		Models: map[string]Model{},
	})

	idx, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, k := range []string{
		"cloudflare/anthropic/claude-opus-4.8",
		"cloudflare-ai-gateway/anthropic/claude-opus-4.8",
	} {
		if _, ok := idx.Models[k]; ok {
			t.Errorf("cloudflare-ai-gateway model wrongly ingested as %q; keys=%v", k, keysOf(idx.Models))
		}
	}
	if _, ok := idx.Providers["cloudflare"]; ok {
		t.Errorf("unexpected canonical cloudflare provider created from ai-gateway")
	}
}

func keysOf(m map[string]Model) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOfProviders(m map[string]Provider) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
