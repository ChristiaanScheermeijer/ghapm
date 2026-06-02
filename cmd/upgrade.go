package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	upgradeAllowMajor bool

	upgradeCmd = &cobra.Command{
		Use:   "upgrade",
		Short: "Move pinned actions forward to the latest safe release",
		RunE: func(cmd *cobra.Command, args []string) error {
			if upgradeAllowMajor {
				fmt.Println("Upgrading actions (including majors) (coming soon)...")
				return nil
			}

			fmt.Println("Upgrading actions within current major (coming soon)...")
			return nil
		},
	}
)

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeAllowMajor, "major", false, "Allow upgrades to the next major version")
	rootCmd.AddCommand(upgradeCmd)
}
