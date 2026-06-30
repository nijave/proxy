package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/routatic/proxy/internal/catalog"
	"github.com/spf13/cobra"
)

// catalogSourceURL is the default models.dev catalog URL. It is a package
// variable so tests can override it with a local server endpoint.
var catalogSourceURL = "https://models.dev/catalog.json"

// catalogCmd returns the top-level catalog command.
func catalogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Manage the models.dev catalog cache",
		Long: `Download and cache the models.dev catalog locally.

The catalog is stored under ~/.config/routatic-proxy/catalog by default and is
used to resolve canonical model names and providers at runtime.`,
	}

	cmd.AddCommand(catalogSyncCmd())
	cmd.PersistentFlags().StringP("config", "c", "", "Path to config file (used to locate the catalog directory)")

	return cmd
}

// catalogSyncCmd returns the command to sync the models.dev catalog.
func catalogSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Download and cache the models.dev catalog",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, err := cmd.Flags().GetString("config")
			if err != nil {
				return fmt.Errorf("read config flag: %w", err)
			}

			catalogDir := resolveCatalogDir(configPath)
			lock, err := catalog.Sync(catalogSourceURL, catalogDir)
			if err != nil {
				return fmt.Errorf("catalog sync failed: %w", err)
			}

			cmd.Printf("Catalog synced to %s\n", catalogDir)
			cmd.Printf("  SHA256: %s\n", lock.SHA256)
			cmd.Printf("  Bytes:  %d\n", lock.Bytes)
			cmd.Printf("  TTL:    %d hours\n", lock.TTLHours)
			return nil
		},
	}
}

// resolveCatalogDir returns the directory where the catalog should be stored.
// If configPath is provided, the catalog is stored alongside that config file.
// Otherwise the default config directory is used.
func resolveCatalogDir(configPath string) string {
	if configPath != "" {
		return filepath.Join(filepath.Dir(configPath), "catalog")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "routatic-proxy", "catalog")
	}
	return filepath.Join(home, ".config", "routatic-proxy", "catalog")
}
