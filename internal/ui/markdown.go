package ui

import (
	"regexp"
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
)

type rendererKey struct {
	width int
	dark  bool
	plain bool
}

var (
	renderersMu sync.Mutex
	renderers   = map[rendererKey]*glamour.TermRenderer{}
)

// Markdown renders a description or comment body with glamour, wrapped to
// width. Surrounding blank lines and the document margin are removed so the
// caller controls layout. If rendering fails the source is returned wrapped.
func (t Theme) Markdown(source string, width int) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}
	if width < 20 {
		width = 20
	}
	renderer, err := t.markdownRenderer(width)
	if err == nil {
		renderersMu.Lock()
		out, renderErr := renderer.Render(source)
		renderersMu.Unlock()
		if renderErr == nil {
			return tidy(out)
		}
	}
	return lipgloss.NewStyle().Width(width).Render(source)
}

func (t Theme) markdownRenderer(width int) (*glamour.TermRenderer, error) {
	key := rendererKey{width: width, dark: t.Dark, plain: t.Plain}
	renderersMu.Lock()
	defer renderersMu.Unlock()
	if renderer, ok := renderers[key]; ok {
		return renderer, nil
	}
	var style ansi.StyleConfig
	switch {
	case t.Plain:
		style = styles.NoTTYStyleConfig
	case t.Dark:
		style = styles.DarkStyleConfig
	default:
		style = styles.LightStyleConfig
	}
	// Copy-on-write: StyleConfig is a value, so changing the document block
	// here never touches glamour's shared defaults.
	var margin uint
	style.Document.Margin = &margin
	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	renderer, err := glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(width))
	if err != nil {
		return nil, err
	}
	renderers[key] = renderer
	return renderer, nil
}

// trailingPad matches the styled spaces glamour pads each line with.
var trailingPad = regexp.MustCompile(`(?:\x1b\[[0-9;]*m| )+$`)

// tidy trims padding glamour adds: trailing (possibly styled) spaces on each
// line and blank lines around the document.
func tidy(rendered string) string {
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		lines[i] = trailingPad.ReplaceAllStringFunc(line, func(pad string) string {
			if strings.Contains(pad, "\x1b") {
				return "\x1b[m" // the removed run may have closed a style
			}
			return ""
		})
	}
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(stripANSI(lines[start])) == "" {
		start++
	}
	for end > start && strings.TrimSpace(stripANSI(lines[end-1])) == "" {
		end--
	}
	return strings.Join(lines[start:end], "\n")
}
