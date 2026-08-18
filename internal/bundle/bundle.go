// Package bundle assembles a task's context bundle — the single most important
// output in docket. It is what a fresh agent session reads to resume work:
// description, history, decisions, artifacts, and relationships resolved to
// human-meaningful titles rather than bare ids.
package bundle

import (
	"github.com/tvdavies/docket/internal/project"
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

// Bundle is the resolved context handoff payload.
type Bundle struct {
	ID            string               `json:"id"`
	Title         string               `json:"title"`
	Status        string               `json:"status"`
	Project       *ProjectRef          `json:"project,omitempty"`
	Labels        []string             `json:"labels"`
	Assignee      string               `json:"assignee,omitempty"`
	Description   string               `json:"description"`
	Relationships map[string][]TaskRef `json:"relationships,omitempty"`
	Comments      []CommentView        `json:"comments"`
	Attachments   []*task.Attachment   `json:"attachments"`
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
		Description: t.Description,
	}
	if b.Labels == nil {
		b.Labels = []string{}
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

	comments, err := t.Comments()
	if err != nil {
		return nil, err
	}
	if commentLimit > 0 && len(comments) > commentLimit {
		comments = comments[len(comments)-commentLimit:]
	}
	b.Comments = make([]CommentView, 0, len(comments))
	for _, c := range comments {
		b.Comments = append(b.Comments, CommentView{
			Author:    c.Author,
			Session:   c.Session,
			CreatedAt: c.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			Body:      c.Body,
		})
	}

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
