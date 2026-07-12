package update

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/routatic/proxy/internal/config"
)

// Channel represents an update channel
type Channel string

const (
	ChannelStable Channel = "stable"
	ChannelBeta   Channel = "beta"
)

// DefaultChannel is the default update channel
const DefaultChannel = ChannelStable

// StateFileName is the name of the file storing update channel preference
const StateFileName = "update-channel"

// GetChannel returns the currently configured update channel
func GetChannel(cfg *config.Config) Channel {
	// Check config first
	if cfg.UpdateChannel != "" {
		return Channel(cfg.UpdateChannel)
	}
	// Fall back to default
	return DefaultChannel
}

// SetChannel saves the update channel preference
func SetChannel(channel Channel) error {
	configDir, err := getConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}

	stateFile := filepath.Join(configDir, StateFileName)
	if err := os.WriteFile(stateFile, []byte(channel), 0644); err != nil {
		return fmt.Errorf("failed to write update channel state: %w", err)
	}

	return nil
}

// LoadChannel loads the update channel from the state file
func LoadChannel() (Channel, error) {
	configDir, err := getConfigDir()
	if err != nil {
		return DefaultChannel, nil // Return default if we can't determine config dir
	}

	stateFile := filepath.Join(configDir, StateFileName)
	data, err := os.ReadFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultChannel, nil
		}
		return DefaultChannel, fmt.Errorf("failed to read update channel state: %w", err)
	}

	channel := Channel(string(data))
	if channel != ChannelStable && channel != ChannelBeta {
		return DefaultChannel, nil
	}

	return channel, nil
}

func getConfigDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".config", "routatic-proxy"), nil
}
