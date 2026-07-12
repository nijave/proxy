package main

import (
	"fmt"

	"github.com/routatic/proxy/internal/update"
	"github.com/spf13/cobra"
)

var updateChannelCmd = &cobra.Command{
	Use:   "update-channel [stable|beta]",
	Short: "Switch between stable and beta update channels",
	Long: `Choose which release channel to receive updates from.

Stable channel (default): Receives production releases only
Beta channel: Receives early access beta releases for testing

Examples:
  routatic-proxy update-channel          # Show current channel
  routatic-proxy update-channel stable   # Switch to stable releases
  routatic-proxy update-channel beta     # Switch to beta releases`,
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
			fmt.Println("  routatic-proxy update-channel stable")
			fmt.Println("  routatic-proxy update-channel beta")
			return nil
		}

		channel := args[0]
		if channel != "stable" && channel != "beta" {
			return fmt.Errorf("invalid channel %q: must be 'stable' or 'beta'", channel)
		}

		if err := update.SetChannel(channel); err != nil {
			return fmt.Errorf("failed to set update channel: %w", err)
		}

		if channel == "stable" {
			fmt.Println("Switched to stable (production) release channel.")
			fmt.Println("You will now receive stable releases when running 'routatic-proxy update'.")
			fmt.Println("To receive beta releases, run: routatic-proxy update-channel beta")
		} else {
			fmt.Println("Switched to beta release channel.")
			fmt.Println("You will now receive beta releases when running 'routatic-proxy update'.")
			fmt.Println("To receive stable releases, run: routatic-proxy update-channel stable")
		}

		return nil
	},
}

// TODO: wire updateChannelCmd into rootCmd when root command registration is centralized.
// func init() {
// 	rootCmd.AddCommand(updateChannelCmd)
// }
