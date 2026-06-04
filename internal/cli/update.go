package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/csthink/llmlore/internal/classifier"
	"github.com/csthink/llmlore/internal/collector"
	"github.com/csthink/llmlore/internal/config"
	"github.com/csthink/llmlore/internal/enricher"
	"github.com/csthink/llmlore/internal/model"
	"github.com/csthink/llmlore/internal/store"
)

// Discovery modes (spec §1 `--mode`).
const (
	modeHistorical = "historical"
	modeTrending   = "trending"
	modeBoth       = "both"
)

// defaultSearchKeyword seeds the historical search when no --topic narrows it,
// so the query is never empty. Topic filters replace it.
const defaultSearchKeyword = "llm"

// defaultSearchLimit caps how many candidates the search collector fetches per
// run. The readable-view caps (store.Select) shrink this further at render time.
const defaultSearchLimit = 100

// searcher / trender are the slices of the collector clients the pipeline needs,
// narrowed to interfaces so tests can inject fakes.
type searcher interface {
	Search(ctx context.Context, opts collector.SearchOptions) ([]collector.Candidate, error)
}

type trender interface {
	Trending(ctx context.Context, opts collector.TrendingOptions) ([]collector.Candidate, error)
}

// updateOptions holds the resolved flags for one update run. The flag set
// mirrors spec §1 exactly: --mode / --topic / --min-stars / --limit / --no-llm.
type updateOptions struct {
	mode     string
	topics   []string
	minStars int
	limit    int // per-topic/type visible-card cap (0 = store defaults)
	noLLM    bool
}

// pipelineDeps are the collaborators runPipeline orchestrates. search/trending
// may be nil when the mode does not use them.
type pipelineDeps struct {
	search   searcher
	trending trender
	classify classifier.Classifier
	now      time.Time
}

// newUpdateCmd builds `llmlore update`: run the full pipeline, write the
// dataset, and render the dashboard to disk (spec §1). It does NOT serve —
// viewing is the job of `llmlore` (no args) and `serve`.
func newUpdateCmd() *cobra.Command {
	opts := updateOptions{mode: modeHistorical}
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Re-run the full pipeline, regenerate the dataset, and render the dashboard",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd, opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.mode, "mode", modeHistorical, "discovery mode: historical | trending | both")
	f.StringSliceVar(&opts.topics, "topic", nil, "restrict to these topics (comma-separated)")
	f.IntVar(&opts.minStars, "min-stars", 0, "lower bound on stars")
	f.IntVar(&opts.limit, "limit", 0, "per-topic/type visible-card cap (default: built-in caps)")
	f.BoolVar(&opts.noLLM, "no-llm", false, "force heuristic classification, never call the LLM")
	return cmd
}

// runUpdate wires real collectors/classifier to the pipeline, persists the
// result, and renders the dashboard to disk. Serving is intentionally separate
// (spec §1): run `llmlore` or `serve` to view, or open out/dashboard.html.
func runUpdate(cmd *cobra.Command, opts updateOptions) error {
	if err := validateMode(opts.mode); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	deps := pipelineDeps{classify: pickClassifier(cfg, opts.noLLM), now: time.Now()}
	if opts.mode == modeHistorical || opts.mode == modeBoth {
		deps.search = collector.NewSearchClient(cfg.GitHubToken)
	}
	if opts.mode == modeTrending || opts.mode == modeBoth {
		deps.trending = collector.NewTrendingClient()
	}

	existing, err := store.Load(dataPath(cfg))
	if err != nil {
		return err
	}

	logf(cmd, "Running %s discovery (%s classification)...", opts.mode, cfg.ClassifierMode())
	updated, err := runPipeline(cmd.Context(), existing, deps, opts)
	if err != nil {
		return err
	}

	if err := store.Save(dataPath(cfg), updated); err != nil {
		return err
	}
	logf(cmd, "Wrote %d repositories to %s", len(updated.Repos), dataPath(cfg))

	// Render the dashboard to disk. Unlike the serve path's best-effort write,
	// here the rendered HTML is update's deliverable, so a write failure is fatal.
	// LLMLORE_EXCLUDE_STARRED filters the VIEW only: the full catalog was already
	// persisted above, so the open dataset stays complete while the dashboard
	// hides repos the user has already starred (personal data read-only).
	view, err := applyExcludeStarred(cfg, updated)
	if err != nil {
		return err
	}
	html, err := buildDashboard(view, time.Now(), selectOptionsFor(opts))
	if err != nil {
		return err
	}
	if err := writeDashboard(html); err != nil {
		return fmt.Errorf("write dashboard: %w", err)
	}
	logf(cmd, "Rendered dashboard to %s", dashboardOutPath)
	return nil
}

// selectOptionsFor maps the run's --min-stars / --limit flags onto the
// render-time view trim, so those flags actually shape the rendered dashboard.
// A zero --limit falls back to the built-in per-category caps.
func selectOptionsFor(opts updateOptions) store.SelectOptions {
	sel := store.SelectOptions{
		MinStars:    opts.minStars,
		PerTypeCap:  store.DefaultPerTypeCap,
		PerTopicCap: store.DefaultPerTopicCap,
	}
	if opts.limit > 0 {
		sel.PerTypeCap = opts.limit
		sel.PerTopicCap = opts.limit
	}
	return sel
}

// runPipeline is the pure orchestration core: collect candidates for the mode,
// dedupe, refresh repos already in the catalog and classify new ones, then merge
// and stamp meta. It performs no I/O of its own beyond the injected collaborators
// and never serves — so it is unit-testable with fakes. The returned dataset is
// the full merged catalog (history preserved); view trimming happens at render.
func runPipeline(ctx context.Context, existing *model.Dataset, deps pipelineDeps, opts updateOptions) (*model.Dataset, error) {
	candidates, err := collectCandidates(ctx, deps, opts)
	if err != nil {
		return nil, err
	}
	candidates = store.DedupeCandidates(candidates)

	index := store.Index(existing)
	staleAfter := enricher.DefaultStaleThreshold
	var incoming []model.Repo
	for _, c := range candidates {
		if prev, ok := index[c.ID]; ok {
			// Already cataloged: refresh activity + append a snapshot, never reclassify.
			incoming = append(incoming, enricher.Refresh(prev, c, deps.now, staleAfter))
			continue
		}
		dec, err := deps.classify.Classify(ctx, c)
		if err != nil {
			return nil, err
		}
		if !dec.Keep {
			continue
		}
		incoming = append(incoming, enricher.NewRepo(c, dec, deps.now, staleAfter))
	}

	merged := store.Merge(existing, incoming)
	merged.Meta.SchemaVersion = model.CurrentSchemaVersion
	merged.Meta.GeneratedAt = deps.now
	merged.Meta.Mode = opts.mode
	merged.Meta.Count = len(merged.Repos)
	return merged, nil
}

// collectCandidates gathers candidates from the sources the mode enables.
func collectCandidates(ctx context.Context, deps pipelineDeps, opts updateOptions) ([]collector.Candidate, error) {
	var out []collector.Candidate
	if deps.search != nil {
		searchOpts := collector.SearchOptions{
			Topics:   opts.topics,
			MinStars: opts.minStars,
			Limit:    defaultSearchLimit,
		}
		if len(opts.topics) == 0 {
			searchOpts.Keywords = []string{defaultSearchKeyword}
		}
		found, err := deps.search.Search(ctx, searchOpts)
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	if deps.trending != nil {
		found, err := deps.trending.Trending(ctx, collector.TrendingOptions{Since: "daily"})
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	return out, nil
}

// pickClassifier honours --no-llm: forced heuristic regardless of a configured
// key, otherwise the config-implied classifier (LLM when a provider+key exist).
func pickClassifier(cfg *config.Config, noLLM bool) classifier.Classifier {
	if noLLM {
		return classifier.NewHeuristic()
	}
	return classifier.New(cfg)
}

// validateMode rejects an unknown --mode with a usage error (exit code 2).
func validateMode(mode string) error {
	switch mode {
	case modeHistorical, modeTrending, modeBoth:
		return nil
	default:
		return &usageError{fmt.Errorf("invalid --mode %q: want historical, trending, or both", mode)}
	}
}
