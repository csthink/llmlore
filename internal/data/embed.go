// Package data provides the offline fallback snapshot embedded into the binary.
//
// docs/design.md §8 calls for an embedded snapshot as an offline fallback: when
// no dataset exists on disk (a fresh brew install that has not run `pull` or
// `update` yet, AC-1), the binary still has data to render.
//
// go:embed can only reach files at or below this file's directory, so the
// embedded snapshot lives here as internal/data/snapshot.json — a frozen,
// point-in-time copy. It is deliberately separate from the live, on-disk
// data/repos.json (the open dataset that pull/update refresh): this one is the
// immutable fallback compiled into the binary, that one is regenerable runtime
// state. They are seeded with identical content at T6.
package data

import (
	"bytes"
	_ "embed"

	"github.com/csthink/llmlore/internal/model"
)

//go:embed snapshot.json
var snapshot []byte

// Snapshot returns the embedded fallback dataset, decoded and schema-checked.
// It is used only when the on-disk dataset is absent or empty, so the cost of
// decoding on every call is irrelevant. A decode failure here is a build-time
// data error (the embedded bytes ship with the binary), surfaced as an error
// rather than a panic so the CLI can report it cleanly.
func Snapshot() (*model.Dataset, error) {
	return model.Load(bytes.NewReader(snapshot))
}
