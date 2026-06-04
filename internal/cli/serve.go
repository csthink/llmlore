package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/csthink/llmlore/internal/config"
	"github.com/csthink/llmlore/internal/data"
	"github.com/csthink/llmlore/internal/model"
	"github.com/csthink/llmlore/internal/render"
	"github.com/csthink/llmlore/internal/server"
	"github.com/csthink/llmlore/internal/stars"
	"github.com/csthink/llmlore/internal/store"
)

// dashboardOutPath is where the rendered HTML is written so it persists on disk
// after the server stops (AC-7: the on-disk HTML stays double-clickable). It is
// gitignored (/out/) — the source of truth is data/repos.json, the HTML is
// regenerable.
const dashboardOutPath = "out/dashboard.html"

// newServeCmd builds `llmlore serve`: render the existing dataset and serve it,
// without refreshing data (spec §1). It is also the path the no-arg `llmlore`
// default takes (AC-1).
func newServeCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the existing dashboard without refreshing data",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ds, err := loadDataset(cfg)
			if err != nil {
				return err
			}
			return renderAndServe(cmd, cfg, ds, port)
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "local server port (default: $LLMLORE_PORT or 7777)")
	return cmd
}

// dataPath returns the on-disk dataset path for a config's data dir.
func dataPath(cfg *config.Config) string {
	return filepath.Join(cfg.DataDir, "repos.json")
}

// loadDataset loads the on-disk dataset, falling back to the embedded snapshot
// when no local data exists yet (a fresh install that has not pulled/updated —
// AC-1). A present-but-empty on-disk dataset also falls back, so the dashboard
// is never blank when a usable snapshot ships in the binary.
func loadDataset(cfg *config.Config) (*model.Dataset, error) {
	ds, err := store.Load(dataPath(cfg))
	if err != nil {
		return nil, err
	}
	if len(ds.Repos) > 0 {
		return ds, nil
	}
	snapshot, err := data.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("no local dataset and embedded fallback is unreadable: %w", err)
	}
	return snapshot, nil
}

// applyExcludeStarred drops the user's already-starred repos from a discover
// VIEW when LLMLORE_EXCLUDE_STARRED is set (spec §2 / design §6). It reads the
// starred id set read-only from ~/.local/share/llmlore/my-stars.json and filters
// the in-memory dataset only — data/repos.json (the source of truth / open
// dataset) is never personalized, and no personal data is ever written into it.
// A machine with no my-stars.json yields an empty set, so discover is unaffected.
func applyExcludeStarred(cfg *config.Config, ds *model.Dataset) (*model.Dataset, error) {
	if !cfg.ExcludeStarred {
		return ds, nil
	}
	ids, err := stars.LoadStarredIDs(stars.DefaultDataPath(os.Getenv))
	if err != nil {
		return nil, err
	}
	return stars.ExcludeFrom(ds, ids), nil
}

// buildDashboard trims ds to a readable view (store.Select) and renders the
// self-contained HTML. The full dataset on disk is untouched — Select is a
// view-only operation. It is shared by `serve`/the no-arg default (which then
// serve the HTML) and `update` (which writes it to disk without serving).
func buildDashboard(ds *model.Dataset, now time.Time, sel store.SelectOptions) ([]byte, error) {
	return render.Render(store.Select(ds, sel), now)
}

// defaultSelectOptions is the readable-view trim used when no per-run caps are
// given (serve / the no-arg default).
func defaultSelectOptions() store.SelectOptions {
	return store.SelectOptions{
		PerTypeCap:  store.DefaultPerTypeCap,
		PerTopicCap: store.DefaultPerTopicCap,
	}
}

// renderAndServe renders the dashboard view of ds, writes it to disk, and serves
// it until Ctrl+C. The on-disk write is best-effort (a failure must not stop
// serving). port==0 means use the configured port.
func renderAndServe(cmd *cobra.Command, cfg *config.Config, ds *model.Dataset, port int) error {
	ds, err := applyExcludeStarred(cfg, ds)
	if err != nil {
		return err
	}
	html, err := buildDashboard(ds, time.Now(), defaultSelectOptions())
	if err != nil {
		return err
	}

	// Persist the HTML so the on-disk copy stays openable after shutdown
	// (best-effort here: serving from memory does not depend on the file).
	if err := writeDashboard(html); err != nil {
		logf(cmd, "Could not write %s: %v", dashboardOutPath, err)
	}

	if port == 0 {
		port = cfg.Port
	}
	addr := fmt.Sprintf(":%d", port)

	// Ctrl+C cancels the context, which Serve turns into a graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return server.Serve(ctx, addr, html, server.OpenBrowser, func(f string, a ...any) { logf(cmd, f, a...) })
}

// writeDashboard writes the rendered HTML to dashboardOutPath, creating the
// parent directory as needed.
func writeDashboard(html []byte) error {
	if err := os.MkdirAll(filepath.Dir(dashboardOutPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dashboardOutPath, html, 0o644)
}

// logf prints a status line to the command's stderr.
func logf(cmd *cobra.Command, format string, a ...any) {
	fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", a...)
}
