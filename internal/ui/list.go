package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/tvdavies/docket/internal/task"
)

// TaskTable renders tasks as an aligned, status-coloured table that fits
// width. Titles shrink (with an ellipsis) before any other column does.
func (t Theme) TaskTable(tasks []*task.Task, width int) string {
	if width <= 0 {
		width = 100
	}
	headers := []string{"ID", "STATUS", "TITLE", "LABELS", "WAITING"}
	type row struct{ id, status, title, labels, waiting string }
	rows := make([]row, 0, len(tasks))
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = lipgloss.Width(header)
	}
	for _, value := range tasks {
		r := row{
			id:     value.ID,
			status: "● " + value.Status,
			title:  strings.Join(strings.Fields(value.Title), " "),
			labels: strings.Join(value.Labels, ", "),
		}
		if value.Wait != nil {
			r.waiting = "◷ " + value.Wait.Kind
		}
		rows = append(rows, r)
		for i, cell := range []string{r.id, r.status, r.title, r.labels, r.waiting} {
			widths[i] = max(widths[i], lipgloss.Width(cell))
		}
	}
	const gap = 2
	fixed := widths[0] + widths[1] + widths[3] + widths[4] + gap*4
	if available := width - fixed; widths[2] > available {
		widths[2] = max(available, 12)
	}

	pad := func(s string, w int) string {
		s = truncateWidth(s, w)
		return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
	}
	join := func(cells []string) string {
		return strings.TrimRight(strings.Join(cells, strings.Repeat(" ", gap)), " ")
	}

	var b strings.Builder
	header := make([]string, len(headers))
	for i, h := range headers {
		header[i] = t.Fg(t.Muted).Bold(true).Render(pad(h, widths[i]))
	}
	b.WriteString(join(header) + "\n")
	for _, r := range rows {
		status := r.status
		statusName := strings.TrimPrefix(status, "● ")
		cells := []string{
			t.Fg(t.Muted).Render(pad(r.id, widths[0])),
			t.Fg(t.StatusColor(statusName)).Render(pad(status, widths[1])),
			pad(r.title, widths[2]),
			t.Fg(t.Accent).Render(pad(r.labels, widths[3])),
			t.Fg(t.Warn).Render(pad(r.waiting, widths[4])),
		}
		b.WriteString(join(cells) + "\n")
	}
	return b.String()
}

// truncateWidth shortens s to at most w terminal cells, adding an ellipsis.
func truncateWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return stripTail(s, w-1) + "…"
}

func stripTail(s string, w int) string {
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > w {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String()
}

// Truncate shortens plain text to at most w terminal cells with an ellipsis.
func Truncate(s string, w int) string { return truncateWidth(s, w) }
