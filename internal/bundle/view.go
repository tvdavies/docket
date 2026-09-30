package bundle

import (
	"fmt"

	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

// Views select how much of a task's context a read returns.
const (
	// ViewFull is the complete bundle with its established JSON contract.
	ViewFull = "full"
	// ViewCurrent is the task's current state without any history.
	ViewCurrent = "current"
	// ViewAgent is current state plus a bounded, de-duplicated activity tail.
	ViewAgent = "agent"
)

// DefaultAgentActivity is the activity tail an agent view returns when no
// explicit limit is given.
const DefaultAgentActivity = 20

// Options bounds a context read. Limits only shape output: they never change
// stored history or acknowledge inbox events.
type Options struct {
	View string
	// CommentLimit > 0 keeps only the most recent N comments, both in
	// Comments and in Activity.
	CommentLimit int
	// ActivityLimit > 0 returns at most N timeline items ending just before
	// ActivityBefore (or at the newest item when ActivityBefore is 0).
	ActivityLimit int
	// ActivityBefore > 0 is an exclusive timeline position from a previous
	// page's next_before, used to read older items.
	ActivityBefore int
}

// ActivityPage describes a slice of the chronological timeline. Positions
// count items oldest-first from 0, so they stay stable as new activity is
// appended, provided CommentLimit is the same between reads.
type ActivityPage struct {
	Total     int  `json:"total"`
	Start     int  `json:"start"`
	End       int  `json:"end"`
	Truncated bool `json:"truncated"`
	// NextBefore, when non-zero, is the --activity-before value that reads
	// the items older than this page.
	NextBefore int `json:"next_before,omitempty"`
}

func pageActivity(total, limit, before int) ActivityPage {
	end := total
	if before > 0 && before < end {
		end = before
	}
	start := 0
	if limit > 0 && end-limit > start {
		start = end - limit
	}
	return ActivityPage{
		Total: total, Start: start, End: end,
		Truncated: start > 0 || end < total, NextBefore: start,
	}
}

// Context is the compact projection returned by the current and agent views.
// Each fact appears once: comments and session audits live only in Activity,
// and legacy widget records keep their fallback text but not the raw record.
type Context struct {
	View            string               `json:"view"`
	ID              string               `json:"id"`
	Title           string               `json:"title"`
	Status          string               `json:"status"`
	CreatedAt       string               `json:"created_at"`
	UpdatedAt       string               `json:"updated_at"`
	Project         *ProjectRef          `json:"project,omitempty"`
	Labels          []string             `json:"labels"`
	Assignee        string               `json:"assignee,omitempty"`
	Wait            *task.Wait           `json:"wait,omitempty"`
	References      []task.Reference     `json:"references"`
	Description     string               `json:"description"`
	Relationships   map[string][]TaskRef `json:"relationships,omitempty"`
	Attachments     []*task.Attachment   `json:"attachments"`
	CommentsOmitted int                  `json:"comments_omitted,omitempty"`
	Activity        []ActivityView       `json:"activity,omitempty"`
	ActivityPage    ActivityPage         `json:"activity_page"`
}

// State returns the bundle's current-state fields as a Context with no
// activity, for callers that render state and history separately.
func (b *Bundle) State() *Context {
	return &Context{
		View: ViewFull, ID: b.ID, Title: b.Title, Status: b.Status,
		CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt, Project: b.Project,
		Labels: b.Labels, Assignee: b.Assignee, Wait: b.Wait, References: b.References,
		Description: b.Description, Relationships: b.Relationships,
		Attachments: b.Attachments, CommentsOmitted: b.CommentsOmitted,
	}
}

// ValidView reports whether view names a supported projection.
func ValidView(view string) error {
	switch view {
	case "", ViewFull, ViewCurrent, ViewAgent:
		return nil
	}
	return fmt.Errorf("unknown view %q (use %s, %s, or %s)", view, ViewCurrent, ViewAgent, ViewFull)
}

// BuildContext assembles the current or agent projection for a task.
func BuildContext(ws *workspace.Workspace, id string, options Options) (*Context, error) {
	if options.View != ViewCurrent && options.View != ViewAgent {
		return nil, fmt.Errorf("BuildContext needs the %s or %s view, not %q", ViewCurrent, ViewAgent, options.View)
	}
	full := options
	full.ActivityLimit, full.ActivityBefore = 0, 0
	b, err := BuildWith(ws, id, full)
	if err != nil {
		return nil, err
	}
	result := b.State()
	result.View = options.View
	if options.View == ViewCurrent {
		result.ActivityPage = pageActivity(len(b.Activity), 0, 0)
		result.ActivityPage.Start = result.ActivityPage.End
		result.ActivityPage.Truncated = result.ActivityPage.Total > 0
		result.ActivityPage.NextBefore = result.ActivityPage.End
		return result, nil
	}
	limit := options.ActivityLimit
	if limit <= 0 {
		limit = DefaultAgentActivity
	}
	result.ActivityPage = pageActivity(len(b.Activity), limit, options.ActivityBefore)
	result.Activity = make([]ActivityView, 0, result.ActivityPage.End-result.ActivityPage.Start)
	for _, item := range b.Activity[result.ActivityPage.Start:result.ActivityPage.End] {
		if item.Kind == "widget" {
			// Body already carries the record's fallback summary and links.
			item.Data = nil
		}
		result.Activity = append(result.Activity, item)
	}
	return result, nil
}
