package update

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ChannelConfig stores the user's preferred update channel
type ChannelConfig struct {
	Channel string `json:"channel"` // "stable" or "beta"
}

// DefaultChannel is the default update channel
const DefaultChannel = "stable"

// ConfigFileName is the name of the channel config file
const ConfigFileName = "update-channel.json"

// GetConfigPath returns the path to the channel config file
func GetConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	configDir := filepath.Join(homeDir, ".config", "routatic-proxy")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create config directory: %w", err)
	}

	return filepath.Join(configDir, ConfigFileName), nil
}

// GetChannel reads the user's preferred channel from config
// Returns "stable" by default if no config exists
func GetChannel() (string, error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return DefaultChannel, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultChannel, nil
		}
		return DefaultChannel, fmt.Errorf("failed to read channel config: %w", err)
	}

	var cfg ChannelConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultChannel, fmt.Errorf("failed to parse channel config: %w", err)
	}

	if cfg.Channel == "" {
		return DefaultChannel, nil
	}

	return cfg.Channel, nil
}

// SetChannel saves the user's preferred channel to config
func SetChannel(channel string) error {
	if channel != "stable" && channel != "beta" {
		return fmt.Errorf("invalid channel %q: must be 'stable' or 'beta'", channel)
	}

	configPath, err := GetConfigPath()
	if err != nil {
		return err
	}

	cfg := ChannelConfig{Channel: channel}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal channel config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write channel config: %w", err)
	}

	return nil
}
