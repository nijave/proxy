package main

import (
	"fmt"

	"github.com/routatic/proxy/internal/update"
	"github.com/spf13/cobra"
)

var updateChannelCmd = &cobra.Command{
	Use:   "update-channel [stable|beta]",
	Short: "Switch between stable and beta update channels",
	Long: `Switch between stable (production) and beta (early access) update channels.

By default, updates come from the stable channel. Use this command to opt into
beta releases for early access to new features.

Examples:
  routatic-proxy update-channel beta    # Switch to beta channel
  routatic-proxy update-channel stable  # Switch back to stable channel
  routatic-proxy update-channel         # Show current channel`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			// Show current channel
			channel, err := update.GetChannel()
			if err != nil {
				return fmt.Errorf("failed to get update channel: %w", err)
			}
			fmt.Printf("Current update channel: %s\n", channel)
			fmt.Println("\nTo switch channels:")
			fmt.Println("  routatic-proxy update-channel beta    # Early access releases")
			fmt.Println("  routatic-proxy update-channel stable  # Production releases (default)")
			return nil
		}

		channel := update.Channel(args[0])
		if err := update.SetChannel(channel); err != nil {
			return fmt.Errorf("failed to set update channel: %w", err)
		}

		if channel == update.ChannelBeta {
			fmt.Println("Switched to beta update channel.")
			fmt.Println("You will now receive beta (early access) releases when running 'routatic-proxy update'.")
			fmt.Println("To switch back to stable releases, run: routatic-proxy update-channel stable")
		} else {
			fmt.Println("Switched to stable update channel.")
			fmt.Println("You will now receive stable (production) releases when running 'routatic-proxy update'.")
			fmt.Println("To receive beta releases, run: routatic-proxy update-channel beta")
		}

		return nil
	},
}

// TODO: wire updateChannelCmd into rootCmd when root command registration is centralized.
// func init() {
// 	rootCmd.AddCommand(updateChannelCmd)
// }
