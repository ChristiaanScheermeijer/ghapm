package cmd

import (
	"fmt"
	"os"

	githubclient "github.com/christiaanscheermeijer/ghapm/internal/githubclient"
	"github.com/spf13/cobra"
)

var (
	rootCmd = &cobra.Command{
		Use:   "ghapm",
		Short: "GitHub Actions Package Manager",
		Long:  "ghapm locks GitHub Actions workflows to specific commits and helps you track safe upgrades.",
	}

	version = "0.0.1"
)

// Execute runs the root command.
func Execute() {
	rootCmd.Version = version
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.SetVersionTemplate("ghapm version {{.Version}}\n")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")

	cobra.OnInitialize(func() {
		if verbose {
			githubclient.SetLogger(debugf)
		} else {
			githubclient.SetLogger(nil)
		}
	})
}
