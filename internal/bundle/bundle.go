// Package bundle assembles a task's context bundle — the single most important
// output in docket. It is what a fresh agent session reads to resume work:
// description, history, decisions, artifacts, and relationships resolved to
// human-meaningful titles rather than bare ids.
package bundle

import (
	"sort"
	"time"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/project"
	"github.com/tvdavies/docket/internal/session"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

// TaskRef is a related task resolved to include its title.
type TaskRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ProjectRef is a project resolved to include its name.
type ProjectRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CommentView is a comment as presented in a bundle.
type CommentView struct {
	Author    string `json:"author"`
	Session   string `json:"session,omitempty"`
	CreatedAt string `json:"created_at"`
	Body      string `json:"body"`
}

// ActivityView is one chronological task history item assembled from the
// event log, comments, and session audit records.
type ActivityView struct {
	At      string         `json:"at"`
	Kind    string         `json:"kind"`
	Type    string         `json:"type"`
	Actor   string         `json:"actor,omitempty"`
	Session string         `json:"session,omitempty"`
	Body    string         `json:"body,omitempty"`
	Data    map[string]any `json:"data,omitempty"`

	sortTime time.Time
	order    int
}

// Bundle is the resolved context handoff payload.
type Bundle struct {
	ID             string               `json:"id"`
	Title          string               `json:"title"`
	Status         string               `json:"status"`
	Project        *ProjectRef          `json:"project,omitempty"`
	Labels         []string             `json:"labels"`
	Assignee       string               `json:"assignee,omitempty"`
	Wait           *task.Wait           `json:"wait,omitempty"`
	References     []task.Reference     `json:"references"`
	Description    string               `json:"description"`
	Relationships  map[string][]TaskRef `json:"relationships,omitempty"`
	Comments       []CommentView        `json:"comments"`
	Attachments    []*task.Attachment   `json:"attachments"`
	Sessions       []session.Entry      `json:"sessions"`
	ActiveSessions []session.Entry      `json:"active_sessions"`
	Activity       []ActivityView       `json:"activity"`
}

// Build assembles the bundle for a task. commentLimit > 0 keeps only the most
// recent N comments; <= 0 keeps all.
func Build(ws *workspace.Workspace, id string, commentLimit int) (*Bundle, error) {
	t, err := task.Load(ws, id)
	if err != nil {
		return nil, err
	}

	b := &Bundle{
		ID:          t.ID,
		Title:       t.Title,
		Status:      t.Status,
		Labels:      t.Labels,
		Assignee:    t.Assignee,
		Wait:        t.Wait,
		References:  t.References,
		Description: t.Description,
	}
	if b.Labels == nil {
		b.Labels = []string{}
	}
	if b.References == nil {
		b.References = []task.Reference{}
	}

	if t.Project != "" {
		if p, err := project.Load(ws, t.Project); err == nil {
			b.Project = &ProjectRef{ID: p.ID, Name: p.Name}
		} else {
			b.Project = &ProjectRef{ID: t.Project}
		}
	}

	if len(t.Relationships) > 0 {
		b.Relationships = map[string][]TaskRef{}
		for kind, ids := range t.Relationships {
			for _, rid := range ids {
				ref := TaskRef{ID: rid}
				if rt, err := task.Load(ws, rid); err == nil {
					ref.Title = rt.Title
				}
				b.Relationships[kind] = append(b.Relationships[kind], ref)
			}
		}
	}

	log, err := events.All(ws)
	if err != nil {
		return nil, err
	}

	comments, err := t.Comments()
	if err != nil {
		return nil, err
	}
	if commentLimit > 0 && len(comments) > commentLimit {
		comments = comments[len(comments)-commentLimit:]
	}
	b.Comments = make([]CommentView, 0, len(comments))
	for _, c := range comments {
		createdAt := c.CreatedAt.Format(time.RFC3339Nano)
		b.Comments = append(b.Comments, CommentView{
			Author: c.Author, Session: c.Session, CreatedAt: createdAt, Body: c.Body,
		})
		b.Activity = append(b.Activity, ActivityView{
			At: createdAt, Kind: "comment", Type: "comment",
			Actor: c.Author, Session: c.Session, Body: c.Body,
			sortTime: c.CreatedAt, order: len(b.Activity),
		})
	}

	// Older session audits used second-resolution timestamps. When their
	// corresponding task event exists, use its nanosecond timestamp for the
	// computed timeline while preserving the authoritative audit entry itself.
	sessionEventTimes := map[string][]string{}
	for _, event := range log {
		if event.Task != t.ID || (event.Type != events.TaskAttached && event.Type != events.TaskDetached) {
			continue
		}
		sessionID, _ := event.Data["session"].(string)
		key := event.Type + "\x00" + sessionID
		sessionEventTimes[key] = append(sessionEventTimes[key], event.Time)
	}
	b.Sessions, err = session.Entries(t)
	if err != nil {
		return nil, err
	}
	b.ActiveSessions = session.ActiveEntries(b.Sessions)
	for _, entry := range b.Sessions {
		eventType := events.TaskAttached
		if entry.Action == "detach" {
			eventType = events.TaskDetached
		}
		key := eventType + "\x00" + entry.Session
		activityAt := entry.At
		if matching := sessionEventTimes[key]; len(matching) > 0 {
			activityAt = matching[0]
			sessionEventTimes[key] = matching[1:]
		}
		b.Activity = append(b.Activity, ActivityView{
			At: activityAt, Kind: "session", Type: entry.Action,
			Actor: entry.Actor, Session: entry.Session,
			sortTime: parseActivityTime(activityAt), order: len(b.Activity),
		})
	}

	for _, event := range log {
		if event.Task != t.ID || event.Type == events.TaskCommented || event.Type == events.TaskAttached || event.Type == events.TaskDetached {
			continue
		}
		b.Activity = append(b.Activity, ActivityView{
			At: event.Time, Kind: "event", Type: event.Type,
			Actor: event.Actor, Data: event.Data,
			sortTime: parseActivityTime(event.Time), order: len(b.Activity),
		})
	}
	sort.SliceStable(b.Activity, func(left, right int) bool {
		if b.Activity[left].sortTime.Equal(b.Activity[right].sortTime) {
			return b.Activity[left].order < b.Activity[right].order
		}
		return b.Activity[left].sortTime.Before(b.Activity[right].sortTime)
	})

	atts, err := t.Attachments()
	if err != nil {
		return nil, err
	}
	if atts == nil {
		atts = []*task.Attachment{}
	}
	b.Attachments = atts

	return b, nil
}

func parseActivityTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
