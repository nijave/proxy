package update

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Channel represents the update channel preference
type Channel string

const (
	ChannelStable Channel = "stable"
	ChannelBeta   Channel = "beta"
)

// ChannelConfig represents the update channel configuration
type ChannelConfig struct {
	Channel Channel `json:"channel"`
}

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

	return filepath.Join(configDir, "update-channel.json"), nil
}

// GetChannel returns the user's preferred update channel
func GetChannel() (Channel, error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return ChannelStable, err
	}

	// If config file doesn't exist, default to stable
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return ChannelStable, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return ChannelStable, fmt.Errorf("failed to read channel config: %w", err)
	}

	var config ChannelConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return ChannelStable, fmt.Errorf("failed to parse channel config: %w", err)
	}

	// Validate channel
	if config.Channel != ChannelStable && config.Channel != ChannelBeta {
		return ChannelStable, nil
	}

	return config.Channel, nil
}

// SetChannel saves the user's preferred update channel
func SetChannel(channel Channel) error {
	if channel != ChannelStable && channel != ChannelBeta {
		return fmt.Errorf("invalid channel: %s (must be 'stable' or 'beta')", channel)
	}

	configPath, err := GetConfigPath()
	if err != nil {
		return err
	}

	config := ChannelConfig{Channel: channel}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal channel config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write channel config: %w", err)
	}

	return nil
}
