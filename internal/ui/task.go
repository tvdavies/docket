package ui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/task"
)

func stripANSI(s string) string { return xansi.Strip(s) }

// Task is the part of a context bundle the styled views draw. It unifies the
// full bundle and the current/agent projections.
type Task struct {
	View            string
	ID              string
	Title           string
	Status          string
	CreatedAt       string
	UpdatedAt       string
	Project         *bundle.ProjectRef
	Labels          []string
	Assignee        string
	Wait            *task.Wait
	References      []task.Reference
	Description     string
	Relationships   map[string][]bundle.TaskRef
	Attachments     []*task.Attachment
	CommentsOmitted int
	Activity        []bundle.ActivityView
	// Page describes which slice of the timeline Activity holds; nil means
	// the whole timeline.
	Page *bundle.ActivityPage
}

// FromBundle adapts a full bundle.
func FromBundle(b *bundle.Bundle) Task {
	t := fromContext(b.State())
	t.View = bundle.ViewFull
	t.Activity = b.Activity
	t.Page = b.ActivityPage
	return t
}

// FromContext adapts a current or agent projection.
func FromContext(c *bundle.Context) Task {
	t := fromContext(c)
	t.Activity = c.Activity
	page := c.ActivityPage
	t.Page = &page
	return t
}

func fromContext(c *bundle.Context) Task {
	return Task{
		View: c.View, ID: c.ID, Title: c.Title, Status: c.Status,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Project: c.Project,
		Labels: c.Labels, Assignee: c.Assignee, Wait: c.Wait, References: c.References,
		Description: c.Description, Relationships: c.Relationships,
		Attachments: c.Attachments, CommentsOmitted: c.CommentsOmitted,
	}
}

// Renderer draws a task at a fixed width. Now anchors relative times so
// output is reproducible in tests.
type Renderer struct {
	Theme Theme
	Width int
	Now   time.Time
}

func (r Renderer) width() int {
	if r.Width <= 0 {
		return 80
	}
	return r.Width
}

func (r Renderer) now() time.Time {
	if r.Now.IsZero() {
		return time.Now()
	}
	return r.Now
}

// Document is the complete `docket show` view: header, description, links,
// and activity.
func (r Renderer) Document(t Task) string {
	sections := []string{r.Header(t)}
	if description := r.Description(t); description != "" {
		sections = append(sections, r.Theme.Rule("Description", r.width())+"\n\n"+description)
	}
	if links := r.Links(t); links != "" {
		sections = append(sections, links)
	}
	if timeline := r.Timeline(t); timeline != "" {
		sections = append(sections, timeline)
	}
	return strings.Join(sections, "\n\n") + "\n"
}

// Header shows identity, status, ownership, labels, and any active wait.
func (r Renderer) Header(t Task) string {
	th := r.Theme
	var lines []string
	lines = append(lines, th.Fg(th.Muted).Bold(true).Render(t.ID)+"  "+th.StatusBadge(t.Status))
	title := lipgloss.NewStyle().Bold(true).Foreground(th.Text).Render(lipgloss.Wrap(t.Title, r.width(), ""))
	lines = append(lines, title)

	var meta []string
	if t.Assignee != "" {
		meta = append(meta, th.Fg(th.Info).Render("@"+t.Assignee))
	}
	if t.Project != nil {
		name := t.Project.Name
		if name == "" || name == t.Project.ID {
			meta = append(meta, "project "+t.Project.ID)
		} else {
			meta = append(meta, fmt.Sprintf("project %s %s", name, th.Dim("("+t.Project.ID+")")))
		}
	}
	if created := ParseTime(t.CreatedAt); !created.IsZero() {
		meta = append(meta, th.Dim("created "+Relative(created, r.now())))
	}
	if updated := ParseTime(t.UpdatedAt); !updated.IsZero() && t.UpdatedAt != t.CreatedAt {
		meta = append(meta, th.Dim("updated "+Relative(updated, r.now())))
	}
	if len(meta) > 0 {
		lines = append(lines, lipgloss.Wrap(strings.Join(meta, th.Dim(" · ")), r.width(), " "))
	}
	if len(t.Labels) > 0 {
		lines = append(lines, th.Chips(t.Labels))
	}
	if t.Wait != nil {
		lines = append(lines, "", r.waitBlock(t.Wait))
	}
	return strings.Join(lines, "\n")
}

func (r Renderer) waitBlock(w *task.Wait) string {
	th := r.Theme
	head := th.Fg(th.Warn).Bold(true).Render("◷ Waiting on " + w.Kind)
	if !w.Since.IsZero() {
		head += th.Dim(" · since " + Relative(w.Since, r.now()))
	}
	lines := []string{head}
	if w.Reason != "" {
		lines = append(lines, lipgloss.Wrap(w.Reason, r.width()-2, ""))
	}
	detail := "wait " + w.ID
	if w.Actor != "" {
		detail += " · set by " + w.Actor
	}
	lines = append(lines, th.Dim(detail))
	if w.Reference != "" {
		lines = append(lines, th.Fg(th.Info).Render("↗ "+w.Reference))
	}
	style := lipgloss.NewStyle().
		Border(lipgloss.ThickBorder(), false, false, false, true).
		BorderForeground(th.Warn).
		PaddingLeft(1)
	return style.Render(strings.Join(lines, "\n"))
}

// Description renders the task description as markdown.
func (r Renderer) Description(t Task) string {
	return r.Theme.Markdown(t.Description, r.width())
}

// Overview is the board's first detail tab: metadata and description.
func (r Renderer) Overview(t Task) string {
	description := r.Description(t)
	if description == "" {
		description = r.Theme.Dim("No description.")
	}
	out := r.Theme.Rule("Description", r.width()) + "\n\n" + description
	if timestamps := r.timestamps(t); timestamps != "" {
		out += "\n\n" + timestamps
	}
	return out
}

func (r Renderer) timestamps(t Task) string {
	var lines []string
	for _, field := range []struct{ label, value string }{{"created", t.CreatedAt}, {"updated", t.UpdatedAt}} {
		at := ParseTime(field.value)
		if at.IsZero() {
			continue
		}
		lines = append(lines, r.Theme.Dim(fmt.Sprintf("%-8s %s · %s", field.label, Absolute(at), Relative(at, r.now()))))
	}
	return strings.Join(lines, "\n")
}

// Links renders relationships, references, and attachments, omitting empty
// sections. It returns "" when the task has none.
func (r Renderer) Links(t Task) string {
	th := r.Theme
	var sections []string
	if len(t.Relationships) > 0 {
		var lines []string
		for _, kind := range slices.Sorted(maps.Keys(t.Relationships)) {
			for _, ref := range t.Relationships[kind] {
				line := th.Dim(fmt.Sprintf("%-12s", kind)) + " " + th.Bold(ref.ID)
				if ref.Title != "" {
					line += "  " + ref.Title
				}
				lines = append(lines, line)
			}
		}
		sections = append(sections, th.Rule("Relationships", r.width())+"\n\n"+strings.Join(lines, "\n"))
	}
	if len(t.References) > 0 {
		var lines []string
		for _, reference := range t.References {
			head := th.Fg(th.Info).Render("↗ " + reference.Kind)
			if reference.Title != "" {
				head += "  " + th.Bold(reference.Title)
			}
			head += "  " + th.Dim(reference.ID)
			lines = append(lines, head, "  "+th.Fg(th.Info).Render(reference.URL))
		}
		sections = append(sections, th.Rule("References", r.width())+"\n\n"+strings.Join(lines, "\n"))
	}
	if len(t.Attachments) > 0 {
		var lines []string
		for _, attachment := range t.Attachments {
			line := "⎘ " + th.Bold("attachments/"+attachment.File) + " " + th.Dim(attachmentMeta(attachment))
			if attachment.Caption != "" {
				line += "\n  " + attachment.Caption
			}
			lines = append(lines, line)
		}
		sections = append(sections, th.Rule("Attachments", r.width())+"\n\n"+strings.Join(lines, "\n"))
	}
	return strings.Join(sections, "\n\n")
}

func attachmentMeta(a *task.Attachment) string {
	parts := []string{a.Mime}
	if a.Bytes > 0 {
		parts = append(parts, humanBytes(a.Bytes))
	}
	if a.AddedBy != "" {
		parts = append(parts, "by "+a.AddedBy)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// Timeline renders the activity history as a chronological, day-grouped
// timeline, including the paging hints of bounded views.
func (r Renderer) Timeline(t Task) string {
	return r.timeline(t, "Activity", t.Activity, true)
}

// Comments renders only the comments from the timeline.
func (r Renderer) Comments(t Task) string {
	var comments []bundle.ActivityView
	for _, item := range t.Activity {
		if item.Kind == "comment" {
			comments = append(comments, item)
		}
	}
	out := r.timeline(t, "Comments", comments, false)
	if out == "" {
		return r.Theme.Rule("Comments", r.width()) + "\n\n" + r.Theme.Dim("No comments yet.")
	}
	return out
}

func (r Renderer) timeline(t Task, title string, items []bundle.ActivityView, hints bool) string {
	th := r.Theme
	var notes []string
	heading := title
	if hints {
		switch {
		case t.View == bundle.ViewCurrent:
			if t.Page != nil && t.Page.Total > 0 {
				notes = append(notes, fmt.Sprintf("%d items not shown; read them with: docket show %s --view agent", t.Page.Total, t.ID))
			}
		case t.Page != nil && t.Page.Truncated:
			heading = fmt.Sprintf("%s · %d–%d of %d", title, t.Page.Start+1, t.Page.End, t.Page.Total)
			if t.Page.NextBefore > 0 {
				notes = append(notes, fmt.Sprintf("%d older items not shown; read them with: docket show %s --view agent --activity-before %d", t.Page.NextBefore, t.ID, t.Page.NextBefore))
			}
			if newer := t.Page.Total - t.Page.End; newer > 0 {
				notes = append(notes, fmt.Sprintf("%d newer items not shown.", newer))
			}
		case len(items) > 0:
			heading = fmt.Sprintf("%s · %d", title, len(items))
		}
		if t.CommentsOmitted > 0 {
			notes = append(notes, fmt.Sprintf("%d older comments omitted by --comments.", t.CommentsOmitted))
		}
	} else if len(items) > 0 {
		heading = fmt.Sprintf("%s · %d", title, len(items))
	}
	if len(items) == 0 && len(notes) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(th.Rule(heading, r.width()))
	b.WriteString("\n")
	for _, note := range notes {
		b.WriteString("\n" + th.Dim(note))
	}
	if len(notes) > 0 {
		b.WriteString("\n")
	}
	lastDay := ""
	for _, item := range items {
		at := ParseTime(item.At)
		if day := dayLabel(at); day != lastDay {
			b.WriteString("\n" + th.Fg(th.Muted).Bold(true).Render(day) + "\n")
			lastDay = day
		}
		b.WriteString(r.activityItem(item, at))
	}
	return strings.TrimRight(b.String(), "\n")
}

func dayLabel(at time.Time) string {
	if at.IsZero() {
		return "Undated"
	}
	return at.Local().Format("Mon 2 Jan 2006")
}

func (r Renderer) activityItem(item bundle.ActivityView, at time.Time) string {
	th := r.Theme
	summary := Summarize(item)
	actor := item.Actor
	if actor == "" {
		actor = "system"
	}
	left := th.Fg(th.ToneColor(summary.Tone)).Bold(true).Render(summary.Icon) + "  " + th.Bold(actor) + " " + summary.Text
	if item.Session != "" && item.Kind != "session" {
		left += th.Dim(" · session " + item.Session)
	}
	var when string
	if !at.IsZero() {
		when = th.Dim(at.Local().Format("15:04") + " · " + Relative(at, r.now()))
	}
	line := left
	if when != "" {
		gap := r.width() - lipgloss.Width(left) - lipgloss.Width(when)
		if gap >= 2 {
			line = left + strings.Repeat(" ", gap) + when
		} else {
			line = lipgloss.Wrap(left, r.width(), "") + "\n   " + when
		}
	}
	out := line + "\n"

	// Event data is already phrased in the summary sentence; only free-text
	// bodies (comments, widget fallbacks) are rendered beneath it.
	if body := th.Markdown(item.Body, r.width()-4); body != "" {
		gutter := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(th.Subtle).
			PaddingLeft(1).
			MarginLeft(1)
		out += gutter.Render(body) + "\n"
	}
	return out
}
