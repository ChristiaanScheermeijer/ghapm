package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Report available updates for pinned actions",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Checking for action updates (coming soon)...")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(checkCmd)
}
