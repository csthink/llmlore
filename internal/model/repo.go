// Package model defines the on-disk data contract for llmlore.
//
// data/repos.json is the single source of truth (see docs/spec.md §3-§4); the
// HTML dashboard is a regenerable read-only view of it. Every struct, tag, and
// controlled-vocabulary value here mirrors that spec exactly. When the spec
// changes the schema, bump CurrentSchemaVersion and update these types together.
package model

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// CurrentSchemaVersion is the schema version this binary reads and writes.
// Load rejects datasets carrying any other version.
const CurrentSchemaVersion = 1

// Controlled vocabulary — type axis (single value per repo). See spec §4.
const (
	TypeTutorial = "tutorial" // courses / learning paths
	TypeExample  = "example"  // runnable example apps / demos
	TypeTemplate = "template" // prompt / agent / workflow template collections
	TypeGuide    = "guide"    // getting-started guides for a tool / framework
)

// Controlled vocabulary — topic axis (one or more per repo, extensible). See spec §4.
const (
	TopicLLM        = "llm"
	TopicAgent      = "agent"
	TopicRAG        = "rag"
	TopicMultimodal = "multimodal"
	TopicAICoding   = "ai-coding"
	TopicEval       = "eval"
	TopicInfra      = "infra"
	TopicPrompt     = "prompt"
	TopicOther      = "other"
)

// Controlled vocabulary — source of a candidate. See spec §3.
const (
	SourceSearch   = "search"
	SourceTrending = "trending"
)

// Controlled vocabulary — how a repo was classified. See spec §3.
const (
	ClassifiedByLLM       = "llm"
	ClassifiedByHeuristic = "heuristic"
)

// validTypes / validTopics / validSources / validClassifiedBy back the
// validation helpers below. Keep them in sync with the constants above.
var (
	validTypes = map[string]bool{
		TypeTutorial: true, TypeExample: true, TypeTemplate: true, TypeGuide: true,
	}
	validTopics = map[string]bool{
		TopicLLM: true, TopicAgent: true, TopicRAG: true, TopicMultimodal: true,
		TopicAICoding: true, TopicEval: true, TopicInfra: true, TopicPrompt: true,
		TopicOther: true,
	}
	validSources = map[string]bool{
		SourceSearch: true, SourceTrending: true,
	}
	validClassifiedBy = map[string]bool{
		ClassifiedByLLM: true, ClassifiedByHeuristic: true,
	}
)

// ValidType reports whether t is in the controlled type vocabulary.
func ValidType(t string) bool { return validTypes[t] }

// ValidTopic reports whether t is in the controlled topic vocabulary.
func ValidTopic(t string) bool { return validTopics[t] }

// ValidSource reports whether s is a known candidate source.
func ValidSource(s string) bool { return validSources[s] }

// ValidClassifiedBy reports whether c is a known classification origin.
func ValidClassifiedBy(c string) bool { return validClassifiedBy[c] }

// Dataset is the top-level shape of data/repos.json.
type Dataset struct {
	Meta  Meta   `json:"meta"`
	Repos []Repo `json:"repos"`
}

// Meta carries dataset-level bookkeeping.
type Meta struct {
	SchemaVersion int       `json:"schema_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	Mode          string    `json:"mode"`
	Count         int       `json:"count"`
}

// Repo is a single curated repository record. Field order and JSON tags follow
// spec §3 verbatim.
type Repo struct {
	ID            string         `json:"id"` // "owner/name", global dedup key
	URL           string         `json:"url"`
	Owner         string         `json:"owner"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Language      string         `json:"language"`
	Stars         int            `json:"stars"`
	Summary       string         `json:"summary"` // one-line, LLM-generated
	Type          string         `json:"type"`    // controlled type vocabulary
	Topics        []string       `json:"topics"`  // controlled topic vocabulary, >= 1
	PushedAt      time.Time      `json:"pushed_at"`
	IsStale       bool           `json:"is_stale"` // derived: now - pushed_at > threshold
	Source        string         `json:"source"`
	AddedAt       time.Time      `json:"added_at"`
	ClassifiedBy  string         `json:"classified_by"`
	StarSnapshots []StarSnapshot `json:"star_snapshots"` // append-only, ascending by time
}

// StarSnapshot is one (timestamp, stars) point in a repo's history.
type StarSnapshot struct {
	T     time.Time `json:"t"`
	Stars int       `json:"stars"`
}

// Validate checks a single repo against the field constraints in spec §3-§4.
// It returns a human-readable English error describing the first violation.
func (r Repo) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("repo id must not be empty")
	}
	if r.Owner == "" || r.Name == "" {
		return fmt.Errorf("repo %q: owner and name must not be empty", r.ID)
	}
	if want := r.Owner + "/" + r.Name; r.ID != want {
		return fmt.Errorf("repo %q: id must equal \"owner/name\" (%q)", r.ID, want)
	}
	if !ValidType(r.Type) {
		return fmt.Errorf("repo %q: invalid type %q", r.ID, r.Type)
	}
	if len(r.Topics) == 0 {
		return fmt.Errorf("repo %q: at least one topic is required", r.ID)
	}
	for _, t := range r.Topics {
		if !ValidTopic(t) {
			return fmt.Errorf("repo %q: invalid topic %q", r.ID, t)
		}
	}
	if !ValidSource(r.Source) {
		return fmt.Errorf("repo %q: invalid source %q", r.ID, r.Source)
	}
	if !ValidClassifiedBy(r.ClassifiedBy) {
		return fmt.Errorf("repo %q: invalid classified_by %q", r.ID, r.ClassifiedBy)
	}
	return nil
}

// Validate checks dataset-level invariants: a supported schema version and that
// every repo is individually valid.
func (d Dataset) Validate() error {
	if err := checkSchemaVersion(d.Meta.SchemaVersion); err != nil {
		return err
	}
	for _, r := range d.Repos {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func checkSchemaVersion(v int) error {
	if v != CurrentSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d (this build supports %d)", v, CurrentSchemaVersion)
	}
	return nil
}

// Load decodes a dataset from r and verifies its schema_version. It does NOT
// run full per-repo validation, so partially-built datasets remain readable;
// call Dataset.Validate for strict checks.
func Load(r io.Reader) (*Dataset, error) {
	var d Dataset
	dec := json.NewDecoder(r)
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("decode dataset: %w", err)
	}
	if err := checkSchemaVersion(d.Meta.SchemaVersion); err != nil {
		return nil, err
	}
	return &d, nil
}

// Encode writes the dataset to w as indented JSON (human-readable, diff-friendly).
// It refreshes Meta.Count to match the repo slice so the file stays consistent.
func (d *Dataset) Encode(w io.Writer) error {
	d.Meta.Count = len(d.Repos)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(d); err != nil {
		return fmt.Errorf("encode dataset: %w", err)
	}
	return nil
}

// MarshalJSON-free helper kept intentionally absent: standard time.Time JSON
// marshalling already produces the RFC3339 timestamps the spec shows.

// NormalizeID builds the canonical "owner/name" id, trimming stray slashes.
func NormalizeID(owner, name string) string {
	return strings.Trim(owner, "/") + "/" + strings.Trim(name, "/")
}
