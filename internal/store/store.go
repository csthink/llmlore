// Package store owns the catalog dataset on disk: loading, deduplicating,
// incrementally merging a run's results, selecting a readable subset, and
// writing data/repos.json — the single source of truth (docs/design.md §1).
//
// The package is deliberately dumb about classification and enrichment: it
// merges model.Repo records by id and never re-derives their contents. The
// per-record snapshot append and staleness live in the enricher; here we keep
// the dataset as a whole consistent.
package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/csthink/llmlore/internal/collector"
	"github.com/csthink/llmlore/internal/model"
)

// Default per-category caps keep the catalog readable (design §5). They are
// constants for now; making them configurable is a TODO deferred to a later
// task (do not grow config for this yet).
const (
	DefaultPerTypeCap  = 60
	DefaultPerTopicCap = 40
)

// Load reads the dataset at path. A missing file is the first-run case and
// yields an empty, current-schema dataset rather than an error.
func Load(path string) (*model.Dataset, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &model.Dataset{Meta: model.Meta{SchemaVersion: model.CurrentSchemaVersion}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open dataset %s: %w", path, err)
	}
	defer f.Close()
	return model.Load(f)
}

// Save writes the dataset to path atomically (temp file + rename) after a full
// validation, so the source of truth never receives schema-invalid data. Parent
// directories are created as needed.
func Save(path string, d *model.Dataset) error {
	if err := d.Validate(); err != nil {
		return fmt.Errorf("refusing to write invalid dataset: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	if err := d.Encode(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// Index maps repo id → repo for membership tests and prev lookups, letting the
// caller split a run's candidates into new (classify) vs existing (refresh only).
func Index(d *model.Dataset) map[string]model.Repo {
	idx := make(map[string]model.Repo, len(d.Repos))
	for _, r := range d.Repos {
		idx[r.ID] = r
	}
	return idx
}

// DedupeCandidates collapses one run's candidates to one per id so a repo gets a
// single star snapshot per run even when both modes surface it. A search hit
// wins over a trending hit (its fields are more complete); among same-source
// duplicates the first is kept.
//
// TODO: the "also appeared on trending" fact for a search-won duplicate is
// currently dropped — model.Repo has no second-source field. Record it only if
// the schema later gains one; do not extend scope for it now.
func DedupeCandidates(cands []collector.Candidate) []collector.Candidate {
	bestAt := make(map[string]int) // id → index into out
	var out []collector.Candidate
	for _, c := range cands {
		if i, ok := bestAt[c.ID]; ok {
			if out[i].Source != model.SourceSearch && c.Source == model.SourceSearch {
				out[i] = c // upgrade trending → search
			}
			continue
		}
		bestAt[c.ID] = len(out)
		out = append(out, c)
	}
	return out
}

// Merge folds a run's enriched records into the existing dataset by id: an
// incoming record replaces the existing one with the same id (it already carries
// the appended snapshot and refreshed fields from the enricher), and a new id is
// appended. Existing records not seen this run are kept untouched. Meta is
// preserved for the caller to stamp.
func Merge(existing *model.Dataset, incoming []model.Repo) *model.Dataset {
	repos := make([]model.Repo, len(existing.Repos))
	copy(repos, existing.Repos)

	at := make(map[string]int, len(repos))
	for i, r := range repos {
		at[r.ID] = i
	}
	for _, r := range incoming {
		if i, ok := at[r.ID]; ok {
			repos[i] = r
		} else {
			at[r.ID] = len(repos)
			repos = append(repos, r)
		}
	}
	return &model.Dataset{Meta: existing.Meta, Repos: repos}
}

// SelectOptions bounds the catalog to a readable size (design §5). A zero cap or
// MinStars means "no limit" for that dimension.
type SelectOptions struct {
	MinStars    int
	PerTypeCap  int
	PerTopicCap int
}

// Select returns a dataset trimmed to a readable subset: repos below MinStars
// are dropped, then repos are admitted in descending-star order subject to
// per-type and per-topic caps.
//
// Cap semantics (PROPOSAL-001, option b — ratified): each per-topic / per-type
// FILTER VIEW shows at most N cards. A repo is admitted only when its type AND
// EVERY one of its topics is still under cap; if any topic is already full the
// whole repo is rejected. On admission the type counter and ALL of the repo's
// topic counters increment. Because high-star repos are considered first, they
// take the limited slots, and no topic view can ever exceed N (every card
// carrying topic t was admitted while t was under cap). The trade-off is that a
// high-star multi-topic repo can be dropped if a single one of its topics is
// saturated — an intentional choice favouring a hard per-view guarantee.
func Select(d *model.Dataset, opts SelectOptions) *model.Dataset {
	ranked := make([]model.Repo, 0, len(d.Repos))
	for _, r := range d.Repos {
		if r.Stars >= opts.MinStars {
			ranked = append(ranked, r)
		}
	}
	// Stars descending; id as a stable tiebreaker for deterministic output.
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Stars != ranked[j].Stars {
			return ranked[i].Stars > ranked[j].Stars
		}
		return ranked[i].ID < ranked[j].ID
	})

	typeCount := map[string]int{}
	topicCount := map[string]int{}
	kept := make([]model.Repo, 0, len(ranked))
	for _, r := range ranked {
		if !underCap(typeCount[r.Type], opts.PerTypeCap) {
			continue
		}
		if !allTopicsUnderCap(r.Topics, topicCount, opts.PerTopicCap) {
			continue // any saturated topic rejects the whole repo (visible-card cap)
		}
		typeCount[r.Type]++
		for _, t := range r.Topics {
			topicCount[t]++
		}
		kept = append(kept, r)
	}
	return &model.Dataset{Meta: d.Meta, Repos: kept}
}

// underCap reports whether count is below limit; a non-positive limit means no cap.
func underCap(count, limit int) bool {
	return limit <= 0 || count < limit
}

// allTopicsUnderCap reports whether EVERY topic still has room under limit, so
// admitting the repo cannot push any topic view past the cap.
func allTopicsUnderCap(topics []string, counts map[string]int, limit int) bool {
	if limit <= 0 {
		return true
	}
	for _, t := range topics {
		if counts[t] >= limit {
			return false
		}
	}
	return true
}
