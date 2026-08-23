package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchConfig_DetectsFileChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	initialJSON := `{"api_key": "watcher-test"}`
	if err := os.WriteFile(path, []byte(initialJSON), 0644); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	cfg, err := LoadFromPath(path)
	if err != nil {
		t.Fatalf("LoadFromPath failed: %v", err)
	}

	at := NewAtomicConfig(cfg, path)

	// Watch for reload via callback instead of polling.
	reloaded := make(chan struct{}, 1)
	at.OnReload(func(_ *Config) {
		select {
		case reloaded <- struct{}{}:
		default:
		}
	})

	// Start watcher in background
	go func() {
		if err := WatchConfig(t.Context(), at); err != nil && err != context.Canceled {
			t.Logf("WatchConfig returned: %v", err)
		}
	}()

	// Give watcher time to set up
	time.Sleep(200 * time.Millisecond)

	// Modify config file
	updatedJSON := `{"api_key": "watcher-updated"}`
	if err := os.WriteFile(path, []byte(updatedJSON), 0644); err != nil {
		t.Fatalf("failed to write updated config: %v", err)
	}

	// Wait for reload notification with timeout
	select {
	case <-reloaded:
		// Reload invokes callbacks before swapping the config in, so
		// poll briefly for the new value to become visible.
		deadline := time.Now().Add(5 * time.Second)
		for at.Get().APIKey != "watcher-updated" {
			if time.Now().After(deadline) {
				t.Fatalf("after reload callback, APIKey = %q, want %q", at.Get().APIKey, "watcher-updated")
			}
			time.Sleep(10 * time.Millisecond)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("config was not reloaded after file change")
	}
}
