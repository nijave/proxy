package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestResolveCatalogDir_Default(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home directory: %v", err)
	}

	got := resolveCatalogDir("")
	want := filepath.Join(home, ".config", "routatic-proxy", "catalog")
	if got != want {
		t.Fatalf("resolveCatalogDir(\"\") = %q, want %q", got, want)
	}
}

func TestResolveCatalogDir_FromConfigPath(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")

	got := resolveCatalogDir(configPath)
	want := filepath.Join(tmp, "catalog")
	if got != want {
		t.Fatalf("resolveCatalogDir(%q) = %q, want %q", configPath, got, want)
	}
}

func TestCatalogSyncCmd_Help(t *testing.T) {
	cmd := catalogSyncCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Download and cache the models.dev catalog") {
		t.Fatalf("help text missing expected description: %q", out)
	}
}

func TestCatalogCmd_Help(t *testing.T) {
	cmd := catalogCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "sync") {
		t.Fatalf("help text missing sync subcommand: %q", out)
	}
}

func TestCatalogSyncCmd_Success(t *testing.T) {
	catalogJSON := `{
  "models": {
    "claude-sonnet-4": {"providers": ["openrouter"]}
  },
  "providers": {
    "openrouter": {"base_url": "https://openrouter.ai/api/v1"}
  }
}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalog.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, catalogJSON)
	}))
	defer server.Close()

	// Override the package-level source URL for this test.
	oldURL := catalogSourceURL
	catalogSourceURL = server.URL + "/catalog.json"
	t.Cleanup(func() { catalogSourceURL = oldURL })

	catalogDir := filepath.Join(t.TempDir(), "catalog")

	root := catalogCmd()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"sync", "--config", filepath.Join(catalogDir, "config.json")})

	if err := root.Execute(); err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Catalog synced to") {
		t.Fatalf("output missing sync confirmation: %q", out)
	}
	if !strings.Contains(out, "SHA256:") {
		t.Fatalf("output missing SHA256: %q", out)
	}
	if !strings.Contains(out, "Bytes:") {
		t.Fatalf("output missing Bytes: %q", out)
	}
	if !strings.Contains(out, "TTL:") {
		t.Fatalf("output missing TTL: %q", out)
	}

	if _, err := os.Stat(filepath.Join(catalogDir, "catalog.json")); err != nil {
		t.Fatalf("catalog file not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(catalogDir, "catalog.lock.json")); err != nil {
		t.Fatalf("lock file not written: %v", err)
	}
}

func TestCatalogSyncCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	oldURL := catalogSourceURL
	catalogSourceURL = server.URL + "/catalog.json"
	t.Cleanup(func() { catalogSourceURL = oldURL })

	root := catalogCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"sync", "--config", filepath.Join(t.TempDir(), "config.json")})

	if err := root.Execute(); err == nil {
		t.Fatal("expected sync to fail on HTTP 500")
	} else if !strings.Contains(err.Error(), "catalog sync failed") {
		t.Fatalf("expected wrapped catalog sync error, got: %v", err)
	}
}

func TestCatalogSyncCmd_AddedToRoot(t *testing.T) {
	root := &cobra.Command{Use: "routatic-proxy"}
	root.AddCommand(catalogCmd())

	cmd, args, err := root.Find([]string{"catalog", "sync"})
	if err != nil {
		t.Fatalf("find catalog sync failed: %v", err)
	}
	if cmd.Name() != "sync" {
		t.Fatalf("expected sync command, got %q", cmd.Name())
	}
	if len(args) != 0 {
		t.Fatalf("unexpected args: %v", args)
	}
}
