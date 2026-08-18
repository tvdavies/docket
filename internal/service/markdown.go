package service

import (
	"bytes"
	"fmt"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var webMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
)

// renderMarkdown renders trusted presentation HTML from untrusted Markdown.
// Goldmark's default renderer omits raw HTML and rejects dangerous link URLs.
func renderMarkdown(source string) (string, error) {
	var output bytes.Buffer
	if err := webMarkdown.Convert([]byte(source), &output); err != nil {
		return "", fmt.Errorf("render markdown: %w", err)
	}
	return output.String(), nil
}
