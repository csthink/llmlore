// Package web embeds the dashboard templates so the renderer can produce a
// fully self-contained HTML file with no external asset dependencies.
//
// go:embed can only reach files at or below the embedding .go file's directory.
// The templates live under web/templates/* (their owning location per
// docs/tasks.md T5), so this file sits in web/ to embed them and exposes the
// filesystem to internal/render. It is the embed glue for the templates, not a
// second home for rendering logic.
package web

import "embed"

// TemplatesFS holds the dashboard HTML templates. The renderer parses these;
// callers should not depend on the concrete file layout.
//
//go:embed templates/*.tmpl
var TemplatesFS embed.FS
