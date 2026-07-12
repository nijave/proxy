package cmd

import (
	"fmt"

	"github.com/routatic/proxy/internal/update"
	"github.com/spf13/cobra"
)

var updateChannelCmd = &cobra.Command{
	Use:   "update-channel [stable|beta]",
	Short: "Get or set the update channel (stable or beta)",
	Long: `Get or set the update channel preference for self-updates.

Channels:
  stable  - Production releases only (recommended for most users)
  beta    - Beta/nightly releases for early access to new features

Examples:
  # Show current channel
  routatic-proxy update-channel

  # Switch to beta channel
  routatic-proxy update-channel beta

  # Switch back to stable channel
  routatic-proxy update-channel stable`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			// Get current channel
			channel, err := update.LoadChannel()
			if err != nil {
				return fmt.Errorf("failed to load update channel: %w", err)
			}
			fmt.Printf("Current update channel: %s\n", channel)
			if channel == update.ChannelBeta {
				fmt.Println("\nYou are receiving beta releases.")
				fmt.Println("To switch back to stable releases, run: routatic-proxy update-channel stable")
			} else {
				fmt.Println("\nYou are receiving stable (production) releases.")
				fmt.Println("To receive beta releases, run: routatic-proxy update-channel beta")
			}
			return nil
		}

		// Set channel
		channelStr := args[0]
		var channel update.Channel
		switch channelStr {
		case "stable":
			channel = update.ChannelStable
		case "beta":
			channel = update.ChannelBeta
		default:
			return fmt.Errorf("invalid channel %q: must be 'stable' or 'beta'", channelStr)
		}

		if err := update.SetChannel(channel); err != nil {
			return fmt.Errorf("failed to set update channel: %w", err)
		}

		fmt.Printf("Update channel set to: %s\n", channel)
		if channel == update.ChannelBeta {
			fmt.Println("\nYou will now receive beta releases when running 'routatic-proxy update'.")
			fmt.Println("Beta releases may contain new features but could also have bugs.")
			fmt.Println("To switch back to stable releases, run: routatic-proxy update-channel stable")
		} else {
			fmt.Println("\nYou will now receive stable (production) releases when running 'routatic-proxy update'.")
			fmt.Println("To receive beta releases, run: routatic-proxy update-channel beta")
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(updateChannelCmd)
}
