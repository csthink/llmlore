// Package cli wires up the llmlore command tree.
//
// T0 only provides the skeleton: the root command and four placeholder
// subcommands (update / serve / pull / stars). Each handler currently reports
// "not implemented yet" in English; later tasks replace these stubs with real
// implementations (update/serve/pull in T6, stars in T7).
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// newRootCmd builds the root command and attaches every subcommand.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "llmlore",
		Short: "Discover and curate LLM/agent learning repositories",
		Long: "llmlore collects, classifies, and tags GitHub repositories about\n" +
			"LLMs and agents, then renders a local HTML dashboard from an open dataset.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Running llmlore with no subcommand will eventually load + render +
		// serve (AC-1). Until then, show help.
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	root.AddCommand(
		newUpdateCmd(),
		newServeCmd(),
		newPullCmd(),
		newStarsCmd(),
	)

	return root
}

// notImplemented returns a RunE handler that reports a stub command.
func notImplemented(name string) func(*cobra.Command, []string) error {
	return func(*cobra.Command, []string) error {
		return fmt.Errorf("%s: not implemented yet", name)
	}
}

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Re-run the full pipeline and regenerate the dataset",
		RunE:  notImplemented("update"),
	}
}

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve the existing dashboard without refreshing data",
		RunE:  notImplemented("serve"),
	}
}

func newPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Download the pre-generated open dataset",
		RunE:  notImplemented("pull"),
	}
}

func newStarsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stars",
		Short: "Local-only my-stars mode (sync / organize / stale / search / view)",
		RunE:  notImplemented("stars"),
	}
}

// Execute runs the root command and exits with a non-zero status on error.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
