package cmd

import (
	"errors"
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

	version = "dev"
)

// Execute runs the root command.
func Execute() {
	rootCmd.Version = version
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true

	if err := rootCmd.Execute(); err != nil {
		exitCode := 1
		var coded interface{ ExitCode() int }
		if errors.As(err, &coded) {
			exitCode = coded.ExitCode()
		}

		if err.Error() != "" {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}

		os.Exit(exitCode)
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
