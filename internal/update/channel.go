package update

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ChannelConfig stores the user's update channel preference
type ChannelConfig struct {
	Channel string `json:"channel"` // "stable" or "beta"
}

// DefaultChannel is the default update channel
const DefaultChannel = "stable"

// GetChannelConfigPath returns the path to the channel config file
func GetChannelConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	configDir := filepath.Join(homeDir, ".config", "routatic-proxy")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(configDir, "update-channel.json"), nil
}

// GetChannel reads the user's preferred update channel
func GetChannel() (string, error) {
	configPath, err := GetChannelConfigPath()
	if err != nil {
		return DefaultChannel, nil // Return default on error
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultChannel, nil // Default to stable
		}
		return DefaultChannel, nil
	}

	var config ChannelConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return DefaultChannel, nil
	}

	if config.Channel != "stable" && config.Channel != "beta" {
		return DefaultChannel, nil
	}

	return config.Channel, nil
}

// SetChannel saves the user's preferred update channel
func SetChannel(channel string) error {
	if channel != "stable" && channel != "beta" {
		return os.ErrInvalid
	}

	configPath, err := GetChannelConfigPath()
	if err != nil {
		return err
	}

	config := ChannelConfig{Channel: channel}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}
