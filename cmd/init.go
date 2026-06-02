package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Pin all workflow actions to specific commits",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Initializing ghapm (workflow pinning coming soon)...")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
