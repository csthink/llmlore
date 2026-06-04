// Package cli wires up the llmlore command tree.
//
// update / serve / pull are implemented in their own files (T6: update.go,
// serve.go, pull.go); stars is still a placeholder reporting "not implemented
// yet" in English until T7. The no-arg `llmlore` default loads the dataset,
// renders the dashboard, and serves it (AC-1).
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/csthink/llmlore/internal/classifier"
	"github.com/csthink/llmlore/internal/collector"
	"github.com/csthink/llmlore/internal/config"
)

// newRootCmd builds the root command and attaches every subcommand. The no-arg
// run loads the dataset (on-disk, falling back to the embedded snapshot) and
// serves the dashboard (AC-1).
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "llmlore",
		Short: "Discover and curate LLM/agent learning repositories",
		Long: "llmlore collects, classifies, and tags GitHub repositories about\n" +
			"LLMs and agents, then renders a local HTML dashboard from an open dataset.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ds, err := loadDataset(cfg)
			if err != nil {
				return err
			}
			return renderAndServe(cmd, cfg, ds, 0)
		},
	}

	// Flag-parse failures are usage errors (exit code 2); inherited by children.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{err}
	})

	root.AddCommand(
		newUpdateCmd(),
		newServeCmd(),
		newPullCmd(),
		newStarsCmd(),
	)

	return root
}

func newStarsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stars",
		Short: "Local-only my-stars mode (sync / organize / stale / search / view)",
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("stars: not implemented yet")
		},
	}
}

// usageError marks an invalid-invocation error so Execute can exit with code 2
// (spec §1). It wraps the underlying cause for messaging.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// exitCode maps an error to the process exit code contract in spec §1:
// 0 success · 2 usage · 3 network/upstream · 4 missing config · 1 otherwise.
func exitCode(err error) int {
	var ue *usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ue):
		return 2
	case collector.IsUpstream(err) || classifier.IsUpstream(err) || collector.IsLayoutDrift(err):
		return 3
	case classifier.IsMissingConfig(err):
		return 4
	default:
		return 1
	}
}

// Execute runs the root command and exits with the spec §1 status code on error.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(exitCode(err))
	}
}
