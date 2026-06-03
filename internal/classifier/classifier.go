// Package classifier decides whether a collected repository belongs in the
// catalog and labels it with a type, topics, and (when an LLM is configured) a
// one-line summary. It is the second pipeline stage (docs/design.md §1), sitting
// between the collector and the store.
//
// Two layers, picked by configuration (spec §5, design §4):
//   - heuristic: zero-key keyword scoring; rough by design. Records
//     classified_by=heuristic.
//   - llm: an LLM judges keep/type/topics/summary via a pluggable provider.
//     Records classified_by=llm; falls back to heuristic for an individual
//     candidate whose response cannot be parsed.
//
// The classifier is side-effect free: it only returns a Decision. It NEVER drops
// a candidate itself — a Decision with Keep=false means "do not store this", and
// enforcing that is the DOWNSTREAM caller's job (T4 store / T6 wiring). A
// downstream stage that ignores Keep=false would let rejected repositories into
// the catalog.
package classifier

import (
	"context"
	"errors"
	"fmt"

	"github.com/csthink/llmlore/internal/collector"
	"github.com/csthink/llmlore/internal/config"
)

// Process exit codes this stage can demand of the CLI (spec §1). The classifier
// is a library and never calls os.Exit; it returns the typed errors below and
// the CLI (T6) maps them.
const (
	ExitUpstream      = 3 // network / timeout / 5xx / rate-limit retries exhausted
	ExitMissingConfig = 4 // provider rejected the credentials (401/403)
)

// Decision is the classifier's verdict for one candidate. It mirrors the
// catalog fields the next stages need, but is deliberately NOT a model.Repo:
// stars, snapshots, staleness, and timestamps are added later (T4).
//
// Keep=false means the candidate must not be stored. Type/Topics/Summary are
// only meaningful when Keep is true.
type Decision struct {
	Keep         bool
	Type         string   // controlled type vocabulary (model.Type*)
	Topics       []string // controlled topic vocabulary, >= 1 when Keep
	Summary      string   // one-line English summary; empty on the heuristic path
	ClassifiedBy string   // model.ClassifiedByLLM or model.ClassifiedByHeuristic
}

// Classifier turns a raw candidate into a Decision. Implementations are pure
// with respect to the catalog: they observe a candidate and return a verdict.
type Classifier interface {
	Classify(ctx context.Context, c collector.Candidate) (Decision, error)
}

// New selects the classifier implied by configuration: the LLM path when a
// provider and key are present (cfg.UseLLM), otherwise the heuristic fallback.
// To force the heuristic path despite a configured key (the --no-llm flag),
// call NewHeuristic directly.
func New(cfg *config.Config) Classifier {
	if cfg.UseLLM() {
		return NewLLM(newOpenAIProvider(cfg.LLM))
	}
	return NewHeuristic()
}

// UpstreamError wraps a network/transport-level failure talking to the LLM
// provider (including rate-limit retries exhausted). The CLI maps it to
// ExitUpstream. It mirrors collector.UpstreamError so both pipeline stages read
// alike; T6 may later consolidate exit-code mapping into one place.
type UpstreamError struct {
	Op  string
	Err error
}

func (e *UpstreamError) Error() string { return fmt.Sprintf("%s: %v", e.Op, e.Err) }
func (e *UpstreamError) Unwrap() error { return e.Err }

// MissingConfigError signals that the provider rejected the credentials (401/403)
// — i.e. a required key is missing or wrong. The CLI maps it to ExitMissingConfig.
type MissingConfigError struct {
	Op  string
	Err error
}

func (e *MissingConfigError) Error() string { return fmt.Sprintf("%s: %v", e.Op, e.Err) }
func (e *MissingConfigError) Unwrap() error { return e.Err }

// IsUpstream reports whether err is (or wraps) an *UpstreamError (exit 3).
func IsUpstream(err error) bool {
	var ue *UpstreamError
	return errors.As(err, &ue)
}

// IsMissingConfig reports whether err is (or wraps) a *MissingConfigError (exit 4).
func IsMissingConfig(err error) bool {
	var me *MissingConfigError
	return errors.As(err, &me)
}
