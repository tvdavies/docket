// Package ui renders docket tasks for people: the styled `docket show` and
// `docket list` output and the content of the interactive board share it, so
// the CLI and the TUI look the same. It never affects JSON or piped output.
package ui

import (
	"hash/fnv"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Theme is the small palette every styled surface draws from. Colours have
// light and dark variants chosen once from the terminal background; output is
// downsampled (or stripped for NO_COLOR and TERM=dumb) by the writer.
type Theme struct {
	// Dark reports whether the terminal background is dark.
	Dark bool
	// Plain renders without colour-dependent decoration: chips become
	// [label] and markdown uses glamour's no-TTY style. Set it when the output
	// profile cannot show colour, so the layout still reads well.
	Plain bool

	Text   color.Color
	Muted  color.Color
	Subtle color.Color
	Accent color.Color
	Good   color.Color
	Warn   color.Color
	Danger color.Color
	Info   color.Color
	Purple color.Color
	ChipBg color.Color
	Select color.Color
}

// NewTheme builds the palette for a dark or light background.
func NewTheme(dark bool) Theme {
	pick := lipgloss.LightDark(dark)
	c := lipgloss.Color
	return Theme{
		Dark:   dark,
		Text:   pick(c("#1F2328"), c("#E6EDF3")),
		Muted:  pick(c("#59636E"), c("#9198A1")),
		Subtle: pick(c("#D1D9E0"), c("#3D444D")),
		Accent: pick(c("#8250DF"), c("#B392F0")),
		Good:   pick(c("#1A7F37"), c("#3FB950")),
		Warn:   pick(c("#9A6700"), c("#D29922")),
		Danger: pick(c("#CF222E"), c("#F85149")),
		Info:   pick(c("#0969DA"), c("#58A6FF")),
		Purple: pick(c("#8250DF"), c("#BC8CFF")),
		ChipBg: pick(c("#EAEEF2"), c("#262C36")),
		Select: pick(c("#F0E8FF"), c("#2A2140")),
	}
}

// StatusColor gives each workflow lane a stable colour. The default lanes
// have deliberate colours; custom lanes are hashed onto the rest of the
// palette so they stay consistent between runs.
func (t Theme) StatusColor(status string) color.Color {
	switch strings.ToLower(status) {
	case "backlog", "todo", "icebox":
		return t.Muted
	case "ready", "next":
		return t.Info
	case "in-progress", "doing", "active", "wip":
		return t.Warn
	case "blocked":
		return t.Danger
	case "in-review", "review":
		return t.Purple
	case "done", "closed", "complete", "completed", "shipped":
		return t.Good
	}
	choices := []color.Color{t.Info, t.Purple, t.Warn, t.Good, t.Accent}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(status))
	return choices[hash.Sum32()%uint32(len(choices))]
}

// Fg is a style with only a foreground colour.
func (t Theme) Fg(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// Dim renders secondary text.
func (t Theme) Dim(s string) string { return t.Fg(t.Muted).Render(s) }

// Bold renders emphasised text in the main text colour.
func (t Theme) Bold(s string) string { return lipgloss.NewStyle().Bold(true).Render(s) }

// StatusBadge renders "● status" in the lane colour.
func (t Theme) StatusBadge(status string) string {
	return t.Fg(t.StatusColor(status)).Bold(true).Render("● " + status)
}

// Chip renders a label as a small tag.
func (t Theme) Chip(label string) string {
	if t.Plain {
		return "[" + label + "]"
	}
	return lipgloss.NewStyle().Foreground(t.Accent).Background(t.ChipBg).Padding(0, 1).Render(label)
}

// Chips renders labels as space-separated chips.
func (t Theme) Chips(labels []string) string {
	parts := make([]string, 0, len(labels))
	for _, label := range labels {
		parts = append(parts, t.Chip(label))
	}
	return strings.Join(parts, " ")
}

// Rule renders a section heading followed by a rule filling width.
func (t Theme) Rule(title string, width int) string {
	head := t.Fg(t.Accent).Bold(true).Render(title)
	fill := width - lipgloss.Width(title) - 4
	if fill < 3 {
		fill = 3
	}
	line := t.Fg(t.Subtle).Render
	return line("── ") + head + " " + line(strings.Repeat("─", fill))
}

// ToneColor maps an activity tone onto the palette.
func (t Theme) ToneColor(tone Tone) color.Color {
	switch tone {
	case ToneGood:
		return t.Good
	case ToneWarn:
		return t.Warn
	case ToneDanger:
		return t.Danger
	case ToneInfo:
		return t.Info
	case ToneAccent:
		return t.Accent
	}
	return t.Muted
}
