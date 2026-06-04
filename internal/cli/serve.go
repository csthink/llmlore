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

// renderAndServe renders the dashboard view of ds and serves it until Ctrl+C.
// The catalog is trimmed to a readable view (store.Select) before rendering; the
// full dataset on disk is untouched. port==0 means use the configured port.
func renderAndServe(cmd *cobra.Command, cfg *config.Config, ds *model.Dataset, port int) error {
	now := time.Now()
	view := store.Select(ds, store.SelectOptions{
		PerTypeCap:  store.DefaultPerTypeCap,
		PerTopicCap: store.DefaultPerTopicCap,
	})
	html, err := render.Render(view, now)
	if err != nil {
		return err
	}

	// Persist the HTML so the on-disk copy stays openable after shutdown
	// (best-effort: a write failure must not stop serving).
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
