package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/csthink/llmlore/internal/config"
)

// newConfigCmd builds the `config` command group. Only `init` is exposed: build
// exactly what T9 needs and no more (PROPOSAL-004 D5a — avoid early abstraction).
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage the llmlore configuration file",
		Long: "Work with ~/.config/llmlore/config.toml (non-secret settings only).\n" +
			"Your API key is read from the environment and is never stored in the file.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newConfigInitCmd())
	return cmd
}

// newConfigInitCmd builds `config init`: scaffold the config template to
// ~/.config/llmlore/config.toml (honoring XDG_CONFIG_HOME). It writes no secret
// to disk and refuses to clobber an existing file unless --force (exit 1).
func newConfigInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a config.toml template to ~/.config/llmlore/ (no secrets are stored)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := config.DefaultConfigPath(os.Getenv)
			err := config.WriteTemplate(path, force)
			if errors.Is(err, config.ErrConfigExists) {
				return fmt.Errorf("%s already exists; re-run with --force to overwrite it", path)
			}
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Wrote config template to %s\n", path)
			fmt.Fprintln(out, "Next steps:")
			fmt.Fprintln(out, "  1. Edit the file and set `provider` to a real name to enable LLM features.")
			fmt.Fprintf(out, "  2. Export your API key (it is never stored in the file): export %s=...\n", config.EnvLLMAPIKey)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config file")
	return cmd
}
