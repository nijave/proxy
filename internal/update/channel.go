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

// ConfigFileName is the name of the channel config file
const ConfigFileName = "update-channel.json"

// GetConfigPath returns the path to the channel config file
func GetConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "routatic-proxy", ConfigFileName), nil
}

// GetChannel reads the user's preferred update channel
func GetChannel() (string, error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return DefaultChannel, nil // Fall back to default if we can't determine path
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultChannel, nil // No preference set, use default
		}
		return "", err
	}

	var config ChannelConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return DefaultChannel, nil // Corrupted config, use default
	}

	if config.Channel != "stable" && config.Channel != "beta" {
		return DefaultChannel, nil // Invalid channel, use default
	}

	return config.Channel, nil
}

// SetChannel saves the user's preferred update channel
func SetChannel(channel string) error {
	if channel != "stable" && channel != "beta" {
		return nil // Silently ignore invalid channels
	}

	configPath, err := GetConfigPath()
	if err != nil {
		return err
	}

	// Ensure directory exists
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	config := ChannelConfig{Channel: channel}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}
