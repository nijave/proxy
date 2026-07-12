package update

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Channel represents the update channel preference
type Channel string

const (
	ChannelStable Channel = "stable"
	ChannelBeta   Channel = "beta"
)

// ChannelConfig stores the user's update channel preference
type ChannelConfig struct {
	Channel Channel `json:"channel"`
}

// Default channel is stable
const DefaultChannel = ChannelStable

// getChannelFilePath returns the path to the channel config file
func getChannelFilePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "routatic-proxy", "update-channel.json"), nil
}

// GetChannel reads the user's preferred update channel
// Returns DefaultChannel if no preference is set or file doesn't exist
func GetChannel() (Channel, error) {
	path, err := getChannelFilePath()
	if err != nil {
		return DefaultChannel, nil // Fall back to default on error
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultChannel, nil // No preference set yet
		}
		return DefaultChannel, nil // Fall back to default on read error
	}

	var config ChannelConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return DefaultChannel, nil // Fall back to default on parse error
	}

	// Validate channel value
	if config.Channel != ChannelStable && config.Channel != ChannelBeta {
		return DefaultChannel, nil
	}

	return config.Channel, nil
}

// SetChannel saves the user's preferred update channel
func SetChannel(channel Channel) error {
	// Validate channel value
	if channel != ChannelStable && channel != ChannelBeta {
		return os.ErrInvalid
	}

	path, err := getChannelFilePath()
	if err != nil {
		return err
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	config := ChannelConfig{Channel: channel}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// IsBeta returns true if the user has selected the beta channel
func IsBeta() bool {
	channel, err := GetChannel()
	if err != nil {
		return false
	}
	return channel == ChannelBeta
}
