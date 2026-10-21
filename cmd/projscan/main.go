package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/afshinnkarimi/projscan/internal/app"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	command := newRootCommand(stdout, stderr)
	command.SetArgs(args)
	if err := command.Execute(); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func newRootCommand(stdout, stderr io.Writer) *cobra.Command {
	config := viper.New()
	config.SetEnvPrefix("PROJSCAN")
	config.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	config.AutomaticEnv()

	command := &cobra.Command{
		Use:           "projscan",
		Short:         "Scan artifacts in a directory",
		Long:          "Scan Docker image archives, Helm charts, Java application archives, and release directories.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			directory, err := filepath.Abs(config.GetString("directory"))
			if err != nil {
				return fmt.Errorf("resolve scan directory: %w", err)
			}
			return app.Scan(directory, config.GetBool("latest"), cmd.OutOrStdout())
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := config.BindPFlag("directory", cmd.Flags().Lookup("directory")); err != nil {
				return fmt.Errorf("bind directory flag: %w", err)
			}
			if err := config.BindPFlag("latest", cmd.Flags().Lookup("latest")); err != nil {
				return fmt.Errorf("bind latest flag: %w", err)
			}
			if cmd.Flags().Changed("dir") {
				config.Set("directory", cmd.Flag("dir").Value.String())
			}
			return nil
		},
	}
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.Flags().StringP("directory", "d", ".", "directory to scan")
	command.Flags().String("dir", "", "alias for --directory")
	command.Flags().Bool("latest", false, "keep only latest Docker tags, Helm chart versions, and Java archive filename versions")
	return command
}
