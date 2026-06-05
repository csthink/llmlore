package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/csthink/llmlore/internal/config"
	"github.com/csthink/llmlore/internal/stars"
)

// defaultCrossLimit caps how many "not yet starred" recommendations `stars
// cross` prints, keeping the output readable.
const defaultCrossLimit = 20

// myStarsPath resolves the local my-stars.json path (~/.local/share/llmlore/),
// which is deliberately outside any repository so personal data is never
// committed (AC-8).
func myStarsPath() string {
	return stars.DefaultDataPath(os.Getenv)
}

// newStarsCmd builds the local-only `stars` command group. Every subcommand
// works on ~/.local/share/llmlore/my-stars.json, physically separate from the
// shared catalog, and none performs any account write operation.
func newStarsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stars",
		Short: "Local-only my-stars mode: sync, organize, list stale, search, and cross with discover",
		Long: "Work with your own starred repositories entirely on this machine.\n" +
			"Data lives in ~/.local/share/llmlore/my-stars.json and is never committed.\n" +
			"No subcommand ever stars, unstars, or follows on your behalf.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newStarsSyncCmd(),
		newStarsOrganizeCmd(),
		newStarsStaleCmd(),
		newStarsSearchCmd(),
		newStarsCrossCmd(),
	)
	return cmd
}

// newStarsSyncCmd builds `stars sync`: fetch the starred list and refresh the
// local dataset. Public stars need only --user; private stars or a higher rate
// limit need LLMLORE_GITHUB_TOKEN. The fetched set replaces the local one,
// preserving classification and appending one star snapshot per repo.
func newStarsSyncCmd() *cobra.Command {
	var user string
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch your starred repositories into the local my-stars dataset",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if user == "" && cfg.GitHubToken == "" {
				return &usageError{fmt.Errorf("stars sync needs --user <login> for public stars, or set LLMLORE_GITHUB_TOKEN to sync your own (including private) stars")}
			}
			return runStarsSync(cmd, cfg, user)
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "GitHub login whose public stars to sync (omit to sync your own via token)")
	return cmd
}

func runStarsSync(cmd *cobra.Command, cfg *config.Config, user string) error {
	path := myStarsPath()
	existing, err := stars.Load(path)
	if err != nil {
		return err
	}

	if user != "" {
		logf(cmd, "Fetching stars for %s ...", user)
	} else {
		logf(cmd, "Fetching your stars ...")
	}
	fetched, err := stars.NewClient(cfg.GitHubToken).FetchStarred(cmd.Context(), user)
	if err != nil {
		return err
	}

	updated := stars.Sync(existing, fetched, user, time.Now())
	if err := stars.Save(path, updated); err != nil {
		return err
	}
	logf(cmd, "Synced %d starred repositories to %s", len(updated.Repos), path)
	logf(cmd, "Run `llmlore stars organize` to label them by type and topic.")
	return nil
}

// newStarsOrganizeCmd builds `stars organize`: classify the local stars into
// type x topic (and a one-line summary on the LLM path). Without a key, or with
// --no-llm, it degrades to heuristic classification.
func newStarsOrganizeCmd() *cobra.Command {
	var noLLM bool
	cmd := &cobra.Command{
		Use:   "organize",
		Short: "Classify your stars by type and topic, adding a summary when an LLM is configured",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return runStarsOrganize(cmd, cfg, noLLM)
		},
	}
	cmd.Flags().BoolVar(&noLLM, "no-llm", false, "force heuristic classification, never call the LLM")
	return cmd
}

func runStarsOrganize(cmd *cobra.Command, cfg *config.Config, noLLM bool) error {
	path := myStarsPath()
	ds, err := stars.Load(path)
	if err != nil {
		return err
	}
	if len(ds.Repos) == 0 {
		return fmt.Errorf("no starred repositories found at %s; run `llmlore stars sync` first", path)
	}

	logf(cmd, "Organizing %d starred repositories (%s classification)...", len(ds.Repos), cfg.ClassifierMode())
	organized, err := stars.Organize(cmd.Context(), ds, pickClassifier(cfg, noLLM))
	if err != nil {
		return err
	}
	if err := stars.Save(path, organized); err != nil {
		return err
	}
	logf(cmd, "Organized %d repositories in %s", len(organized.Repos), path)
	return nil
}

// newStarsStaleCmd builds `stars stale`: list archived or stale stars as
// unfollow candidates. It only prints them — it never unstars anything.
func newStarsStaleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stale",
		Short: "List archived or inactive stars as unfollow candidates (listing only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := myStarsPath()
			ds, err := stars.Load(path)
			if err != nil {
				return err
			}
			candidates := stars.Stale(ds)
			if len(candidates) == 0 {
				logf(cmd, "No archived or inactive stars found.")
				return nil
			}
			logf(cmd, "%d unfollow candidate(s) — review and unstar manually if you wish:", len(candidates))
			for _, r := range candidates {
				printStaleRepo(cmd, r)
			}
			return nil
		},
	}
}

func printStaleRepo(cmd *cobra.Command, r stars.Repo) {
	reason := "inactive"
	if r.Archived {
		reason = "archived"
	}
	if !r.PushedAt.IsZero() {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s (%s, last push %s) %s\n", r.ID, reason, r.PushedAt.Format("2006-01-02"), r.URL)
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  %s (%s) %s\n", r.ID, reason, r.URL)
}

// newStarsSearchCmd builds `stars search <query>`: filter the local stars by
// tag, language, topic, type, id, or summary.
func newStarsSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "search <query>",
		Short: "Search your stars by tag, language, or topic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := myStarsPath()
			ds, err := stars.Load(path)
			if err != nil {
				return err
			}
			results := stars.Search(ds, args[0])
			if len(results) == 0 {
				logf(cmd, "No stars match %q.", args[0])
				return nil
			}
			logf(cmd, "%d match(es) for %q:", len(results), args[0])
			for _, r := range results {
				printSearchRepo(cmd, r)
			}
			return nil
		},
	}
}

func printSearchRepo(cmd *cobra.Command, r stars.Repo) {
	lang := r.Language
	if lang == "" {
		lang = "n/a"
	}
	typ := r.Type
	if typ == "" {
		typ = "unclassified"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  %s [%s] %s ★%d — topics:%v lang:%s\n", r.ID, typ, r.URL, r.Stars, r.Topics, lang)
}

// writeLocalHTML writes a rendered dashboard that may embed personal data with
// owner-only permissions under the local share directory (never the repo). It
// backs the combined `llmlore view` HTML write (privacy red line).
func writeLocalHTML(path string, html []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, html, 0o600)
}

// newStarsCrossCmd builds `stars cross`: intersect the discover catalog with
// your stars to surface what you already follow (could be excluded) and the
// highest-star catalog repos you have not starred yet (recommendations).
func newStarsCrossCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "cross",
		Short: "Cross the discover catalog with your stars: recommend popular repos you have not starred",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			catalog, err := loadDataset(cfg)
			if err != nil {
				return err
			}
			mine, err := stars.Load(myStarsPath())
			if err != nil {
				return err
			}
			res := stars.Cross(catalog, mine, limit)
			logf(cmd, "You have already starred %d catalog repositories.", len(res.AlreadyStarred))
			if len(res.Recommended) == 0 {
				logf(cmd, "No further recommendations: you have starred everything in the catalog.")
				return nil
			}
			logf(cmd, "Top %d popular repositories you have not starred yet:", len(res.Recommended))
			for _, r := range res.Recommended {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s ★%d [%s] %s\n", r.ID, r.Stars, r.Type, r.URL)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", defaultCrossLimit, "maximum number of recommendations to print")
	return cmd
}
