// Package render turns a model.Dataset into a single self-contained HTML
// dashboard (docs/spec.md §6). The output inlines all CSS and JS, depends on no
// external asset, and is therefore double-clickable from disk as well as
// serveable over HTTP (AC-7). All user-facing text is English (AC-9).
//
// The work splits in two: BuildView is a pure function that derives every number
// the dashboard shows (testable without parsing HTML), and Render feeds that
// view to the embedded template. Callers receive HTML bytes; this package never
// decides where the file lands on disk (that wiring is the CLI's, T6).
package render

import (
	"bytes"
	"fmt"
	"html/template"
	"sort"
	"strings"
	"time"

	"github.com/csthink/llmlore/internal/model"
	"github.com/csthink/llmlore/web"
)

// Dashboard sizes. The catalog is already trimmed to a readable size upstream
// (store.Select), so these are display caps on the highlight sections, not data
// limits — the filterable card grid below always shows every repo.
const (
	topByStarsLimit = 15
	trendingLimit   = 30
	growingLimit    = 15
)

// window7d and window30d are the two look-back windows the "Fastest growing"
// lane reports, computed from each repo's star_snapshots (docs/design.md §6, the
// "self-computed long-term gain" lane). The shorter window doubles as the sort
// tiebreaker after the longer one.
var (
	window7d  = 7 * 24 * time.Hour
	window30d = 30 * 24 * time.Hour
)

// View is the fully-derived dashboard model. Every field is render-ready: counts
// are computed, deltas are formatted with sign, and slices are pre-sorted, so
// the template carries no logic beyond ranging and printing.
type View struct {
	GeneratedAt  string
	Mode         string
	Total        int
	NewThisRound int
	TopicDist    []LabelCount
	TypeDist     []LabelCount
	TopByStars   []RankRow
	Trending     []RankRow
	Growing      []GrowthRow
	Topics       []string // filter options, sorted
	Types        []string
	Languages    []string
	Cards        []Card
}

// LabelCount is one (label, count) bar in a distribution.
type LabelCount struct {
	Label string
	N     int
}

// RankRow is one line in the "Top by stars" / "Trending on GitHub" tables.
type RankRow struct {
	Rank      int
	ID        string
	URL       string
	Language  string
	LangColor string
	Stars     int
	Stale     bool
}

// GrowthRow is one line in the "Fastest growing" table. D7/D30 are pre-formatted
// (e.g. "+128", "0", or "—" when the window predates the repo's history).
type GrowthRow struct {
	ID    string
	URL   string
	Stars int
	D7    string
	D30   string
}

// Card is one repository card in the filterable grid (spec §6.5). The *Attr
// fields are lowercase, space/data-ready values the client-side filter reads off
// data-* attributes.
type Card struct {
	ID          string
	URL         string
	Owner       string
	Name        string
	Description string
	Summary     string
	Language    string
	LangColor   string
	Stars       int
	Type        string
	Topics      []string
	TopicsAttr  string // space-joined topics for the data attribute
	Active      bool   // !is_stale; drives the "active only" filter
	Stale       bool
	PushedAt    string // YYYY-MM-DD, empty when unknown
}

// Render builds the dashboard view from d (as of now) and executes the embedded
// template, returning the self-contained HTML. now drives the growth windows and
// is taken explicitly so output is deterministic and testable.
func Render(d *model.Dataset, now time.Time) ([]byte, error) {
	view := BuildView(d, now)
	tmpl, err := template.ParseFS(web.TemplatesFS, "templates/*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse dashboard template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "dashboard.tmpl", view); err != nil {
		return nil, fmt.Errorf("render dashboard: %w", err)
	}
	return buf.Bytes(), nil
}

// BuildView derives the dashboard model from d as of now. It is pure: same
// inputs, same output, no I/O. A nil or empty dataset yields a valid, empty
// dashboard rather than an error.
func BuildView(d *model.Dataset, now time.Time) View {
	v := View{Mode: "unknown"}
	if d == nil {
		return v
	}
	repos := d.Repos
	v.Total = len(repos)
	v.Mode = orUnknown(d.Meta.Mode)
	v.GeneratedAt = formatTime(d.Meta.GeneratedAt)
	v.NewThisRound = countNewThisRound(repos, d.Meta.GeneratedAt)

	// Distributions: a repo contributes once per topic it carries, once for its type.
	v.TopicDist = distribution(repos, func(r model.Repo) []string { return r.Topics })
	v.TypeDist = distribution(repos, func(r model.Repo) []string { return []string{r.Type} })

	// One star-descending order underlies the top table and the card grid so the
	// dashboard reads consistently top to bottom.
	ranked := sortedByStars(repos)

	for i, r := range ranked {
		if i >= topByStarsLimit {
			break
		}
		v.TopByStars = append(v.TopByStars, rankRow(i+1, r))
	}

	for _, r := range ranked {
		if r.Source != model.SourceTrending {
			continue
		}
		if len(v.Trending) >= trendingLimit {
			break
		}
		v.Trending = append(v.Trending, rankRow(len(v.Trending)+1, r))
	}

	v.Growing = growthRows(ranked, now)
	v.Topics, v.Types, v.Languages = filterOptions(repos)
	v.Cards = cards(ranked)
	return v
}

// countNewThisRound counts repos first added in the latest run. Convention: a
// run stamps meta.generated_at and the new records' added_at from the same round
// timestamp, so "added this round" == AddedAt equal to GeneratedAt. A zero
// GeneratedAt (incomplete meta) yields 0 rather than miscounting every repo.
func countNewThisRound(repos []model.Repo, generatedAt time.Time) int {
	if generatedAt.IsZero() {
		return 0
	}
	n := 0
	for _, r := range repos {
		if r.AddedAt.Equal(generatedAt) {
			n++
		}
	}
	return n
}

// distribution counts repos by the labels keyExtractor returns, sorted by count
// descending then label ascending for stable output.
func distribution(repos []model.Repo, keyExtractor func(model.Repo) []string) []LabelCount {
	counts := map[string]int{}
	for _, r := range repos {
		for _, k := range keyExtractor(r) {
			if k == "" {
				continue
			}
			counts[k]++
		}
	}
	out := make([]LabelCount, 0, len(counts))
	for label, n := range counts {
		out = append(out, LabelCount{Label: label, N: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// sortedByStars returns repos copied and ordered by stars descending, id
// ascending as a stable tiebreaker (matching store.Select's ranking).
func sortedByStars(repos []model.Repo) []model.Repo {
	out := make([]model.Repo, len(repos))
	copy(out, repos)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Stars != out[j].Stars {
			return out[i].Stars > out[j].Stars
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func rankRow(rank int, r model.Repo) RankRow {
	return RankRow{
		Rank:      rank,
		ID:        r.ID,
		URL:       r.URL,
		Language:  r.Language,
		LangColor: languageColor(r.Language),
		Stars:     r.Stars,
		Stale:     r.IsStale,
	}
}

// growthRows ranks repos by their self-computed 30-day star gain (then 7-day,
// then id), keeping only those with enough snapshot history to measure. Repos
// with a single snapshot have no measurable gain and are omitted.
func growthRows(ranked []model.Repo, now time.Time) []GrowthRow {
	type scored struct {
		row     GrowthRow
		d30, d7 int
		id      string
	}
	var scoredRows []scored
	for _, r := range ranked {
		g30, ok30 := gainSince(r.StarSnapshots, now, window30d)
		g7, ok7 := gainSince(r.StarSnapshots, now, window7d)
		if !ok30 && !ok7 {
			continue // no measurable history
		}
		scoredRows = append(scoredRows, scored{
			row: GrowthRow{
				ID:    r.ID,
				URL:   r.URL,
				Stars: r.Stars,
				D7:    formatDelta(g7, ok7),
				D30:   formatDelta(g30, ok30),
			},
			d30: g30,
			d7:  g7,
			id:  r.ID,
		})
	}
	sort.Slice(scoredRows, func(i, j int) bool {
		if scoredRows[i].d30 != scoredRows[j].d30 {
			return scoredRows[i].d30 > scoredRows[j].d30
		}
		if scoredRows[i].d7 != scoredRows[j].d7 {
			return scoredRows[i].d7 > scoredRows[j].d7
		}
		return scoredRows[i].id < scoredRows[j].id
	})
	out := make([]GrowthRow, 0, len(scoredRows))
	for i, s := range scoredRows {
		if i >= growingLimit {
			break
		}
		out = append(out, s.row)
	}
	return out
}

// gainSince returns the star gain from the baseline snapshot to the latest one,
// where the baseline is the last snapshot at or before now-window (or the
// earliest snapshot when the whole history is newer than the window). It reports
// ok=false when fewer than two snapshots exist, i.e. no gain can be measured.
func gainSince(snaps []model.StarSnapshot, now time.Time, window time.Duration) (int, bool) {
	if len(snaps) < 2 {
		return 0, false
	}
	latest := snaps[len(snaps)-1]
	cutoff := now.Add(-window)
	baseline := snaps[0]
	for _, s := range snaps {
		if s.T.After(cutoff) {
			break
		}
		baseline = s
	}
	return latest.Stars - baseline.Stars, true
}

// filterOptions returns the distinct topics, types, and languages present,
// each sorted ascending, for populating the filter controls.
func filterOptions(repos []model.Repo) (topics, types, languages []string) {
	topicSet := map[string]bool{}
	typeSet := map[string]bool{}
	langSet := map[string]bool{}
	for _, r := range repos {
		for _, t := range r.Topics {
			topicSet[t] = true
		}
		if r.Type != "" {
			typeSet[r.Type] = true
		}
		if r.Language != "" {
			langSet[r.Language] = true
		}
	}
	return sortedKeys(topicSet), sortedKeys(typeSet), sortedKeys(langSet)
}

func cards(ranked []model.Repo) []Card {
	out := make([]Card, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, Card{
			ID:          r.ID,
			URL:         r.URL,
			Owner:       r.Owner,
			Name:        r.Name,
			Description: r.Description,
			Summary:     r.Summary,
			Language:    r.Language,
			LangColor:   languageColor(r.Language),
			Stars:       r.Stars,
			Type:        r.Type,
			Topics:      r.Topics,
			TopicsAttr:  strings.Join(r.Topics, " "),
			Active:      !r.IsStale,
			Stale:       r.IsStale,
			PushedAt:    formatDate(r.PushedAt),
		})
	}
	return out
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func formatDelta(delta int, ok bool) string {
	if !ok {
		return "—"
	}
	if delta > 0 {
		return fmt.Sprintf("+%d", delta)
	}
	return fmt.Sprintf("%d", delta)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// languageColor maps a language to a swatch color, mirroring familiar GitHub
// linguist hues. Unknown or empty languages fall back to a neutral grey.
func languageColor(lang string) string {
	if c, ok := langColors[lang]; ok {
		return c
	}
	return "#b0b0b0"
}

var langColors = map[string]string{
	"Python":           "#3572A5",
	"JavaScript":       "#f1e05a",
	"TypeScript":       "#3178c6",
	"Go":               "#00ADD8",
	"Rust":             "#dea584",
	"Java":             "#b07219",
	"C++":              "#f34b7d",
	"C":                "#555555",
	"Ruby":             "#701516",
	"Shell":            "#89e051",
	"Jupyter Notebook": "#DA5B0B",
	"HTML":             "#e34c26",
	"Swift":            "#F05138",
	"Kotlin":           "#A97BFF",
	"PHP":              "#4F5D95",
	"C#":               "#178600",
}
