// Package enricher turns a classified candidate into a catalog record and keeps
// a re-seen record's activity fresh. It is the enrichment stage of the pipeline
// (docs/design.md §1), between the classifier and the store.
//
// Every function is pure and takes an explicit `now` plus a staleness threshold,
// so behaviour is deterministic and testable. The crucial invariant for cost
// control (design §8 "only classify new repos") lives in Refresh: it appends a
// star snapshot and refreshes volatile facts but NEVER re-classifies, never
// rewrites history, and never moves AddedAt.
package enricher

import (
	"time"

	"github.com/csthink/llmlore/internal/classifier"
	"github.com/csthink/llmlore/internal/collector"
	"github.com/csthink/llmlore/internal/model"
)

// DefaultStaleThreshold is the default age past which a repo is considered stale
// (spec §3: ~12 months). A repo with no known pushed_at is never stale.
const DefaultStaleThreshold = 365 * 24 * time.Hour

// NewRepo builds a first-time catalog record from a kept candidate and its
// classifier decision. The record starts with a single star snapshot at `now`
// and AddedAt = now.
func NewRepo(cand collector.Candidate, dec classifier.Decision, now time.Time, staleAfter time.Duration) model.Repo {
	return model.Repo{
		ID:            cand.ID,
		URL:           cand.URL,
		Owner:         cand.Owner,
		Name:          cand.Name,
		Description:   cand.Description,
		Language:      cand.Language,
		Stars:         cand.Stars,
		Summary:       dec.Summary,
		Type:          dec.Type,
		Topics:        dec.Topics,
		PushedAt:      cand.PushedAt,
		IsStale:       deriveStale(cand.PushedAt, now, staleAfter),
		Source:        cand.Source,
		AddedAt:       now,
		ClassifiedBy:  dec.ClassifiedBy,
		StarSnapshots: []model.StarSnapshot{{T: now, Stars: cand.Stars}},
	}
}

// Refresh updates a record already in the catalog with a fresh observation. It
// appends a star snapshot, refreshes volatile facts (stars, pushed_at,
// description, language), and recomputes staleness — while preserving the
// record's identity, classification (type/topics/summary/classified_by),
// AddedAt, and full snapshot history unchanged. That preservation is what lets
// the pipeline skip the LLM for re-seen repos.
//
// An out-of-order or replayed observation (`now` earlier than the latest
// snapshot) is rejected WHOLESALE: prev is returned untouched, with neither a
// snapshot appended nor any volatile field overwritten. The snapshot history and
// the current state share one accept/reject decision, so a stale observation can
// never regress stars or flip is_stale while the history correctly refuses it.
//
// The returned Repo does not alias prev's snapshot slice, so the caller's prev
// is never mutated.
func Refresh(prev model.Repo, cand collector.Candidate, now time.Time, staleAfter time.Duration) model.Repo {
	if n := len(prev.StarSnapshots); n > 0 && now.Before(prev.StarSnapshots[n-1].T) {
		return prev // out-of-order: accept nothing, change nothing
	}

	out := prev // struct copy; slices still shared until reassigned below
	out.Stars = cand.Stars
	if !cand.PushedAt.IsZero() {
		out.PushedAt = cand.PushedAt // keep prior pushed_at if this source can't observe it
	}
	if cand.Description != "" {
		out.Description = cand.Description
	}
	if cand.Language != "" {
		out.Language = cand.Language
	}
	out.IsStale = deriveStale(out.PushedAt, now, staleAfter)
	out.StarSnapshots = appendSnapshot(prev.StarSnapshots, model.StarSnapshot{T: now, Stars: cand.Stars})
	return out
}

// appendSnapshot returns a new slice with next appended, copying history so the
// input is never mutated. Ordering is the caller's responsibility (Refresh gates
// out-of-order observations before calling this).
func appendSnapshot(history []model.StarSnapshot, next model.StarSnapshot) []model.StarSnapshot {
	out := make([]model.StarSnapshot, len(history), len(history)+1)
	copy(out, history)
	return append(out, next)
}

// deriveStale reports whether a repo is stale. An unknown (zero) pushed_at is
// treated as "not stale": we cannot judge activity, and marking a trending
// newcomer stale would be a false negative for freshness (spec §3 / T4 Q2).
func deriveStale(pushedAt, now time.Time, staleAfter time.Duration) bool {
	if pushedAt.IsZero() {
		return false
	}
	return now.Sub(pushedAt) > staleAfter
}
