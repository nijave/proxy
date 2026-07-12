package main

import (
	"fmt"

	"github.com/routatic/proxy/internal/update"
	"github.com/spf13/cobra"
)

var updateChannelCmd = &cobra.Command{
	Use:   "update-channel [stable|beta]",
	Short: "Get or set the update channel preference",
	Long: `View or change your preferred update channel.

Channels:
  stable  - Production releases only (default)
  beta    - Beta releases from main branch

Examples:
  # Show current channel
  routatic-proxy update-channel

  # Switch to beta channel
  routatic-proxy update-channel beta

  # Switch back to stable
  routatic-proxy update-channel stable`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			// Show current channel
			channel, err := update.GetChannel()
			if err != nil {
				return fmt.Errorf("failed to get channel: %w", err)
			}
			fmt.Printf("Current update channel: %s\n", channel)
			if channel == "stable" {
				fmt.Println("You will receive stable (production) releases when running 'routatic-proxy update'.")
				fmt.Println("To receive beta releases, run: routatic-proxy update-channel beta")
			} else {
				fmt.Println("You will receive beta releases when running 'routatic-proxy update'.")
				fmt.Println("To receive stable (production) releases, run: routatic-proxy update-channel stable")
			}
			return nil
		}

		channel := args[0]
		if channel != "stable" && channel != "beta" {
			return fmt.Errorf("invalid channel %q: must be 'stable' or 'beta'", channel)
		}

		if err := update.SetChannel(channel); err != nil {
			return fmt.Errorf("failed to set channel: %w", err)
		}

		if channel == "stable" {
			fmt.Println("Update channel set to: stable")
			fmt.Println("You will now receive stable (production) releases when running 'routatic-proxy update'.")
			fmt.Println("To receive beta releases, run: routatic-proxy update-channel beta")
		} else {
			fmt.Println("Update channel set to: beta")
			fmt.Println("You will now receive beta releases when running 'routatic-proxy update'.")
			fmt.Println("To receive stable (production) releases, run: routatic-proxy update-channel stable")
		}

		return nil
	},
}

// TODO: wire updateChannelCmd into rootCmd when root command registration is centralized.
// func init() {
// 	rootCmd.AddCommand(updateChannelCmd)
// }
