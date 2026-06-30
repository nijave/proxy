package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func ptr(b bool) *bool { return &b }

func TestIndex_BuildProviderIndex_Valid(t *testing.T) {
	catalog := Catalog{
		Providers: map[string]Provider{
			"openai":    {Name: "openai", Enabled: nil},
			"anthropic": {Name: "anthropic", Enabled: ptr(true)},
			"disabled":  {Name: "disabled", Enabled: ptr(false)},
		},
		Models: map[string]Model{
			"gpt-4": {
				Name:      "gpt-4",
				Providers: []string{"openai"},
			},
			"claude-3": {
				Name:      "claude-3",
				Providers: []string{"anthropic", "anthropic"},
			},
			"gpt-3.5": {
				Name:      "gpt-3.5",
				Providers: []string{"openai"},
			},
		},
	}

	idx, err := BuildProviderIndex(catalog)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string][]string{
		"openai":    {"gpt-3.5", "gpt-4"},
		"anthropic": {"claude-3"},
	}

	if !reflect.DeepEqual(idx.ProviderModels, want) {
		t.Fatalf("index mismatch: got %+v, want %+v", idx.ProviderModels, want)
	}

	if _, ok := idx.ProviderModels["disabled"]; ok {
		t.Fatalf("expected disabled provider to be omitted")
	}
}

func TestIndex_NoEnabledProviders(t *testing.T) {
	catalog := Catalog{
		Providers: map[string]Provider{
			"disabled": {Name: "disabled", Enabled: ptr(false)},
		},
		Models: map[string]Model{
			"gpt-4": {Name: "gpt-4", Providers: []string{"disabled"}},
		},
	}

	_, err := BuildProviderIndex(catalog)
	if err == nil {
		t.Fatalf("expected error for no enabled providers, got nil")
	}
}

func TestIndex_EmptyModels(t *testing.T) {
	catalog := Catalog{
		Providers: map[string]Provider{
			"openai": {Name: "openai"},
		},
		Models: map[string]Model{},
	}

	_, err := BuildProviderIndex(catalog)
	if err == nil {
		t.Fatalf("expected error for empty models, got nil")
	}
}

func TestIndex_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	idx := &ProviderModelIndex{
		ProviderModels: map[string][]string{
			"openai": {"gpt-3.5", "gpt-4"},
		},
	}

	if err := idx.Write(dir); err != nil {
		t.Fatalf("write index: %v", err)
	}

	tmpPath := filepath.Join(dir, indexTmpFileName)
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("expected no temp file left behind, got %v", err)
	}

	read, err := ReadProviderIndex(dir)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}

	if !reflect.DeepEqual(read.ProviderModels, idx.ProviderModels) {
		t.Fatalf("read mismatch: got %+v, want %+v", read.ProviderModels, idx.ProviderModels)
	}
}
