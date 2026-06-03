// Package collector fetches raw repository candidates from upstream sources.
//
// It is the first stage of the pipeline (see docs/design.md §1): search hits the
// GitHub Search API for historical high-star repositories, trending scrapes
// github.com/trending for recent momentum. Both emit the same Candidate shape —
// classification, tagging, and persistence happen downstream (T3/T4), so a
// Candidate deliberately carries only the raw facts a source can observe, NOT
// the type/topics/summary/classified_by fields of model.Repo.
//
// Network failures surface as *UpstreamError so a caller can map them to exit
// code 3 (spec §1). The collector never calls os.Exit itself; it is a library.
package collector

import (
	"errors"
	"fmt"
)

// ExitUpstream is the process exit code for a network / upstream failure
// (spec §1). The collector returns *UpstreamError; the CLI maps it to this code.
const ExitUpstream = 3

// Candidate is a pre-classification repository observation. Fields a given
// source cannot determine (e.g. trending does not expose a reliable pushed_at)
// are left at their zero value for a later stage to enrich.
type Candidate struct {
	ID          string // "owner/name", the global dedup key (model.NormalizeID)
	Owner       string
	Name        string
	URL         string
	Description string
	Language    string
	Stars       int
	Source      string // model.SourceSearch or model.SourceTrending
}

// UpstreamError wraps a failure talking to an external service (GitHub API or
// the trending page). The CLI recognizes it via IsUpstream and exits with
// ExitUpstream.
type UpstreamError struct {
	Op  string // the operation that failed, e.g. "github search"
	Err error
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("%s: %v", e.Op, e.Err)
}

func (e *UpstreamError) Unwrap() error { return e.Err }

// upstreamErrorf builds an *UpstreamError with a formatted cause.
func upstreamErrorf(op, format string, args ...any) *UpstreamError {
	return &UpstreamError{Op: op, Err: fmt.Errorf(format, args...)}
}

// IsUpstream reports whether err is (or wraps) an *UpstreamError, so callers can
// distinguish a network/upstream failure (exit 3) from other errors.
func IsUpstream(err error) bool {
	var ue *UpstreamError
	return errors.As(err, &ue)
}
