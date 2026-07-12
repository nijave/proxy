package main

import (
	"fmt"

	"github.com/routatic/proxy/internal/update"
	"github.com/spf13/cobra"
)

// updateChannelCmd allows users to switch between stable and beta update channels
var updateChannelCmd = &cobra.Command{
	Use:   "update-channel [stable|beta]",
	Short: "Switch between stable and beta update channels",
	Long: `Switch between stable (production) and beta (early access) update channels.

When set to 'beta', the 'update' command will fetch pre-release versions
from GitHub instead of stable releases.

Examples:
  routatic-proxy update-channel beta     # Switch to beta channel
  routatic-proxy update-channel stable   # Switch back to stable (default)
  routatic-proxy update-channel          # Show current channel`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			// Show current channel
			channel, err := update.GetChannel()
			if err != nil {
				return fmt.Errorf("failed to read channel: %w", err)
			}
			fmt.Printf("Current update channel: %s\n", channel)
			if channel == "stable" {
				fmt.Println("To receive beta releases, run: routatic-proxy update-channel beta")
			} else {
				fmt.Println("To receive stable (production) releases, run: routatic-proxy update-channel stable")
			}
			return nil
		}

		channel := args[0]
		switch channel {
		case "stable", "beta":
			if err := update.SetChannel(update.Channel(channel)); err != nil {
				return fmt.Errorf("failed to set channel: %w", err)
			}
			fmt.Printf("Update channel set to: %s\n", channel)
			if channel == "stable" {
				fmt.Println("You will now receive stable (production) releases when running 'routatic-proxy update'.")
				fmt.Println("To receive beta releases, run: routatic-proxy update-channel beta")
			} else {
				fmt.Println("You will now receive beta releases when running 'routatic-proxy update'.")
				fmt.Println("To receive stable (production) releases, run: routatic-proxy update-channel stable")
			}
			return nil
		default:
			return fmt.Errorf("invalid channel %q: must be 'stable' or 'beta'", channel)
		}
	},
}

// TODO: wire updateChannelCmd into rootCmd when root command registration is centralized.
// func init() {
// 	rootCmd.AddCommand(updateChannelCmd)
// }
