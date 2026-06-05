package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/csthink/llmlore/internal/config"
	"github.com/csthink/llmlore/internal/data"
	"github.com/csthink/llmlore/internal/model"
	"github.com/csthink/llmlore/internal/render"
	"github.com/csthink/llmlore/internal/stars"
	"github.com/csthink/llmlore/internal/store"
)

// dashboardOutPath is where the rendered catalog HTML is written so it persists
// on disk after the server stops (AC-7: the on-disk HTML stays double-clickable).
// It is gitignored (/out/) — the source of truth is data/repos.json, the HTML is
// regenerable. Only the catalog-only `update` HTML lands here; the combined
// dashboard (which embeds personal data) is written to the local share dir
// instead (privacy red line — see view.go / writeLocalHTML).
const dashboardOutPath = "out/dashboard.html"

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

// nudgeIfPlaceholder prints a one-line, non-fatal English notice when the loaded
// config still holds the `config init` placeholder (provider == sentinel). It
// fires on the dashboard-open path (no-arg `llmlore` / `view`) and never blocks:
// the caller keeps serving in heuristic mode (PROPOSAL-004 / AC-10).
func nudgeIfPlaceholder(cmd *cobra.Command, cfg *config.Config) {
	if !cfg.LLM.Placeholder {
		return
	}
	logf(cmd, "Note: %s still has placeholder values — edit it and export %s to enable LLM features. Running in heuristic mode.",
		config.DefaultConfigPath(os.Getenv), config.EnvLLMAPIKey)
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
