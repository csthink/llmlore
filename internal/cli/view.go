package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"github.com/csthink/llmlore/internal/config"
	"github.com/csthink/llmlore/internal/model"
	"github.com/csthink/llmlore/internal/render"
	"github.com/csthink/llmlore/internal/server"
	"github.com/csthink/llmlore/internal/stars"
	"github.com/csthink/llmlore/internal/store"
)

// newViewCmd builds `llmlore view`: open the single combined dashboard with
// Catalog / My stars / Cross tabs (PROPOSAL-005 / T10). It is also the path the
// no-arg `llmlore` default takes (AC-1). It never refreshes data — run
// `llmlore update`/`pull` for that.
func newViewCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Open the combined dashboard (Catalog / My stars / Cross) locally",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return runCombinedView(cmd, cfg, port)
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "local server port (default: $LLMLORE_PORT or 7777)")
	return cmd
}

// runCombinedView loads the open catalog plus the local my-stars dataset, builds
// the three-tab combined view, and serves it until Ctrl+C.
//
// Privacy red line: the combined HTML embeds personal star data, so the rendered
// file IS personal data. It is written ONLY under ~/.local/share/llmlore/ (0600,
// beside my-stars.json) — never out/, never the repo. With no local my-stars
// data (never synced) the My stars + Cross tabs degrade to an empty hint and the
// Catalog tab still works (revised AC-1) — never an error, never a panic.
func runCombinedView(cmd *cobra.Command, cfg *config.Config, port int) error {
	nudgeIfPlaceholder(cmd, cfg)

	catalog, err := loadDataset(cfg)
	if err != nil {
		return err
	}

	mine, err := stars.Load(myStarsPath())
	if err != nil {
		return err
	}
	hasStars := len(mine.Repos) > 0

	// The Catalog tab is the discover view, so it honors LLMLORE_EXCLUDE_STARRED
	// (spec §2 / design §6): when set, repos the user has already starred are
	// dropped from this tab. This is safe here because the combined HTML is
	// written local-only (0600) — never out/ — so personalizing it leaks
	// nothing into a shareable artifact (privacy red line / AC-11). The Cross tab
	// below deliberately uses the FULL catalog so its already/recommended split
	// stays complete regardless of this filter.
	catalogForView, err := applyExcludeStarred(cfg, catalog)
	if err != nil {
		return err
	}

	// Catalog tab keeps the readable caps; the My-stars tab is uncapped (D3) so
	// the full personal collection shows. Cross is computed unbounded (limit 0).
	catalogView := store.Select(catalogForView, defaultSelectOptions())
	var myView *model.Dataset
	var cross stars.CrossResult
	if hasStars {
		myView = store.Select(mine.AsCatalog(), store.SelectOptions{})
		cross = stars.Cross(catalog, mine, 0)
	}

	html, err := render.RenderCombined(catalogView, myView, cross.AlreadyStarred, cross.Recommended, hasStars, time.Now())
	if err != nil {
		return err
	}

	// Persist local-only (never out/ / the repo) so the privacy invariant holds
	// even when the file embeds personal stars. Best-effort: serving from memory
	// does not depend on the file.
	htmlPath := stars.DefaultHTMLPath(os.Getenv)
	if err := writeLocalHTML(htmlPath, html); err != nil {
		logf(cmd, "Could not write %s: %v", htmlPath, err)
	}

	if port == 0 {
		port = cfg.Port
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return server.Serve(ctx, fmt.Sprintf(":%d", port), html, server.OpenBrowser, func(f string, a ...any) { logf(cmd, f, a...) })
}
